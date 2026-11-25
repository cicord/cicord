package alert

import (
	"fmt"
	"strings"
	"time"
)

// Alert states.
const (
	StateFiring   = "firing"
	StateResolved = "resolved"
	StateTest     = "test"
)

// Severities.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// Message is one alert notification. It names where the problem is and links to the
// admin console; it never carries a secret, a plugin's error text or Discord content:
// Detail is written by Cicord (a sync error, a health message), never by a plugin.
type Message struct {
	// Key identifies the condition ("plugin/<server>/<bot>/<instance>"…): a firing and
	// its resolved message share it.
	Key      string `json:"key"`
	State    string `json:"state"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	// Runtime is the host of the runtime's public URL.
	Runtime    string `json:"runtime"`
	Server     string `json:"server,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	Bot        string `json:"bot,omitempty"`
	Instance   string `json:"instance,omitempty"`
	Plugin     string `json:"plugin,omitempty"`
	// Workflow is what was running: a plugin handler ("modal open", "command /ticket"),
	// "sync", "revision activation", "pause"…
	Workflow string `json:"workflow,omitempty"`
	Revision string `json:"revision,omitempty"`
	// Link is the admin console page of the problem.
	Link  string    `json:"link,omitempty"`
	Since time.Time `json:"since"`
	Time  time.Time `json:"time"`
}

// Heading is the title with its state.
func (m Message) Heading() string {
	switch m.State {
	case StateResolved:
		return "[RESOLVED] " + m.Title
	case StateTest:
		return "[TEST] " + m.Title
	}
	return "[" + strings.ToUpper(m.Severity) + "] " + m.Title
}

// Field is a labeled value of a message.
type Field struct {
	Name, Value string
	Inline      bool
}

// Fields are the labeled values of a message, in display order.
func (m Message) Fields() []Field {
	var out []Field
	add := func(name, value string) {
		if value != "" {
			out = append(out, Field{Name: name, Value: value, Inline: true})
		}
	}
	add("Runtime", m.Runtime)
	server := m.Server
	if m.ServerName != "" && m.ServerName != m.Server {
		server = m.ServerName + " (" + m.Server + ")"
	}
	add("Server", server)
	add("Bot", m.Bot)
	inst := m.Instance
	if m.Plugin != "" && m.Plugin != m.Instance {
		inst += " (" + m.Plugin + ")"
	}
	add("Plugin instance", inst)
	add("Workflow", m.Workflow)
	add("Revision", m.Revision)
	if m.State == StateResolved && !m.Since.IsZero() {
		add("Duration", m.Time.Sub(m.Since).Round(time.Second).String())
	}
	return out
}

// Summary is the message on one line.
func (m Message) Summary() string {
	var b strings.Builder
	b.WriteString(m.Heading())
	for _, f := range m.Fields() {
		fmt.Fprintf(&b, " · %s: %s", f.Name, f.Value)
	}
	if m.Detail != "" {
		b.WriteString(" — " + m.Detail)
	}
	if m.Link != "" {
		b.WriteString(" " + m.Link)
	}
	return b.String()
}

func (m Message) color() int {
	switch {
	case m.State == StateResolved:
		return colorResolved
	case m.State == StateTest || m.Severity == SeverityInfo:
		return colorInfo
	case m.Severity == SeverityWarning:
		return colorWarning
	}
	return colorCritical
}
