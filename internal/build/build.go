// Package build bundles Cicord plugins (TypeScript or JavaScript) into a single script that the
// plugin host (internal/plugin, QuickJS-ng) can run. It uses esbuild as a Go library: no Node.js
// is involved anywhere.
//
// Output contract (consumed by internal/plugin):
//
//   - format: IIFE assigned to the global variable GlobalName ("__cicord_plugin"); the plugin's
//     default export is GlobalName.default;
//   - "@cicord/sdk" is external: the bundle calls the global `require("@cicord/sdk")`, which the
//     plugin host provides. No other module can be required at run time;
//   - target: ES2023 syntax (QuickJS-ng supports all of it, async generators included);
//   - an inline source map is appended as the last line, so the plugin host maps stack
//     traces back to the original TypeScript files (paths relative to the plugin directory,
//     e.g. "src/index.ts:12:5"). The same map is returned separately in Result.SourceMap.
//
// Node.js built-in modules (fs, path, node:*…) cannot be imported; the build fails with a
// readable error pointing at the import.
package build

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing/fstest"
	"time"

	"github.com/cicord/cicord/internal/build/protocol"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const SDKModule = protocol.SDKModule
const GlobalName = protocol.GlobalName

type Message = protocol.Message
type Result = protocol.Result
type Error = protocol.Error

//go:generate go run ./gen
//go:embed compiler.wasm.gz
var compilerGzip []byte

type compiler struct {
	runtime wazero.Runtime
	module  wazero.CompiledModule
}

var buildSlots = make(chan struct{}, 2)

var prepareCompiler = sync.OnceValues(func() (*compiler, error) {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(4096).WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(compilerGzip))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	wasm, err := io.ReadAll(io.LimitReader(reader, 32<<20))
	if err != nil {
		return nil, err
	}
	module, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return &compiler{rt, module}, nil
})

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > 64<<20-b.Len() {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}

func Bundle(pluginDir, entry string) (Result, error) { return BundleAssets(pluginDir, entry, "") }

// BundleAssets mounts only the package for all esbuild reads, including package.json,
// tsconfig extends, source maps and glob resolution. The compiler has no host environment,
// working directory, network or writable filesystem. @cicord/sdk is supplied by the VM.
func BundleAssets(pluginDir, entry, assets string) (Result, error) {
	entry = filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry)))
	if !AssetPath(entry) || !filepath.IsLocal(filepath.FromSlash(entry)) {
		return Result{}, &Error{Messages: []Message{{File: entry, Text: "entry must be inside the plugin directory"}}}
	}
	root, err := os.OpenRoot(pluginDir)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	// Check before compiling so a missing entry stays a cheap, structured diagnostic.
	st, err := root.Stat(filepath.FromSlash(entry))
	if err != nil || !st.Mode().IsRegular() {
		return Result{}, &Error{Messages: []Message{{File: entry, Text: "entry point not found or outside the plugin directory"}}}
	}
	engine, err := prepareCompiler()
	if err != nil {
		return Result{}, fmt.Errorf("initialize plugin compiler: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case buildSlots <- struct{}{}:
		defer func() { <-buildSlots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	var output boundedOutput
	cfg := wazero.NewModuleConfig().WithName("").WithArgs("cicord-build", entry).WithStdout(&output).WithFSConfig(wazero.NewFSConfig().WithFSMount(fstest.MapFS{"plugin": &fstest.MapFile{Mode: fs.ModeDir | 0555}}, "/").WithFSMount(packageFS{root}, "/plugin"))
	module, err := engine.runtime.InstantiateModule(ctx, engine.module, cfg)
	if module != nil {
		defer module.Close(context.Background())
	}
	if err != nil {
		return Result{}, &Error{Messages: []Message{{File: entry, Text: "plugin compiler failed or exceeded its resource budget: " + err.Error()}}}
	}
	var reply protocol.Reply
	if err = json.Unmarshal(output.Bytes(), &reply); err != nil {
		return Result{}, fmt.Errorf("invalid plugin compiler response: %w", err)
	}
	if len(reply.Errors) > 0 {
		return Result{}, &Error{Messages: reply.Errors}
	}
	out := reply.Result
	if assets != "" {
		files, err := readAssets(root, assets)
		if err != nil {
			return Result{}, err
		}
		data, err := json.Marshal(files)
		if err != nil {
			return Result{}, err
		}
		out.Code += AssetMarker + string(data) + "\n"
	}
	return out, nil
}
