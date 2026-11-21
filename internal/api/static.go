package api

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// staticHandler serves an fs.FS under prefix with an SPA fallback to index.html. API
// prefixes are never shadowed: they get a JSON 404.
func (s *server) staticHandler(fsys fs.FS, prefix string, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, reserved := range []string{"/api/", "/auth/", "/hooks/"} {
			if strings.HasPrefix(p, reserved) || p == strings.TrimSuffix(reserved, "/") {
				notFoundHandler(w, r)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "invalid", "method not allowed")
			return
		}
		if fsys == nil {
			notFoundHandler(w, r)
			return
		}
		if admin {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(p, prefix)), "/")
		if rel != "" && rel != "index.html" {
			if st, err := fs.Stat(fsys, rel); err == nil && !st.IsDir() {
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
}

func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
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
