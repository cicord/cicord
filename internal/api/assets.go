package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // image.DecodeConfig formats
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/state"
	"github.com/npjonath/cicord/internal/writeback"
)

// Server images editable from the admin UI (spec/api.md §4, "Server assets"): the file
// is written into the repository next to the server YAML and spec.profile.<kind> is
// pointed at it, in one write-back (commit or pull request per cicord.yaml policies).

// maxAssetBytes bounds an uploaded server image (Discord accepts up to 10 MiB).
const maxAssetBytes = 8 << 20

// assetKinds lists the editable images and the formats Discord accepts for each.
var assetKinds = map[string]struct {
	formats   map[string]bool
	minW      int
	minH      int
	recommend string
}{
	"icon":   {formats: map[string]bool{"png": true, "jpeg": true, "gif": true, "webp": true}, minW: 128, minH: 128, recommend: "a square image of at least 512×512"},
	"banner": {formats: map[string]bool{"png": true, "jpeg": true, "gif": true, "webp": true}, minW: 960, minH: 540, recommend: "a 16:9 image of at least 960×540"},
	"splash": {formats: map[string]bool{"png": true, "jpeg": true, "webp": true}, minW: 1920, minH: 1080, recommend: "a 16:9 image of at least 1920×1080"},
}

var formatExt = map[string]string{"png": "png", "jpeg": "jpg", "gif": "gif", "webp": "webp"}

type assetPutResponse struct {
	Mode           string `json:"mode"`
	Revision       string `json:"revision"`
	PullRequestURL string `json:"pullRequestUrl"`
	// Path is the repository path of the written file; Value the YAML value set.
	Path     string   `json:"path"`
	Value    string   `json:"value"`
	Format   string   `json:"format"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Warnings []string `json:"warnings"`
}

// sniffImage identifies a PNG / JPEG / GIF / WebP image from its bytes and returns its
// format and dimensions. Anything else (SVG, BMP, truncated files…) is an error.
func sniffImage(b []byte) (format string, w, h int, err error) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}),
		bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		cfg, f, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return "", 0, 0, fmt.Errorf("the image is corrupt or truncated: %v", err)
		}
		return f, cfg.Width, cfg.Height, nil
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		w, h, err := webpSize(b)
		if err != nil {
			return "", 0, 0, err
		}
		return "webp", w, h, nil
	}
	return "", 0, 0, errors.New("unsupported image type: use PNG, JPEG, GIF or WebP")
}

// webpSize reads the canvas size of a WebP file (VP8, VP8L or VP8X chunk).
func webpSize(b []byte) (int, int, error) {
	bad := errors.New("the WebP image is corrupt or truncated")
	if len(b) < 30 {
		return 0, 0, bad
	}
	chunk, data := string(b[12:16]), b[20:]
	switch chunk {
	case "VP8 ":
		// frame tag (3 bytes) + start code 9d 01 2a + 14-bit width / height
		if len(data) < 10 || data[3] != 0x9d || data[4] != 0x01 || data[5] != 0x2a {
			return 0, 0, bad
		}
		w := int(binary.LittleEndian.Uint16(data[6:8]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(data[8:10]) & 0x3fff)
		return w, h, nil
	case "VP8L":
		if len(data) < 5 || data[0] != 0x2f {
			return 0, 0, bad
		}
		v := binary.LittleEndian.Uint32(data[1:5])
		return int(v&0x3fff) + 1, int((v>>14)&0x3fff) + 1, nil
	case "VP8X":
		if len(data) < 10 {
			return 0, 0, bad
		}
		w := int(data[4]) | int(data[5])<<8 | int(data[6])<<16
		h := int(data[7]) | int(data[8])<<8 | int(data[9])<<16
		return w + 1, h + 1, nil
	}
	return 0, 0, bad
}

// assetValue returns the declared profile image of a server ("" when not set).
func assetValue(srv *config.Server, kind string) string {
	p := srv.Spec.Profile
	if p == nil {
		return ""
	}
	switch kind {
	case "icon":
		return p.Icon
	case "banner":
		return p.Banner
	case "splash":
		return p.Splash
	}
	return ""
}

func (s *server) handleAssetPut(w http.ResponseWriter, r *http.Request) {
	serverKey, kind := r.PathValue("server"), r.PathValue("kind")
	id, _, err := s.requireRole(r, serverKey, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	spec, ok := assetKinds[kind]
	if !ok {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown server image %q (icon, banner or splash)", kind))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAssetBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.fail(w, httpErr(http.StatusRequestEntityTooLarge, "too_large", "the image is larger than %d MiB", maxAssetBytes>>20))
			return
		}
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "read body: %v", err))
		return
	}
	if len(data) == 0 {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "empty body: send the image bytes"))
		return
	}
	format, width, height, err := sniffImage(data)
	if err != nil {
		s.fail(w, httpErr(http.StatusUnsupportedMediaType, "unsupported_media_type", "%v", err))
		return
	}
	if !spec.formats[format] {
		s.fail(w, httpErr(http.StatusUnsupportedMediaType, "unsupported_media_type", "Discord does not accept %s images for the server %s", strings.ToUpper(format), kind))
		return
	}
	warnings := []string{}
	if width < spec.minW || height < spec.minH {
		warnings = append(warnings, fmt.Sprintf("the image is %d×%d: Discord recommends %s", width, height, spec.recommend))
	}
	if kind == "icon" && width != height {
		warnings = append(warnings, fmt.Sprintf("the icon is not square (%d×%d): Discord crops it to a circle", width, height))
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rev, _ := s.rt.Revision()
	if want := r.URL.Query().Get("revision"); want != "" && want != rev.ID {
		s.fail(w, httpErr(http.StatusConflict, "conflict",
			"the configuration changed since revision %s (active: %s); reload and retry", want, rev.ID))
		return
	}
	srv, ok := s.rt.ServerConfig(serverKey)
	repo := s.rt.Repo()
	if !ok || repo == nil || srv.File == "" {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown server %q", serverKey))
		return
	}
	value := "./assets/" + serverKey + "-" + kind + "." + formatExt[format]
	rel, ok := config.ResolveRepoPath(srv.File, value)
	if !ok {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "cannot place the image next to %s", srv.File))
		return
	}
	out := assetPutResponse{Path: rel, Value: value, Format: format, Width: width, Height: height, Warnings: warnings}

	// Same file already declared: nothing to write.
	if assetValue(srv, kind) == value {
		if cur, err := os.ReadFile(filepath.Join(repo.Root, filepath.FromSlash(rel))); err == nil && bytes.Equal(cur, data) {
			out.Mode, out.Revision = writeback.ModeCommit, rev.ID
			writeJSON(w, http.StatusOK, out)
			return
		}
	}

	if s.opts.Writer == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back is not configured"))
		return
	}
	wb := repo.Project.Spec.WriteBack
	if wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
		return
	}
	edits := map[string]writeback.EditFunc{
		rel: func([]byte) ([]byte, error) { return data, nil },
		srv.File: func(cur []byte) ([]byte, error) {
			if cur == nil {
				return nil, fmt.Errorf("%s no longer exists", srv.File)
			}
			return writeback.SetYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("profile"), writeback.Key(kind)}, value)
		},
	}
	mode := writeback.ModeFor(wb, []string{rel, srv.File})
	prevErr := s.rt.Status().LastError
	res, err := s.opts.Writer.Write(r.Context(), writeback.Request{
		Edits:   edits,
		Message: fmt.Sprintf("server(%s): update %s\n\nFile: %s (%s, %d×%d, %d bytes)", serverKey, kind, rel, format, width, height, len(data)),
		Actor:   id.actor(),
		Source:  "admin-ui",
		Mode:    mode,
	})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			s.fail(w, httpErr(http.StatusConflict, "conflict", "%v", err))
			return
		}
		s.log.Error("write-back failed", "err", err)
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back failed: %v", err))
		return
	}
	if res.Mode == "" {
		res.Mode = mode
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: id.auditActor(), Action: "server.asset",
		Target: serverKey + "/" + kind, Details: mustJSON(map[string]any{
			"kind": kind, "path": rel, "format": format, "bytes": len(data), "mode": res.Mode,
			"revision": res.Revision, "pullRequestUrl": res.PullRequestURL,
		})})
	out.Mode, out.Revision, out.PullRequestURL = res.Mode, res.Revision, res.PullRequestURL
	if res.Mode == writeback.ModeCommit {
		s.rt.Trigger()
		if nr, ok := s.waitActivation(r.Context(), rev.SHA, res.SHA, prevErr); ok {
			out.Revision = nr.ID
		}
	} else {
		out.Revision = ""
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAssetGet serves the declared repository image of a server (active revision),
// so the admin UI can preview it when the live Discord CDN URL is unavailable.
func (s *server) handleAssetGet(w http.ResponseWriter, r *http.Request) {
	serverKey, kind := r.PathValue("server"), r.PathValue("kind")
	if _, _, err := s.requireRole(r, serverKey, roleModerator); err != nil {
		s.fail(w, err)
		return
	}
	srv, ok := s.rt.ServerConfig(serverKey)
	repo := s.rt.Repo()
	if _, known := assetKinds[kind]; !known || !ok || repo == nil {
		notFoundHandler(w, r)
		return
	}
	value := assetValue(srv, kind)
	rel, ok := config.ResolveRepoPath(srv.File, value)
	ct := config.PortalImageType(rel)
	if value == "" || !ok || ct == "" || strings.Contains(ct, "svg") || config.LocalPathError(repo.Root, rel) != nil {
		notFoundHandler(w, r)
		return
	}
	root, err := os.OpenRoot(repo.Root)
	if err != nil {
		notFoundHandler(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		notFoundHandler(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() || st.Size() > maxPortalImage {
		notFoundHandler(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, path.Base(rel), st.ModTime(), f)
}
