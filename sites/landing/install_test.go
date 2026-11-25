// Package landing tests public/install.sh, the script served at https://cicord.dev/install.sh
// (curl -fsSL https://cicord.dev/install.sh | sh). It runs under dash (or sh) against a local
// file server that plays GitHub Releases (CICORD_RELEASES_URL) with fake release assets, and
// stub uname and gh commands on PATH. Only this test file is Go: nothing here is published.
package landing

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeVersion = "1.2.3"

// fakeGh plays the GitHub CLI: $FAKE_GH is "nologin" (not logged in), "old" (no attestation
// command), "fail" (the attestation does not verify) or "ok"; the arguments of `attestation
// verify` are recorded in $FAKE_STATE/gh.
const fakeGh = `#!/bin/sh
case "$*" in
  "attestation verify --help") [ "$FAKE_GH" != old ] || exit 1; exit 0 ;;
  "auth status"*) [ "$FAKE_GH" != nologin ] || exit 1; exit 0 ;;
  "attestation verify "*) echo "$*" > "$FAKE_STATE/gh"; [ "$FAKE_GH" != fail ] || exit 1; exit 0 ;;
esac
exit 2
`

// fakeUname reports $FAKE_OS and $FAKE_ARCH.
const fakeUname = `#!/bin/sh
case "$1" in
  -m) echo "$FAKE_ARCH" ;;
  *) echo "$FAKE_OS" ;;
esac
`

// release is what the fake GitHub serves for v1.2.3.
type release struct {
	archives  map[string][]byte // name → content
	checksums string
}

func fakeArchive(t *testing.T, version string) []byte {
	t.Helper()
	bin := "#!/bin/sh\necho \"cicord " + version + " (abc1234)\"\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{"cicord", bin, 0o755}, {"LICENSE", "Apache-2.0\n", 0o644}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newRelease(t *testing.T) *release {
	t.Helper()
	r := &release{archives: map[string][]byte{}}
	for _, p := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		name := "cicord_" + fakeVersion + "_" + p + ".tar.gz"
		r.archives[name] = fakeArchive(t, fakeVersion)
		sum := sha256.Sum256(r.archives[name])
		r.checksums += hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	return r
}

// serve starts the fake GitHub Releases: /releases/latest redirects to the tag page of
// v1.2.3, /releases/download/v1.2.3/<file> serves the assets, anything else is a 404.
func serve(t *testing.T, r *release) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/releases/tag/v"+fakeVersion, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html></html>")) })
	mux.HandleFunc("/releases/download/v"+fakeVersion+"/", func(w http.ResponseWriter, req *http.Request) {
		name := filepath.Base(req.URL.Path)
		if name == "checksums.txt" {
			_, _ = w.Write([]byte(r.checksums))
			return
		}
		if b, ok := r.archives[name]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, req)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/releases"
}

type result struct {
	out, dir, state string
	err             error
}

// installed is the path of the installed binary, or "" when there is none.
func (r result) installed() string {
	p := filepath.Join(r.dir, "cicord")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// run runs install.sh under dash when available (it has no bash extensions), else sh, with
// a fresh install directory and the stubs first on PATH.
func run(t *testing.T, base string, env ...string) result {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := exec.LookPath("wget"); err != nil {
			t.Skip("neither curl nor wget is installed")
		}
	}
	root := t.TempDir()
	bin, state, dir := filepath.Join(root, "bin"), filepath.Join(root, "state"), filepath.Join(root, "install", "bin")
	for _, d := range []string{bin, state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"gh": fakeGh, "uname": fakeUname} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shell := "dash"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}
	script, _ := filepath.Abs(filepath.Join("public", "install.sh"))
	cmd := exec.CommandContext(context.Background(), shell, script)
	cmd.Dir = root
	cmd.Env = append([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + root,
		"CICORD_RELEASES_URL=" + base,
		"CICORD_INSTALL_DIR=" + dir,
		"FAKE_STATE=" + state,
		"FAKE_OS=Linux",
		"FAKE_ARCH=x86_64",
		"FAKE_GH=nologin",
	}, env...)
	out, err := cmd.CombinedOutput()
	if testing.Verbose() {
		t.Logf("install.sh %s:\n%s", strings.Join(env, " "), out)
	}
	return result{out: string(out), dir: dir, state: state, err: err}
}

func TestInstallLatest(t *testing.T) {
	r := run(t, serve(t, newRelease(t)))
	if r.err != nil {
		t.Fatalf("install.sh failed: %v\n%s", r.err, r.out)
	}
	for _, want := range []string{
		"downloading cicord 1.2.3 for linux/amd64",
		"checksum verified",
		"build provenance not verified: gh is not logged in",
		"gh attestation verify cicord_1.2.3_linux_amd64.tar.gz --repo cicord/cicord",
		"Installed cicord 1.2.3 (abc1234) at " + filepath.Join(r.dir, "cicord"),
		"is not on your PATH",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	fi, err := os.Stat(filepath.Join(r.dir, "cicord"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("cicord is not installed with mode 0755: %v %v", fi, err)
	}
}

func TestInstallVersionAndArchWithAttestation(t *testing.T) {
	r := run(t, serve(t, newRelease(t)), "CICORD_VERSION=v1.2.3", "FAKE_OS=Darwin", "FAKE_ARCH=arm64", "FAKE_GH=ok")
	if r.err != nil || r.installed() == "" {
		t.Fatalf("install.sh failed: %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "for darwin/arm64") || !strings.Contains(r.out, "build provenance verified") {
		t.Errorf("unexpected output:\n%s", r.out)
	}
	args, _ := os.ReadFile(filepath.Join(r.state, "gh"))
	if !strings.Contains(string(args), "cicord_1.2.3_darwin_arm64.tar.gz --repo cicord/cicord --signer-workflow cicord/cicord/.github/workflows/release.yml") {
		t.Errorf("gh attestation verify was called with %q", args)
	}
}

func TestInstallSkipsAttestationWithOldGh(t *testing.T) {
	r := run(t, serve(t, newRelease(t)), "FAKE_ARCH=aarch64", "FAKE_GH=old")
	if r.err != nil || r.installed() == "" {
		t.Fatalf("install.sh failed: %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "for linux/arm64") || !strings.Contains(r.out, "this gh has no 'attestation' command") {
		t.Errorf("unexpected output:\n%s", r.out)
	}
}

func TestInstallFailures(t *testing.T) {
	bad := newRelease(t)
	bad.archives["cicord_1.2.3_linux_amd64.tar.gz"] = fakeArchive(t, "6.6.6") // not the listed one
	unlisted := newRelease(t)
	unlisted.checksums = ""
	for _, tc := range []struct {
		name string
		rel  *release
		env  []string
		want string
	}{
		{"bad checksum", bad, nil, "checksum mismatch for cicord_1.2.3_linux_amd64.tar.gz"},
		{"archive not in checksums.txt", unlisted, nil, "cicord_1.2.3_linux_amd64.tar.gz is not listed in checksums.txt"},
		{"attestation does not verify", newRelease(t), []string{"FAKE_GH=fail"}, "attestation of cicord_1.2.3_linux_amd64.tar.gz does not verify"},
		{"missing version", newRelease(t), []string{"CICORD_VERSION=9.9.9"}, "cicord 9.9.9 not found"},
		{"invalid version", newRelease(t), []string{"CICORD_VERSION=latest"}, "'latest' is not a version"},
		{"unknown arch", newRelease(t), []string{"FAKE_ARCH=riscv64"}, "unsupported architecture riscv64"},
		{"unknown OS", newRelease(t), []string{"FAKE_OS=FreeBSD"}, "unsupported operating system FreeBSD"},
		{"windows", newRelease(t), []string{"FAKE_OS=MINGW64_NT-10.0"}, "Windows: download cicord_<version>_windows_amd64.zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, serve(t, tc.rel), tc.env...)
			if r.err == nil {
				t.Fatalf("install.sh succeeded:\n%s", r.out)
			}
			if !strings.Contains(r.out, "cicord-install: error: ") || !strings.Contains(r.out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, r.out)
			}
			if p := r.installed(); p != "" {
				t.Errorf("%s was installed", p)
			}
		})
	}
}
