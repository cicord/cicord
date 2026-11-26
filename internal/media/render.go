// Package media isolates card rendering and image hashing in a confined WASI worker.
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

// Budgets of one worker run. wazero applies the memory ceiling per runtime, so the
// renderer and the hasher are two runtimes over the same compiled module.
//
// The spec (SDK-11) asks for 2048-pixel sides, 16 MiB and 300 ms of CPU per render,
// which cannot hold together: a 2048×2048 RGBA canvas alone is 16 MiB. The
// renderer keeps sides up to 2048 but caps the canvas and source areas
// (paint.MaxCanvasPixels, paint.MaxSourcePixels). The Go worker's own data and
// the three built-in fonts (4.8 MiB) come on top, hence 24 MiB.
//
// wazero cannot meter CPU, so the deadline is wall clock from the moment the
// render holds a slot (at most one per core). Measured on an Apple M-series core:
// a 700×220 welcome card with a background and an avatar takes about 115 ms; the
// largest envelope (a 0.5 Mpx canvas and source, 60 text layers in three fonts)
// about 600 ms.
const (
	renderPages = 384
	hashPages   = 1536
)

var (
	renderDeadline = time.Second
	// A 4-megapixel progressive JPEG keeps 48 MiB of coefficients next to its
	// 12 MiB of pixels; 96 MiB holds that worst case with the Go runtime. It
	// decodes in about 1.2 s in the worker (native Go: 50 ms), so the deadline
	// leaves headroom for slower hosts. A scan bomb that runs for minutes
	// natively stops here.
	hashDeadline = 3 * time.Second
)

var compiled = wazero.NewCompilationCache()

func engine(pages uint32) func() (*worker, error) {
	return sync.OnceValues(func() (*worker, error) {
		ctx := experimental.WithCompilationWorkers(context.Background(), min(runtime.GOMAXPROCS(0), 16))
		cfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(pages).WithCloseOnContextDone(true).WithCompilationCache(compiled)
		rt := wazero.NewRuntimeWithConfig(ctx, cfg)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
			_ = rt.Close(ctx)
			return nil, err
		}
		mod, err := rt.CompileModule(ctx, wasm)
		if err != nil {
			_ = rt.Close(ctx)
			return nil, err
		}
		return &worker{rt, mod}, nil
	})
}

var (
	renderer = engine(renderPages)
	hasher   = engine(hashPages)
)

// Prepare compiles the worker ahead of the first render or hash.
func Prepare() error { _, err := renderer(); return err }

type worker struct {
	rt     wazero.Runtime
	module wazero.CompiledModule
}

// slots bounds the workers running at once in the process. Each one is CPU-bound,
// so more workers than processors would only stretch every wall-clock deadline.
// Two at least, so that hashing (hashSlots) never takes the only one.
var slots = make(chan struct{}, max(2, min(4, runtime.GOMAXPROCS(0))))

// hashSlots keeps at least half of the slots for rendering: any member can post an
// image that a moderation plugin hashes, and each costs up to hashDeadline.
var hashSlots = make(chan struct{}, max(1, cap(slots)/2))

// Acquire takes a process-wide worker slot. Callers hold it only around the
// worker itself: downloads and other waits happen before.
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

// run executes the worker once with its deadline, which starts once the caller has
// a slot. On expiry wazero closes the module, so a decoder stuck in a crafted
// image stops there instead of running on in the background.
func (w *worker) run(ctx context.Context, deadline time.Duration, stdin []byte, files fstest.MapFS, outMax int, env ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	out := limitBuffer{max: outMax}
	problems := limitBuffer{max: 4096}
	cfg := wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(stdin)).WithStdout(&out).WithStderr(&problems).
		WithFSConfig(wazero.NewFSConfig().WithFSMount(files, "/inputs"))
	for i := 0; i+1 < len(env); i += 2 {
		cfg = cfg.WithEnv(env[i], env[i+1])
	}
	module, err := w.rt.InstantiateModule(ctx, w.module, cfg)
	if module != nil {
		defer module.Close(context.Background())
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("exceeded time budget: %w", ctx.Err())
		}
		msg := strings.SplitN(problems.String(), "\n", 2)[0]
		if msg == "" {
			msg = "memory limit exceeded or invalid image"
		}
		return nil, fmt.Errorf("failed: %.300s", msg)
	}
	return out.Bytes(), nil
}

// Render resolves every input first, then takes a worker slot for the render
// alone. load must bound its own wait: Render does not hold a slot meanwhile.
func Render(ctx context.Context, raw []byte, inputs []string, load func(context.Context, string) ([]byte, error)) ([]byte, error) {
	files := fstest.MapFS{}
	size := 0
	for _, key := range inputs {
		var b []byte
		var err error
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
	w, err := renderer()
	if err != nil {
		return nil, err
	}
	release, err := Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	out, err := w.run(ctx, renderDeadline, raw, files, 16<<20, "GOMEMLIMIT", "16MiB")
	if err != nil {
		return nil, fmt.Errorf("render %w", err)
	}
	return out, nil
}
