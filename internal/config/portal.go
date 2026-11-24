package config

import (
	"errors"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// PortalImageTypes are the image types served by the portal logo and banner endpoints.
var PortalImageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml",
}

// PortalImageType returns the content type of a portal image file ("" when unsupported).
func PortalImageType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ct, ok := PortalImageTypes[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); strings.HasPrefix(ct, "image/") && !strings.Contains(ct, "svg") {
		return ct
	}
	return ""
}

// ResolveRepoPath resolves p (relative to the repository file declaring it, given as a
// slash path relative to the root) to a cleaned slash path relative to the root. ok is
// false when the result escapes the repository.
func ResolveRepoPath(declaringFile, p string) (rel string, ok bool) {
	rel = path.Clean(path.Join(path.Dir(declaringFile), p))
	return rel, rel != "." && filepath.IsLocal(filepath.FromSlash(rel))
}

// LocalPathError reports why a repository path cannot be served: it must stay inside root
// and none of its components may be a symbolic link (snapshots drop symlinks, and a link
// could point outside the repository). Missing files are returned as fs.ErrNotExist.
func LocalPathError(root, rel string) error {
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			return errSymlink
		}
	}
	return nil
}

var errSymlink = errors.New("symbolic link")

// checkPortal validates cicord.yaml spec.portal.path and the portal images of servers.
//
// Problems that would make the runtime serve something unsafe or wrong are errors; a
// portal or image that is not there yet is a warning, so that a missing portal build never
// blocks the reconciliation of servers and bots (the runtime falls back to the default
// portal and the theme's fallbacks).
func (l *loader) checkPortal(repo *Repo) {
	if p := repo.Project; p != nil && p.Spec.Portal != nil && p.Spec.Portal.Path != "" {
		const ptr = "/spec/portal/path"
		file := "cicord.yaml"
		line := 0
		if doc := l.docs[file]; doc != nil {
			line = doc.lineOf(ptr)
		}
		rel, ok := ResolveRepoPath(file, p.Spec.Portal.Path)
		switch err := LocalPathError(l.root, rel); {
		case !ok:
			l.sink.errorf(file, line, ptr, "the portal path %q must be a directory inside the repository", p.Spec.Portal.Path)
		case errors.Is(err, errSymlink):
			l.sink.errorf(file, line, ptr, "the portal path %q goes through a symbolic link: commit the files themselves", p.Spec.Portal.Path)
		case errors.Is(err, fs.ErrNotExist):
			l.sink.warnf(file, line, ptr, "the portal path %q does not exist: build the portal (cicord portal build) and commit its output; until then the runtime serves the default portal", p.Spec.Portal.Path)
		case err != nil:
			l.sink.errorf(file, line, ptr, "%v", err)
		default:
			if st, _ := os.Stat(filepath.Join(l.root, filepath.FromSlash(rel))); st == nil || !st.IsDir() {
				l.sink.errorf(file, line, ptr, "the portal path %q must be a directory (the build output containing index.html)", p.Spec.Portal.Path)
			} else if err := LocalPathError(l.root, rel+"/index.html"); err != nil {
				l.sink.warnf(file, line, ptr, "%s/index.html not found: is %q the build output of the portal? Until it exists the runtime serves the default portal", rel, p.Spec.Portal.Path)
			}
		}
	}
	for _, key := range sortedKeys(repo.Servers) {
		s := repo.Servers[key]
		sp := s.Spec.Portal
		if sp == nil {
			continue
		}
		if sp.Theme != nil {
			l.checkPortalImage(s, "/spec/portal/theme/logo", sp.Theme.Logo)
		}
		l.checkPortalImage(s, "/spec/portal/banner", sp.Banner)
	}
}

func (l *loader) checkPortalImage(s *Server, ptr, p string) {
	if p == "" || strings.Contains(p, "://") {
		return
	}
	line := 0
	if doc := l.docs[s.File]; doc != nil {
		line = doc.lineOf(ptr)
	}
	rel, ok := ResolveRepoPath(s.File, p)
	if !ok {
		l.sink.errorf(s.File, line, ptr, "%q must stay inside the repository", p)
		return
	}
	if PortalImageType(rel) == "" {
		l.sink.errorf(s.File, line, ptr, "%q is not a supported image (png, jpg, gif, webp, avif, svg)", p)
		return
	}
	switch err := LocalPathError(l.root, rel); {
	case errors.Is(err, errSymlink):
		l.sink.errorf(s.File, line, ptr, "%q goes through a symbolic link: commit the image itself", p)
	case errors.Is(err, fs.ErrNotExist):
		l.sink.warnf(s.File, line, ptr, "image %q not found (%s): the portal will not show it", p, rel)
	case err != nil:
		l.sink.errorf(s.File, line, ptr, "%v", err)
	}
}
