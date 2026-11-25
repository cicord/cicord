package api

import (
	"archive/tar"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/reconcile"
)

// Limits of an uploaded repository archive (POST /api/v1/plan).
const (
	maxArchiveFiles     = 5000
	maxArchiveTotal     = 64 << 20
	maxArchiveFileBytes = 16 << 20
)

func (s *server) requireBearer(r *http.Request) error {
	if s.opts.RuntimeToken == "" {
		return httpErr(http.StatusUnauthorized, "unauthenticated", "machine endpoints are disabled (no runtime token configured)")
	}
	if !s.isBearer(r) {
		return httpErr(http.StatusUnauthorized, "unauthenticated", "a valid runtime bearer token is required")
	}
	return nil
}

func (s *server) handleSync(w http.ResponseWriter, r *http.Request) {
	if err := s.requireBearer(r); err != nil {
		s.fail(w, err)
		return
	}
	if s.rt == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "runtime not available"))
		return
	}
	s.rt.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

func (s *server) handlePlanUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.requireBearer(r); err != nil {
		s.fail(w, err)
		return
	}
	if s.rt == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "runtime not available"))
		return
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch strings.TrimSpace(ct) {
	case "application/gzip", "application/x-gzip", "application/x-tar+gzip", "application/octet-stream", "":
	default:
		s.fail(w, httpErr(http.StatusUnsupportedMediaType, "invalid", "expected a .tar.gz body (Content-Type: application/gzip)"))
		return
	}
	tmp, err := os.MkdirTemp("", "cicord-plan-*")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer os.RemoveAll(tmp)
	body := http.MaxBytesReader(w, r.Body, s.opts.MaxUploadBytes)
	if err := extractTarGz(body, tmp); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.fail(w, httpErr(http.StatusRequestEntityTooLarge, "invalid", "archive too large"))
			return
		}
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "invalid archive: %v", err))
		return
	}
	root, err := findRepoRoot(tmp)
	if err != nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "%v", err))
		return
	}
	ds, plans, err := s.rt.PlanDir(r.Context(), root)
	if err != nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "plan: %v", err))
		return
	}
	if ds == nil {
		ds = config.Diagnostics{}
	}
	out := make([]reconcile.Plan, 0, len(plans))
	for _, p := range plans {
		out = append(out, emptyPlan(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": !ds.HasErrors(), "diagnostics": ds, "plans": out})
}

// extractTarGz extracts regular files and directories into dst, refusing absolute
// paths, "..", links and special files, and enforcing size/count limits. Every file
// operation goes through an os.Root on dst, so nothing can be written outside it even if a
// name slipped past the checks.
func extractTarGz(r io.Reader, dst string) error {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name == "" || name == "." {
			continue
		}
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || !filepath.IsLocal(filepath.FromSlash(strings.TrimSuffix(name, "/"))) {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		if MacOSJunk(path.Base(strings.TrimSuffix(name, "/"))) {
			continue // the body is skipped by the next tr.Next()
		}
		target := filepath.FromSlash(strings.TrimSuffix(name, "/")) // relative to root
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			files++
			if files > maxArchiveFiles {
				return fmt.Errorf("too many files (max %d)", maxArchiveFiles)
			}
			if h.Size > maxArchiveFileBytes {
				return fmt.Errorf("%s is too large", h.Name)
			}
			total += h.Size
			if total > maxArchiveTotal {
				return fmt.Errorf("archive content too large (max %d bytes)", maxArchiveTotal)
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			// nosemgrep: go.lang.security.decompression_bomb.potential-dos-via-decompression-bomb -- bounded by maxArchiveFileBytes, maxArchiveTotal and maxArchiveFiles above
			n, err := io.Copy(f, io.LimitReader(tr, h.Size+1))
			cerr := f.Close()
			if err != nil {
				return err
			}
			if cerr != nil {
				return cerr
			}
			if n != h.Size {
				return fmt.Errorf("%s: size mismatch", h.Name)
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
		default:
			return fmt.Errorf("%s: only regular files and directories are allowed", h.Name)
		}
	}
}

// MacOSJunk reports the metadata files macOS puts into archives and folders, never part of
// a repository: AppleDouble "._<name>" files (extended attributes, which tar on macOS adds
// next to every file and are not valid YAML) and ".DS_Store".
func MacOSJunk(base string) bool {
	return strings.HasPrefix(base, "._") || base == ".DS_Store"
}

// findRepoRoot accepts cicord.yaml at the root or inside a single top-level directory.
func findRepoRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "cicord.yaml")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		sub := filepath.Join(dir, entries[0].Name())
		if _, err := os.Stat(filepath.Join(sub, "cicord.yaml")); err == nil {
			return sub, nil
		}
	}
	return "", errors.New("the archive does not contain a cicord.yaml")
}

// ───────────────────────────────────────────────────────────── GitHub webhook

func (s *server) trackedBranch() string {
	if s.opts.Branch != "" {
		return s.opts.Branch
	}
	if s.rt != nil {
		if repo := s.rt.Repo(); repo != nil && repo.Project != nil && repo.Project.Spec.Source != nil && repo.Project.Spec.Source.Branch != "" {
			return repo.Project.Spec.Source.Branch
		}
	}
	return "main"
}

func (s *server) handleGitHubHook(w http.ResponseWriter, r *http.Request) {
	if s.opts.WebhookSecret == "" {
		s.metrics.webhooks.Inc(webhookDisabled)
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "webhooks are disabled (no webhook secret configured)"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if err != nil {
		s.metrics.webhooks.Inc(webhookRejected)
		s.fail(w, httpErr(http.StatusRequestEntityTooLarge, "invalid", "payload too large"))
		return
	}
	sig, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	got, derr := hex.DecodeString(sig)
	mac := hmac.New(sha256.New, []byte(s.opts.WebhookSecret))
	mac.Write(body)
	if !ok || derr != nil || !hmac.Equal(got, mac.Sum(nil)) {
		s.metrics.webhooks.Inc(webhookRejected)
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid webhook signature"))
		return
	}
	switch ev := r.Header.Get("X-GitHub-Event"); ev {
	case "ping":
		s.metrics.webhooks.Inc(webhookIgnored)
		writeJSON(w, http.StatusOK, map[string]any{"status": "pong"})
	case "push":
		var p struct {
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			s.metrics.webhooks.Inc(webhookRejected)
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "invalid push payload"))
			return
		}
		if p.Ref != "refs/heads/"+s.trackedBranch() {
			s.metrics.webhooks.Inc(webhookIgnored)
			writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": "not the tracked branch"})
			return
		}
		if s.rt != nil {
			s.rt.Trigger()
		}
		s.metrics.webhooks.Inc(webhookAccepted)
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
	default:
		s.metrics.webhooks.Inc(webhookIgnored)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": "event " + ev + " is not handled"})
	}
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (s *server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.rt == nil || !s.rt.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "no active revision\n")
		return
	}
	_, _ = io.WriteString(w, "ready\n")
}
