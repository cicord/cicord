package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
)

func unpackAssets(code string) (string, map[string][]byte, error) {
	n := strings.LastIndex(code, build.AssetMarker)
	if n < 0 {
		return code, nil, nil
	}
	// Only the generated final line can contain the package. A marker in source code is inert.
	tail := strings.TrimSuffix(code[n+len(build.AssetMarker):], "\n")
	if strings.Contains(tail, "\n") {
		return code, nil, nil
	}
	if len(tail) > 8<<20 {
		return "", nil, errors.New("invalid asset package size")
	}
	var files map[string][]byte
	if err := json.Unmarshal([]byte(tail), &files); err != nil {
		return "", nil, fmt.Errorf("invalid asset package: %w", err)
	}
	if len(files) > build.MaxAssetEntries {
		return "", nil, errors.New("too many assets")
	}
	size := 0
	for p, b := range files {
		size += len(b)
		if !build.AssetPath(p) || size > build.MaxAssetBytes {
			return "", nil, errors.New("invalid asset path or package size")
		}
	}
	return code[:n], files, nil
}
func (i *Instance) readAsset(path string) ([]byte, error) {
	b, ok := i.assets[path]
	if !ok || !build.AssetPath(path) {
		return nil, argErr("unknown plugin asset %q", path)
	}
	return b, nil
}
func opAssetRead(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := decode[struct{ Path string }](args)
	if err != nil {
		return nil, err
	}
	return func(context.Context) (any, error) { return i.readAsset(a.Path) }, nil
}

// Chunks keep each plugin→host call below the generic JSON and synchronous turn limits.
// A reservation covers the file until the send settles, including time in the Discord rate limiter.
type uploadStore struct {
	sync.Mutex
	next     int64
	reserved int
	pending  map[int64]*stagedUpload
}
type stagedUpload struct {
	owner *invocation
	data  []byte
	size  int
	stop  func() bool
}
type fileInput struct {
	Name, Description, ContentType string
	Handle                         int64
}

func (i *Instance) uploadLimit() int {
	if i.env.UploadLimit != nil {
		return min(discord.UploadLimit(3), i.env.UploadLimit())
	}
	return discord.UploadLimit(0)
}
func (s *uploadStore) free(inv *invocation, id int64) {
	s.Lock()
	defer s.Unlock()
	if f := s.pending[id]; f != nil && f.owner == inv {
		delete(s.pending, id)
		s.reserved -= f.size
		f.stop()
	}
}
func opUploadFree(i *Instance, inv *invocation, args json.RawMessage) (any, error) {
	id, err := decode[int64](args)
	if err == nil {
		i.uploads.free(inv, id)
	}
	return nil, err
}
func opUploadPart(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := decode[struct {
		Handle int64
		Size   int
		Data   []byte
	}](args)
	if err != nil {
		return nil, err
	}
	if a.Size < 0 || a.Size > i.uploadLimit() || len(a.Data) > 128<<10 {
		return nil, argErr("upload exceeds file or chunk limit")
	}
	return func(ctx context.Context) (any, error) {
		s := &i.uploads
		s.Lock()
		defer s.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if a.Handle == 0 {
			if len(s.pending) >= 32 || s.reserved+a.Size > 100<<20 {
				return nil, argErr("too many pending file uploads")
			}
			if s.pending == nil {
				s.pending = make(map[int64]*stagedUpload)
			}
			if len(a.Data) > a.Size {
				return nil, argErr("invalid upload size")
			}
			s.next++
			id := s.next
			f := &stagedUpload{owner: inv, size: a.Size, data: make([]byte, 0, a.Size)}
			f.stop = context.AfterFunc(ctx, func() { s.free(inv, id) })
			s.pending[id] = f
			s.reserved += a.Size
			a.Handle = id
		}
		f := s.pending[a.Handle]
		if f == nil || f.owner != inv || f.size != a.Size || len(f.data)+len(a.Data) > f.size {
			return nil, argErr("invalid upload handle or size")
		}
		f.data = append(f.data, a.Data...)
		return a.Handle, nil
	}, nil
}
func (i *Instance) messageFiles(in []fileInput) ([]discord.UploadFile, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) > 10 {
		return nil, argErr("at most 10 files per message")
	}
	s := &i.uploads
	s.Lock()
	defer s.Unlock()
	files := make([]discord.UploadFile, 0, len(in))
	for _, a := range in {
		f := s.pending[a.Handle]
		if f == nil || f.owner != i.currentInv() || len(f.data) != f.size {
			return nil, argErr("incomplete file upload")
		}
		files = append(files, discord.UploadFile{Name: a.Name, Description: a.Description, ContentType: a.ContentType, Data: f.data})
	}
	return files, discord.ValidateUploads(files, i.uploadLimit())
}
