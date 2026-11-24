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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// SDKModule is the only module resolved at run time by the plugin host.
const SDKModule = "@cicord/sdk"

// GlobalName is the global variable the bundle assigns its exports to.
const GlobalName = "__cicord_plugin"

// Result is a bundled plugin.
type Result struct {
	// Code is the bundled script, with an inline source map as its last line.
	Code string
	// SourceMap is the source map (JSON) of Code, sources relative to the plugin directory.
	SourceMap []byte
	// Warnings are esbuild warnings, formatted as "file:line:col: text".
	Warnings []string
}

// Message is one build diagnostic.
type Message struct {
	File   string // relative to the plugin directory when possible
	Line   int    // 1-based, 0 if unknown
	Column int    // 1-based, 0 if unknown
	Text   string
	// LineText is the source line the message points at, if known.
	LineText string
}

func (m Message) String() string {
	switch {
	case m.File == "":
		return m.Text
	case m.Line == 0:
		return fmt.Sprintf("%s: %s", m.File, m.Text)
	default:
		return fmt.Sprintf("%s:%d:%d: %s", m.File, m.Line, m.Column, m.Text)
	}
}

// Error is returned by Bundle when esbuild reports errors.
type Error struct {
	Messages []Message
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Messages))
	for i, m := range e.Messages {
		parts[i] = m.String()
	}
	return "build failed:\n  " + strings.Join(parts, "\n  ")
}

// nodeBuiltins are refused with an explicit error (instead of esbuild's generic
// "could not resolve").
var nodeBuiltins = map[string]bool{
	"assert": true, "async_hooks": true, "buffer": true, "child_process": true, "cluster": true,
	"console": true, "constants": true, "crypto": true, "dgram": true, "diagnostics_channel": true,
	"dns": true, "domain": true, "events": true, "fs": true, "fs/promises": true, "http": true,
	"http2": true, "https": true, "inspector": true, "module": true, "net": true, "os": true,
	"path": true, "perf_hooks": true, "process": true, "punycode": true, "querystring": true,
	"readline": true, "repl": true, "stream": true, "string_decoder": true, "sys": true,
	"timers": true, "tls": true, "trace_events": true, "tty": true, "url": true, "util": true,
	"v8": true, "vm": true, "wasi": true, "worker_threads": true, "zlib": true,
}

var builtinFilter = regexp.MustCompile(`^(node:.*|[a-z_]+(/promises)?)$`)

func noNodeBuiltins() api.Plugin {
	return api.Plugin{
		Name: "cicord-no-node-builtins",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: builtinFilter.String()}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				name := strings.TrimPrefix(a.Path, "node:")
				if strings.HasPrefix(a.Path, "node:") || nodeBuiltins[name] {
					return api.OnResolveResult{}, fmt.Errorf("cannot import %q: Node.js built-in modules are not available in Cicord plugins (all I/O goes through ctx)", a.Path)
				}
				return api.OnResolveResult{}, nil
			})
		},
	}
}

// Bundle bundles the entry file (relative to pluginDir, e.g. "./src/index.ts") and every
// module it imports into a single script. The bundle is not minified.
func Bundle(pluginDir, entry string) (Result, error) {
	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return Result{}, err
	}
	// esbuild works on real paths: resolve symlinks so source map paths stay relative to the
	// plugin directory (e.g. "src/index.ts", not "../../private/var/…/src/index.ts").
	if real, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = real
	}
	entryPath := entry
	if !filepath.IsAbs(entryPath) {
		entryPath = filepath.Join(absDir, entry)
	}
	if _, err := os.Stat(entryPath); err != nil {
		return Result{}, &Error{Messages: []Message{{File: entry, Text: "entry point not found"}}}
	}

	opts := api.BuildOptions{
		EntryPoints:    []string{entryPath},
		AbsWorkingDir:  absDir,
		Outfile:        filepath.Join(absDir, "plugin.bundle.js"),
		Bundle:         true,
		Write:          false,
		Format:         api.FormatIIFE,
		GlobalName:     GlobalName,
		Platform:       api.PlatformNeutral,
		MainFields:     []string{"module", "main"},
		Conditions:     []string{"import", "default"},
		Target:         api.ES2023,
		Sourcemap:      api.SourceMapInlineAndExternal,
		SourcesContent: api.SourcesContentExclude,
		Charset:        api.CharsetUTF8,
		LegalComments:  api.LegalCommentsNone,
		External:       []string{SDKModule},
		Plugins:        []api.Plugin{noNodeBuiltins()},
		LogLevel:       api.LogLevelSilent,
	}
	res := api.Build(opts)
	if len(res.Errors) > 0 {
		return Result{}, &Error{Messages: convertMessages(absDir, res.Errors)}
	}

	var out Result
	for _, w := range convertMessages(absDir, res.Warnings) {
		out.Warnings = append(out.Warnings, w.String())
	}
	for _, f := range res.OutputFiles {
		switch {
		case strings.HasSuffix(f.Path, ".map"):
			out.SourceMap = f.Contents
		case strings.HasSuffix(f.Path, ".js"):
			out.Code = string(f.Contents)
		}
	}
	if out.Code == "" {
		return Result{}, errors.New("build: esbuild produced no output")
	}
	return out, nil
}

func convertMessages(dir string, msgs []api.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		msg := Message{Text: m.Text}
		if m.Location != nil {
			file := m.Location.File
			if rel, err := filepath.Rel(dir, file); err == nil && filepath.IsAbs(file) && !strings.HasPrefix(rel, "..") {
				file = rel
			}
			msg.File = filepath.ToSlash(file)
			msg.Line = m.Location.Line
			msg.Column = m.Location.Column + 1
			msg.LineText = m.Location.LineText
		}
		for _, n := range m.Notes {
			if n.Text != "" {
				msg.Text += " (" + n.Text + ")"
			}
		}
		out = append(out, msg)
	}
	return out
}
