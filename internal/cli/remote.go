package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/discord/disgoadapter"
)

// ───────────────────────────────────────────────────────────── test seams

var (
	// newConnector creates the Discord connector of local commands (plan, apply,
	// import, adopt). Tests replace it with the simulator.
	newConnector = func() discord.Connector { return disgoadapter.NewConnector() }
	// lookupSecret resolves bot token secretRefs.
	lookupSecret = os.LookupEnv
	// stdinReader and stdinIsTTY are used by confirmation prompts.
	stdinReader io.Reader = os.Stdin
	stdinIsTTY            = func() bool {
		fi, err := os.Stdin.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
	// httpClient is used for the runtime API and GitHub.
	httpClient = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: sameOriginRedirect}
	// pollInterval is the default polling interval of --wait.
	pollInterval = 4 * time.Second
)

// confirm asks a yes/no question on the terminal. Without a terminal it refuses unless
// the caller passed --yes.
func confirm(cmd *cobra.Command, question string) (bool, error) {
	if !stdinIsTTY() {
		return false, errors.New("refusing to continue without a terminal to confirm: pass --yes")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s Only 'yes' is accepted: ", question)
	line, err := bufio.NewReader(stdinReader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}

// ───────────────────────────────────────────────────────────── --remote flag

// remoteAuto is the value of --remote given without a URL.
const remoteAuto = "auto"

type remoteFlags struct {
	remote string
}

func (f *remoteFlags) register(cmd *cobra.Command, always bool) {
	usage := "runtime URL (--remote=URL); without a value: CODI_RUNTIME_URL (https, or http on localhost)"
	if always {
		usage = "runtime URL (default: CODI_RUNTIME_URL; https, or http on localhost)"
	}
	cmd.Flags().StringVar(&f.remote, "remote", "", usage)
	// `--remote` alone must not swallow the next argument (e.g. --comment).
	cmd.Flags().Lookup("remote").NoOptDefVal = remoteAuto
}

// splitRemoteArgs extracts a URL given as a positional argument after a bare --remote
// (`codi plan --remote https://codi.example .`): pflag cannot bind it to the flag.
func (f *remoteFlags) splitRemoteArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if (f.remote == remoteAuto || f.remote == "") && (strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://")) {
			f.remote = a
			continue
		}
		out = append(out, a)
	}
	return out
}

// resolveURL returns the runtime URL the bearer token is sent to: --remote=URL, else
// CODI_RUNTIME_URL. codi.yaml spec.runtime.url is never trusted on its own: on a pull
// request the file is controlled by the PR author, and sending the runtime token there
// would leak it. It is only accepted when it designates the same origin as the explicit
// pin (in which case the pin is used anyway).
func (f *remoteFlags) resolveURL(dir string) (string, error) {
	u := f.remote
	if u == remoteAuto {
		u = ""
	}
	if u == "" {
		u = os.Getenv("CODI_RUNTIME_URL")
	}
	if u == "" {
		if p, err := readProject(dir); err == nil && p.Spec.Runtime != nil && p.Spec.Runtime.URL != "" {
			return "", fmt.Errorf("codi.yaml spec.runtime.url is %s, but the runtime URL must be set explicitly: "+
				"set CODI_RUNTIME_URL (or pass --remote=URL); refusing to send the runtime token to a URL read from codi.yaml",
				p.Spec.Runtime.URL)
		}
		return "", errors.New("no runtime URL: pass --remote=URL or set CODI_RUNTIME_URL")
	}
	return checkRuntimeURL(u)
}

// checkRuntimeURL validates a URL the runtime token will be sent to: https, or http on a
// loopback host (development).
func checkRuntimeURL(raw string) (string, error) {
	pu, err := url.Parse(raw)
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return "", fmt.Errorf("invalid runtime URL %q (expected https://host[:port])", raw)
	}
	if pu.User != nil {
		return "", fmt.Errorf("invalid runtime URL %q: credentials in the URL are not allowed", raw)
	}
	if pu.Scheme == "http" && !isLoopbackHost(pu.Hostname()) {
		return "", fmt.Errorf("refusing to send the runtime token over plain http to %s: use https (http is only allowed for localhost)", pu.Host)
	}
	return strings.TrimRight(pu.Scheme+"://"+pu.Host+pu.EscapedPath(), "/"), nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// sameOriginRedirect refuses redirects that change the scheme or host: the bearer
// token (runtime or GitHub) must only reach the origin it was meant for.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || !strings.EqualFold(req.URL.Host, first.Host) {
		return fmt.Errorf("refusing to follow a redirect from %s to another origin (%s://%s)", first.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// ───────────────────────────────────────────────────────────── API client

type apiClient struct {
	base  string
	token string
}

func newAPIClient(base string) (*apiClient, error) {
	token := os.Getenv("CODI_RUNTIME_TOKEN")
	if token == "" {
		return nil, errors.New("CODI_RUNTIME_TOKEN is not set (the runtime's bearer token)")
	}
	return &apiClient{base: base, token: token}, nil
}

// apiError is an error answered by the runtime.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("runtime answered HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("runtime answered HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

func (c *apiClient) do(ctx context.Context, method, p string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return &apiError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &apiError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s response: %w", p, err)
	}
	return nil
}

// ───────────────────────────────────────────────────────────── repository archive

// Limits mirrored from the runtime (spec/api.md §7) for early, readable errors.
const (
	archiveMaxFiles    = 5000
	archiveMaxFile     = 16 << 20
	archiveMaxUpload   = 32 << 20
	archiveMaxContents = 64 << 20
)

// alwaysExcluded are directory names never uploaded, at any depth.
var alwaysExcluded = map[string]bool{".git": true, "node_modules": true, ".codi": true, ".codi-data": true, "dist": true}

// ignoreRules is a minimal .gitignore matcher (root .gitignore only): blank lines and
// comments are skipped, "!" negations are not supported, a trailing "/" matches
// directories, a pattern with a "/" is anchored to the root, others match base names.
type ignoreRules struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	glob     string
	dirOnly  bool
	anchored bool
}

func loadIgnore(root string) ignoreRules {
	var r ignoreRules
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return r
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		p := ignorePattern{}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.Contains(line, "/") {
			p.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		line = strings.TrimPrefix(line, "**/")
		if line == "" {
			continue
		}
		p.glob = line
		r.patterns = append(r.patterns, p)
	}
	return r
}

func (r ignoreRules) match(rel string, isDir bool) bool {
	base := path.Base(rel)
	for _, p := range r.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		target := base
		if p.anchored {
			target = rel
		}
		if ok, _ := path.Match(p.glob, target); ok {
			return true
		}
	}
	return false
}

// secretFile reports files never uploaded whatever .gitignore says.
func secretFile(name string) bool {
	return name == ".env" || strings.HasPrefix(name, ".env.")
}

// archiveRepo builds a .tar.gz of the repository rooted at dir (codi.yaml at the root of
// the archive). It returns the archive and the number of files.
func archiveRepo(dir string) ([]byte, int, error) {
	if _, err := os.Stat(filepath.Join(dir, "codi.yaml")); err != nil {
		return nil, 0, fmt.Errorf("%s is not a CODI repository (no codi.yaml)", dir)
	}
	ign := loadIgnore(dir)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := 0
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if alwaysExcluded[d.Name()] || ign.match(rel, true) {
				return filepath.SkipDir
			}
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o755, ModTime: time.Unix(0, 0)})
		}
		if !d.Type().IsRegular() || secretFile(d.Name()) || ign.match(rel, false) {
			return nil // symlinks and special files are refused by the runtime
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > archiveMaxFile {
			return fmt.Errorf("%s is larger than %d MiB, the runtime would refuse it", rel, archiveMaxFile>>20)
		}
		files++
		if files > archiveMaxFiles {
			return fmt.Errorf("the repository has more than %d files (exclude some in .gitignore)", archiveMaxFiles)
		}
		total += info.Size()
		if total > archiveMaxContents {
			return fmt.Errorf("the repository is larger than %d MiB", archiveMaxContents>>20)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o644, Size: info.Size(), ModTime: time.Unix(0, 0)}); err != nil {
			return err
		}
		n, err := io.Copy(tw, f)
		if err == nil && n != info.Size() {
			err = fmt.Errorf("%s changed while archiving", rel)
		}
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, 0, err
	}
	if buf.Len() > archiveMaxUpload {
		return nil, 0, fmt.Errorf("the repository archive is %d MiB, above the runtime's %d MiB limit", buf.Len()>>20, archiveMaxUpload>>20)
	}
	return buf.Bytes(), files, nil
}

// outputFlag validates -o.
func checkOutput(o string) error {
	switch o {
	case "text", "json":
		return nil
	}
	return fmt.Errorf("invalid --output %q (text | json)", o)
}

func writeJSONOut(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
