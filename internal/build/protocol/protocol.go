// Package protocol carries compiler results across the WASI boundary.
package protocol

import (
	"fmt"
	"strings"
)

const SDKModule = "@cicord/sdk"
const GlobalName = "__cicord_plugin"

type Reply struct {
	Result Result
	Errors []Message
}

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
