package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Local snapshots a working directory (used by `cicord dev`). Revision IDs are content
// hashes, so unchanged trees map to the same revision.
type Local struct {
	dir      string
	snapRoot string
	changes  chan struct{}
	watcher  *fsnotify.Watcher
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
}

// ignored directories and files never copied into snapshots.
var ignoredNames = map[string]bool{".git": true, "node_modules": true, ".cicord": true, ".DS_Store": true}

// NewLocal watches dir and stores snapshots under snapRoot.
func NewLocal(dir, snapRoot string) (*Local, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, "cicord.yaml")); err != nil {
		return nil, fmt.Errorf("%s is not a Cicord repository (no cicord.yaml)", abs)
	}
	if err := os.MkdirAll(snapRoot, 0o755); err != nil {
		return nil, err
	}
	l := &Local{dir: abs, snapRoot: snapRoot, changes: make(chan struct{}, 1), done: make(chan struct{})}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	l.watcher = w
	if err := l.watchTree(); err != nil {
		w.Close()
		return nil, err
	}
	go l.loop()
	return l, nil
}

// Dir returns the watched working directory.
func (l *Local) Dir() string { return l.dir }

func (l *Local) watchTree() error {
	return filepath.WalkDir(l.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir // unreadable directory: not watched (WalkDir only reports errors on directories)
		}
		if d.IsDir() {
			if ignoredNames[d.Name()] && p != l.dir {
				return filepath.SkipDir
			}
			_ = l.watcher.Add(p)
		}
		return nil
	})
}

// watchDir adds dir and its non-ignored subdirectories to the watcher.
func (l *Local) watchDir(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir // unreadable or already removed directory: not watched
		}
		if d.IsDir() {
			if isIgnoredPath(l.dir, p) {
				return filepath.SkipDir
			}
			_ = l.watcher.Add(p)
		}
		return nil
	})
}

func (l *Local) loop() {
	var timer *time.Timer
	for {
		select {
		case <-l.done:
			return
		case ev, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if isIgnoredPath(l.dir, ev.Name) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					// Watch the new directory and every subdirectory created in the same
					// burst (mkdir -p, a build writing dist/assets…).
					l.watchDir(ev.Name)
				}
			}
			// Debounce bursts of editor writes.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(150*time.Millisecond, func() { notify(l.changes) })
		case _, ok := <-l.watcher.Errors:
			// Watching is best-effort (no logger here): a watcher error only means some
			// change may be noticed late, at the next event or Trigger.
			if !ok {
				return
			}
		}
	}
}

func isIgnoredPath(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if ignoredNames[part] || strings.HasSuffix(part, "~") || strings.HasPrefix(part, ".#") {
			return true
		}
	}
	return false
}

func (l *Local) Changes() <-chan struct{} { return l.changes }
func (l *Local) Trigger()                 { notify(l.changes) }

func (l *Local) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.watcher.Close()
}

// Fetch hashes the working tree and snapshots it if new.
func (l *Local) Fetch(ctx context.Context) (Revision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := listFiles(l.dir)
	if err != nil {
		return Revision{}, err
	}
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(l.dir, f))
		if err != nil {
			return Revision{}, err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(data))
		h.Write(data)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	rev := Revision{ID: "local-" + sum[:7], SHA: sum, Message: "working tree", Author: "local", Time: time.Now().UTC(), Source: "local"}
	snap := filepath.Join(l.snapRoot, sum[:16])
	rev.Dir = snap
	if _, err := os.Stat(filepath.Join(snap, "cicord.yaml")); err == nil {
		return rev, nil
	}
	tmp, err := os.MkdirTemp(l.snapRoot, ".tmp-")
	if err != nil {
		return Revision{}, err
	}
	for _, f := range files {
		if err := copyFile(filepath.Join(l.dir, f), filepath.Join(tmp, f)); err != nil {
			os.RemoveAll(tmp)
			return Revision{}, err
		}
	}
	if err := os.Rename(tmp, snap); err != nil {
		os.RemoveAll(tmp)
		if _, statErr := os.Stat(filepath.Join(snap, "cicord.yaml")); statErr == nil {
			return rev, nil // concurrent snapshot of the same content
		}
		return Revision{}, err
	}
	return rev, nil
}

func listFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ignoredNames[d.Name()] && p != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow symlinks out of the repository
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // G302: snapshot of a repository file (git-tracked content, never a secret), same rule as the G306 exclusion
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
