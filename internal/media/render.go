// Package media isolates card rendering and computes bounded image digests.
package media

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/cicord/cicord/internal/media/paint"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:generate go run ./gen
//go:embed renderer.wasm
var wasm []byte

//go:embed fonts/*.ttf
var fonts embed.FS
var prepared = sync.OnceValues(func() (*renderer, error) {
	ctx := experimental.WithCompilationWorkers(context.Background(), min(runtime.GOMAXPROCS(0), 16))
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(256).WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return &renderer{rt, mod}, nil
})

func Prepare() error { _, err := prepared(); return err }

type renderer struct {
	rt     wazero.Runtime
	module wazero.CompiledModule
}

var slots = make(chan struct{}, 4)

func Acquire(ctx context.Context) (func(), error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type limitBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}

func Render(ctx context.Context, raw []byte, inputs []string, load func(context.Context, string) ([]byte, error)) ([]byte, error) {
	release, err := Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	files := fstest.MapFS{}
	size := 0
	for _, key := range inputs {
		var b []byte
		if font, ok := strings.CutPrefix(key, "font:"); ok {
			paths := map[string]string{"Inter": "inter", "Noto": "notosans", "emoji": "notoemoji"}
			b, err = fonts.ReadFile("fonts/" + paths[font] + ".ttf")
		} else {
			b, err = load(ctx, key)
		}
		if err != nil {
			return nil, err
		}
		size += len(b)
		if size > 16<<20 {
			return nil, errors.New("render inputs exceed 16 MiB")
		}
		files[paint.Path(key)] = &fstest.MapFile{Data: b}
	}
	engine, err := prepared()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	var out = limitBuffer{max: 16 << 20}
	var problems = limitBuffer{max: 4096}
	cfg := wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(raw)).WithStdout(&out).WithStderr(&problems).
		WithFSConfig(wazero.NewFSConfig().WithFSMount(files, "/inputs")).WithEnv("GOMEMLIMIT", "10MiB")
	module, err := engine.rt.InstantiateModule(ctx, engine.module, cfg)
	if module != nil {
		defer module.Close(context.Background())
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("render exceeded time budget: %w", ctx.Err())
		}
		msg := strings.SplitN(problems.String(), "\n", 2)[0]
		if msg == "" {
			msg = "memory limit exceeded or invalid image"
		}
		return nil, fmt.Errorf("render failed: %.300s", msg)
	}
	return out.Bytes(), nil
}
