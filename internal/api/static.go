package api

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/npjonath/codi/internal/config"
)

// reservedPrefixes are never shadowed by a static UI: unknown paths there are JSON 404s.
var reservedPrefixes = []string{"/api/", "/auth/", "/hooks/", "/admin/"}

// reservedPaths are exact paths owned by the API (other methods than the routed ones).
var reservedPaths = []string{"/healthz", "/readyz"}

func isReservedPath(p string) bool {
	for _, reserved := range reservedPrefixes {
		if strings.HasPrefix(p, reserved) || p == strings.TrimSuffix(reserved, "/") {
			return true
		}
	}
	for _, reserved := range reservedPaths {
		if p == reserved || strings.HasPrefix(p, reserved+"/") {
			return true
		}
	}
	return false
}

// staticHandler serves an fs.FS under prefix with an SPA fallback to index.html. API
// prefixes are never shadowed: they get a JSON 404.
func (s *server) staticHandler(fsys fs.FS, prefix string, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		serveSPA(w, r, fsys, prefix)
	}
}

// portalHandler serves the community portal at /: the custom portal of the active
// revision (codi.yaml spec.portal.path) when it has been built, otherwise the embedded
// default portal. The directory is resolved per request, so a new revision swaps the
// served files without a restart.
func (s *server) portalHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isReservedPath(r.URL.Path) {
			notFoundHandler(w, r)
			return
		}
		if root := s.customPortalRoot(); root != nil {
			defer root.Close()
			w.Header().Set("X-Codi-Portal", "repository")
			serveSPA(w, r, root.FS(), "/")
			return
		}
		serveSPA(w, r, s.opts.Portal, "/")
	}
}

// customPortalRoot opens the custom portal directory of the active revision, or returns
// nil (no spec.portal.path, not built, or unsafe). os.Root confines every file access to
// that directory: ".." and symbolic links leading outside are rejected.
func (s *server) customPortalRoot() *os.Root {
	if s.rt == nil {
		return nil
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil || repo.Project.Spec.Portal == nil || repo.Project.Spec.Portal.Path == "" {
		return nil
	}
	return openPortalDir(repo.Root, repo.Project.Spec.Portal.Path)
}

// openPortalDir opens <root>/<p> (p relative to codi.yaml) when it stays in the repository,
// is reached without symbolic links and contains index.html.
func openPortalDir(root, p string) *os.Root {
	rel, ok := config.ResolveRepoPath("codi.yaml", p)
	if !ok || config.LocalPathError(root, rel) != nil {
		return nil
	}
	dir, err := os.OpenRoot(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	if st, err := dir.Stat("index.html"); err != nil || st.IsDir() {
		dir.Close()
		return nil
	}
	return dir
}

func serveSPA(w http.ResponseWriter, r *http.Request, fsys fs.FS, prefix string) {
	p := r.URL.Path
	if isReservedPath(p) && prefix == "/" {
		notFoundHandler(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "invalid", "method not allowed")
		return
	}
	if fsys == nil {
		notFoundHandler(w, r)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(p, prefix)), "/")
	if rel != "" && rel != "index.html" {
		if st, err := fs.Stat(fsys, rel); err == nil && !st.IsDir() {
			if strings.HasPrefix(rel, "assets/") {
				// Vite puts content-hashed files under assets/.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			serveFile(w, r, fsys, rel)
			return
		}
		// Missing asset files are real 404s; other paths are client-side routes.
		if ext := path.Ext(rel); ext != "" && ext != ".html" {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	serveFile(w, r, fsys, "index.html")
}

func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(data)))
}
