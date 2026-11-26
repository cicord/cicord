package registry

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"
)

const (
	maxPackBytes    = 64 << 20
	maxInflatedPack = 128 << 20
	maxObjectSize   = 16 << 20
	maxPackObjects  = 32768
	maxPackageSize  = 32 << 20
	maxPackageFiles = 4096
	maxRefsBytes    = 4 << 20
)

var ErrRegistryLimit = errors.New("registry fetch exceeds resource limit")
var registryFetchSlots = make(chan struct{}, 2)

type registryFetchKey struct{}

func scopedFetch(ctx context.Context, url string) (context.Context, func(), error) {
	if HasUserinfo(url) {
		return nil, nil, errors.New("registry credentials must be provided through a secret reference")
	}
	if publicOnly(ctx) && !IsRemoteURL(url) {
		return nil, nil, fmt.Errorf("%w: public registry fetch requires HTTPS", ErrNotPublic)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, registryFetchKey{}, true), 45*time.Second)
	select {
	case registryFetchSlots <- struct{}{}:
		return ctx, func() { <-registryFetchSlots; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}

// A pack writer intercepts the compressed stream before go-git allocates its
// object tables. Scan bounded inflated representations (including delta targets)
// before handing the same bytes to go-git's normal integrity-checking parser.
type limitedStore struct {
	storage.Storer
	ctx context.Context
}

func (s *limitedStore) PackfileWriter() (io.WriteCloser, error) {
	return &limitedPack{ctx: s.ctx, store: s.Storer}, nil
}

type limitedPack struct {
	ctx    context.Context
	store  storage.Storer
	buf    bytes.Buffer
	failed error
}

func (w *limitedPack) Write(p []byte) (int, error) {
	if w.failed != nil {
		return 0, w.failed
	}
	if err := w.ctx.Err(); err != nil {
		w.failed = err
		return 0, err
	}
	if len(p) > maxPackBytes-w.buf.Len() {
		w.failed = ErrRegistryLimit
		return 0, w.failed
	}
	return w.buf.Write(p)
}
func (w *limitedPack) Close() error {
	if w.failed != nil {
		return w.failed
	}
	data := w.buf.Bytes()
	if err := checkPack(w.ctx, data, maxPackObjects, maxObjectSize, maxInflatedPack); err != nil {
		return err
	}
	return packfile.UpdateObjectStorage(w.store, bytes.NewReader(data))
}

type deltaSizes struct {
	head [20]byte
	n    int
}

func (d *deltaSizes) Write(p []byte) (int, error) { d.n += copy(d.head[d.n:], p); return len(p), nil }
func (d *deltaSizes) target(limit int64) (int64, error) {
	_, n := binary.Uvarint(d.head[:d.n])
	if n <= 0 {
		return 0, errors.New("invalid Git delta header")
	}
	size, n := binary.Uvarint(d.head[n:d.n])
	if n <= 0 {
		return 0, errors.New("invalid Git delta target")
	}
	if size > maxObjectSize {
		return 0, ErrRegistryLimit
	}
	if int64(size) > limit {
		return 0, ErrRegistryLimit
	}
	return int64(size), nil // bounded above by maxObjectSize
}
func checkPack(ctx context.Context, data []byte, maxObjects uint32, maxFile, maxExpanded int64) error {
	s := packfile.NewScanner(bytes.NewReader(data))
	defer s.Close()
	_, count, err := s.Header()
	if err != nil {
		return err
	}
	if count > maxObjects {
		return ErrRegistryLimit
	}
	var total int64
	for range count {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := s.NextObjectHeader()
		if err != nil {
			return err
		}
		if h.Length < 0 || h.Length > maxFile || h.Length > maxExpanded-total {
			return ErrRegistryLimit
		}
		total += h.Length
		var prefix deltaSizes
		out := io.Discard
		delta := h.Type == plumbing.OFSDeltaObject || h.Type == plumbing.REFDeltaObject
		if delta {
			out = &prefix
		}
		n, _, err := s.NextObject(out)
		if err != nil {
			return err
		}
		if n != h.Length {
			return errors.New("invalid Git object length")
		}
		if delta {
			size, err := prefix.target(maxFile)
			if err != nil {
				return err
			}
			if size > maxExpanded-total {
				return ErrRegistryLimit
			}
			total += size
		}
	}
	return nil
}

func unsupportedShallow(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "shallow not supported") || strings.Contains(err.Error(), "does not support shallow"))
}

func cloneRef(ctx context.Context, url string, ref plumbing.ReferenceName) (*git.Repository, error) {
	ctx, done, err := scopedFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	defer done()
	opts := &git.CloneOptions{URL: url, Auth: AuthFor(ctx, url), NoCheckout: true, SingleBranch: true, ReferenceName: ref, Depth: 1, Tags: git.NoTags}
	clone := func() (*git.Repository, error) {
		return git.CloneContext(ctx, &limitedStore{memory.NewStorage(), ctx}, nil, opts)
	}
	r, err := clone()
	if err != nil && unsupportedShallow(err) {
		opts.Depth = 0
		r, err = clone()
	} // older transports still pass through the same pack limits
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", Redact(url), err)
	}
	return r, nil
}

func listRefs(ctx context.Context, url string) ([]*plumbing.Reference, error) {
	ctx, done, err := scopedFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	defer done()
	remote := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: AuthFor(ctx, url)})
	if err != nil {
		return nil, err
	}
	if len(refs) > maxPackObjects {
		return nil, ErrRegistryLimit
	}
	return refs, nil
}

// Fetch by commit first so moving or dropping a release tag does not change a lock.
// Servers that reject exact wants can serve the release tag. The caller always
// resolves the original commit object and compares its tree against the lock.
func pinnedRepo(ctx context.Context, url string, hash plumbing.Hash, tag plumbing.ReferenceName) (*git.Repository, error) {
	remoteFetch := func() (*git.Repository, error) {
		c, done, err := scopedFetch(ctx, url)
		if err != nil {
			return nil, err
		}
		defer done()
		r, err := git.Init(&limitedStore{memory.NewStorage(), c}, nil)
		if err != nil {
			return nil, err
		}
		rem, err := r.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}})
		if err != nil {
			return nil, err
		}
		spec := gitconfig.RefSpec(hash.String() + ":refs/heads/cicord-pin")
		err = rem.FetchContext(c, &git.FetchOptions{Auth: AuthFor(c, url), RefSpecs: []gitconfig.RefSpec{spec}, Depth: 1, Tags: git.NoTags})
		return r, err
	}
	r, err := remoteFetch()
	if err == nil {
		return r, nil
	}
	if errors.Is(err, git.ErrExactSHA1NotSupported) || unsupportedShallow(err) {
		tagged, tagErr := cloneRef(ctx, url, tag)
		if tagErr == nil {
			if _, err := tagged.CommitObject(hash); err == nil {
				return tagged, nil
			}
		} else if !errors.Is(tagErr, plumbing.ErrReferenceNotFound) && !strings.Contains(tagErr.Error(), "couldn't find remote ref") && !strings.Contains(tagErr.Error(), "not found") {
			return nil, tagErr
		}
		// Legacy servers can refuse exact wants and the release tag may have moved.
		// Preserve immutable-lock recovery from reachable history within the same limits.
		c, done, err := scopedFetch(ctx, url)
		if err != nil {
			return nil, err
		}
		defer done()
		return git.CloneContext(c, &limitedStore{memory.NewStorage(), c}, nil, &git.CloneOptions{URL: url, Auth: AuthFor(c, url), NoCheckout: true, Tags: git.AllTags})
	}
	return nil, err
}
