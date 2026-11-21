package config

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one validation finding, located in a file of the repository.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"` // relative to the repo root
	Line     int      `json:"line,omitempty"`
	Path     string   `json:"path,omitempty"` // JSON pointer inside the document
	Message  string   `json:"message"`
}

func (d Diagnostic) String() string {
	var b strings.Builder
	if d.File != "" {
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
		}
		b.WriteString(": ")
	}
	b.WriteString(string(d.Severity))
	b.WriteString(": ")
	if d.Path != "" {
		b.WriteString(d.Path)
		b.WriteString(": ")
	}
	b.WriteString(d.Message)
	return b.String()
}

type Diagnostics []Diagnostic

func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (ds Diagnostics) Errors() Diagnostics {
	var out Diagnostics
	for _, d := range ds {
		if d.Severity == SeverityError {
			out = append(out, d)
		}
	}
	return out
}

// Sort orders diagnostics by file, line, then message for stable output.
func (ds Diagnostics) Sort() {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].File != ds[j].File {
			return ds[i].File < ds[j].File
		}
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		return ds[i].Message < ds[j].Message
	})
}

// Err returns nil when there are no errors, otherwise an error listing them.
func (ds Diagnostics) Err() error {
	errs := ds.Errors()
	if len(errs) == 0 {
		return nil
	}
	lines := make([]string, len(errs))
	for i, d := range errs {
		lines[i] = d.String()
	}
	return fmt.Errorf("%d validation error(s):\n  %s", len(errs), strings.Join(lines, "\n  "))
}

type diagSink struct {
	ds Diagnostics
}

func (s *diagSink) errorf(file string, line int, path, format string, args ...any) {
	s.ds = append(s.ds, Diagnostic{SeverityError, file, line, path, fmt.Sprintf(format, args...)})
}

func (s *diagSink) warnf(file string, line int, path, format string, args ...any) {
	s.ds = append(s.ds, Diagnostic{SeverityWarning, file, line, path, fmt.Sprintf(format, args...)})
}
