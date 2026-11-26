package build

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"testing/fstest"
)

const (
	maxSnapshotFiles    = 256
	maxSnapshotBytes    = 4 << 20
	maxBundleCacheBytes = 16 << 20
	maxCachedBundles    = 64
)

type packageSnapshot struct {
	files     fstest.MapFS
	hash      hash.Hash
	remaining int64
}

// Small ordinary packages can be cached across revisions. Compile precisely
// the bytes that were hashed: a mutable source tree must never populate a
// cache under the identity of a different package. Larger packages and those
// with links, special files or restricted metadata keep the live rooted mount.
func snapshotForBundle(root fs.FS, entry string) (fstest.MapFS, [32]byte) {
	s := packageSnapshot{files: fstest.MapFS{}, hash: sha256.New(), remaining: maxSnapshotBytes}
	s.record("entry", []byte(entry))
	if !s.capture(root, ".") {
		return nil, [32]byte{}
	}
	return s.files, [32]byte(s.hash.Sum(nil))
}
func (s *packageSnapshot) record(name string, data []byte) {
	var lengths [16]byte
	binary.LittleEndian.PutUint64(lengths[:8], uint64(len(name)))
	binary.LittleEndian.PutUint64(lengths[8:], uint64(len(data)))
	s.hash.Write(lengths[:])
	_, _ = io.WriteString(s.hash, name)
	s.hash.Write(data)
}
func (s *packageSnapshot) capture(root fs.FS, name string) bool {
	if len(s.files) >= maxSnapshotFiles {
		return false
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	if info.Mode().IsRegular() {
		if info.Size() > s.remaining {
			return false
		}
		data, err := io.ReadAll(io.LimitReader(f, s.remaining+1))
		if err != nil || int64(len(data)) > s.remaining {
			return false
		}
		s.remaining -= int64(len(data))
		s.files[name] = &fstest.MapFile{Data: data, Mode: 0444}
		s.record("file:"+name, data)
		return true
	}
	d, ok := f.(fs.ReadDirFile)
	if !info.IsDir() || !ok {
		return false
	}
	if name != "." {
		s.files[name] = &fstest.MapFile{Mode: fs.ModeDir | 0555}
		s.record("dir:"+name, nil)
	}
	limit := maxSnapshotFiles - len(s.files)
	entries, err := d.ReadDir(limit + 1)
	if (err != nil && !errors.Is(err, io.EOF)) || len(entries) > limit {
		return false
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		if !s.capture(root, path.Join(name, e.Name())) {
			return false
		}
	}
	return true
}

type cachedBundle struct {
	key    [32]byte
	result Result
	size   int
}
type bundleCache struct {
	sync.Mutex
	entries []cachedBundle
	bytes   int
}

var bundles bundleCache

func cloneBundle(r Result) Result {
	r.SourceMap = slices.Clone(r.SourceMap)
	r.Warnings = slices.Clone(r.Warnings)
	return r
}
func (c *bundleCache) get(key [32]byte) (Result, bool) {
	c.Lock()
	defer c.Unlock()
	for j := len(c.entries) - 1; j >= 0; j-- {
		if c.entries[j].key == key {
			return cloneBundle(c.entries[j].result), true
		}
	}
	return Result{}, false
}
func (c *bundleCache) put(key [32]byte, r Result) {
	size := len(r.Code) + len(r.SourceMap)
	for _, w := range r.Warnings {
		size += len(w)
	}
	if size > maxBundleCacheBytes {
		return
	}
	c.Lock()
	defer c.Unlock()
	for _, e := range c.entries {
		if e.key == key {
			return
		}
	}
	for c.bytes+size > maxBundleCacheBytes || len(c.entries) >= maxCachedBundles {
		c.bytes -= c.entries[0].size
		c.entries[0] = cachedBundle{}
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, cachedBundle{key, cloneBundle(r), size})
	c.bytes += size
}
