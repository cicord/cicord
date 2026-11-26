// Command worker runs esbuild against the package filesystem mounted by the host.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/cicord/cicord/internal/build/protocol"
	"github.com/evanw/esbuild/pkg/api"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const SDKModule = protocol.SDKModule
const GlobalName = protocol.GlobalName

type Message = protocol.Message
type Result = protocol.Result
type Error = protocol.Error

func main() {
	r, err := compile(os.Args[1])
	out := protocol.Reply{Result: r}
	if err != nil {
		out.Errors = err.Messages
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		os.Exit(1)
	}
}

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

func compile(entry string) (Result, *Error) {
	const absDir = "/plugin"
	entryPath := filepath.Join(absDir, entry)

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
		return Result{}, &Error{Messages: []Message{{Text: "esbuild produced no output"}}}
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
