// Package planfmt renders server plans (reconcile.Plan, or the JSON of
// POST /api/v1/plan) for humans: a Terraform-like text form for terminals and a
// Markdown form for pull-request comments.
//
// Plans are handled in their decoded JSON form (values are string, float64, bool,
// nil, []any or map[string]any), so local plans (FromReconcile) and remote plans
// (decoded API responses) render identically.
//
// Symbols:
//
//   - create   ~ update   - delete   > reorder   = adopt   # unmanage (left in Discord)
//     ! blocked (with the reason)   ^ waits for a higher boost level (applied once
//     reached, not drift)   note: information that needs no action
package planfmt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/reconcile"
)

// Field is the from → to of one field.
type Field struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Change is one planned operation (reconcile.Change as JSON).
type Change struct {
	Action  string           `json:"action"`
	Kind    string           `json:"kind"`
	Key     string           `json:"key"`
	ID      string           `json:"id,omitempty"`
	Summary string           `json:"summary"`
	Fields  map[string]Field `json:"fields,omitempty"`
}

// Blocked is an operation the reconciler refuses to perform. One whose reason starts with
// "boost: " only waits for a higher boost level (WaitsForBoost).
type Blocked struct {
	Action  string           `json:"action"`
	Kind    string           `json:"kind"`
	Key     string           `json:"key"`
	ID      string           `json:"id,omitempty"`
	Reason  string           `json:"reason"`
	Summary string           `json:"summary,omitempty"`
	Fields  map[string]Field `json:"fields,omitempty"`
}

// WaitsForBoost reports a blocked operation that only waits for a higher server boost
// level: declared on purpose and not drift, so it is listed and counted apart from the other blocked operations (like the admin console's "Waiting for
// boosts").
func (b Blocked) WaitsForBoost() bool {
	return reconcile.WaitsForBoost(reconcile.Blocked{Reason: b.Reason})
}

// Note is information about a plan that needs no action (reconcile.Note).
type Note struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

// Plan is the plan of one server.
type Plan struct {
	Server  string    `json:"server"`
	Changes []Change  `json:"changes"`
	Blocked []Blocked `json:"blocked"`
	Notes   []Note    `json:"notes,omitempty"`
}

// Empty reports whether the plan has no change to apply (blocked operations are not
// changes), like reconcile.Plan.Empty.
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Result is the response of POST /api/v1/plan.
type Result struct {
	Valid       bool                `json:"valid"`
	Diagnostics []config.Diagnostic `json:"diagnostics"`
	Plans       []Plan              `json:"plans"`
}

// FromReconcile converts reconcile plans to their JSON form.
func FromReconcile(ps ...reconcile.Plan) ([]Plan, error) {
	data, err := json.Marshal(ps)
	if err != nil {
		return nil, err
	}
	var out []Plan
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Changes == nil {
			out[i].Changes = []Change{}
		}
		if out[i].Blocked == nil {
			out[i].Blocked = []Blocked{}
		}
	}
	return out, nil
}

// Pending reports whether any plan has changes to apply.
func Pending(ps []Plan) bool {
	for _, p := range ps {
		if !p.Empty() {
			return true
		}
	}
	return false
}

// Counts are the number of changes per action. Blocked excludes the operations that only
// wait for a higher boost level, counted in Boost.
type Counts struct {
	Create, Update, Delete, Reorder, Adopt, Unmanage, Blocked, Boost int
}

// Total is the number of changes (blocked operations and boost waits excluded).
func (c Counts) Total() int { return c.Create + c.Update + c.Delete + c.Reorder + c.Adopt + c.Unmanage }

// Count tallies the changes of plans.
func Count(ps ...Plan) Counts {
	var c Counts
	for _, p := range ps {
		for _, ch := range p.Changes {
			switch ch.Action {
			case reconcile.ActionCreate:
				c.Create++
			case reconcile.ActionDelete:
				c.Delete++
			case reconcile.ActionReorder:
				c.Reorder++
			case reconcile.ActionAdopt:
				c.Adopt++
			case reconcile.ActionUnmanage:
				c.Unmanage++
			default:
				c.Update++
			}
		}
		for _, b := range p.Blocked {
			if b.WaitsForBoost() {
				c.Boost++
			} else {
				c.Blocked++
			}
		}
	}
	return c
}

// SummaryLine is "Plan: 1 to create, 2 to update, 0 to delete, 1 to reorder. 1 operation(s)
// blocked. 1 change(s) wait for a higher boost level." when there are changes. Without
// changes it says "Discord matches the configuration" only when nothing is blocked
// (boost waits are declared on purpose and not drift, but are still mentioned):
//
//	No changes. Discord matches the configuration.
//	No changes. Discord matches the configuration; 1 change(s) wait for a higher boost level.
//	No changes can be applied: 1 operation(s) blocked.
func SummaryLine(ps ...Plan) string {
	c := Count(ps...)
	blocked := ""
	if c.Blocked > 0 {
		blocked = fmt.Sprintf(" %d operation(s) blocked.", c.Blocked)
	}
	boost := ""
	if c.Boost > 0 {
		boost = fmt.Sprintf(" %d change(s) wait for a higher boost level.", c.Boost)
	}
	if c.Total() == 0 {
		switch {
		case c.Blocked > 0:
			return "No changes can be applied:" + blocked + boost
		case c.Boost > 0:
			return "No changes. Discord matches the configuration;" + boost
		}
		return "No changes. Discord matches the configuration."
	}
	blocked += boost
	parts := []string{
		fmt.Sprintf("%d to create", c.Create),
		fmt.Sprintf("%d to update", c.Update),
		fmt.Sprintf("%d to delete", c.Delete),
	}
	for _, x := range []struct {
		n    int
		what string
	}{{c.Reorder, "to reorder"}, {c.Adopt, "to adopt"}, {c.Unmanage, "to stop managing"}} {
		if x.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", x.n, x.what))
		}
	}
	return "Plan: " + strings.Join(parts, ", ") + "." + blocked
}

// Symbol returns the one-character symbol of an action.
func Symbol(action string) string {
	switch action {
	case reconcile.ActionCreate:
		return "+"
	case reconcile.ActionUpdate:
		return "~"
	case reconcile.ActionDelete:
		return "-"
	case reconcile.ActionReorder:
		return ">"
	case reconcile.ActionAdopt:
		return "="
	case reconcile.ActionUnmanage:
		return "#"
	}
	return "~"
}

// Lines renders one plan as lines (without the server header): one line per change
// prefixed by its symbol, followed by indented field details, then blocked operations,
// boost waits and notes.
func Lines(p Plan) []string {
	var out []string
	for _, c := range p.Changes {
		summary := c.Summary
		if summary == "" {
			summary = fmt.Sprintf("%s %s %s", c.Action, c.Kind, c.Key)
		}
		out = append(out, Symbol(c.Action)+" "+summary)
		if c.Kind != reconcile.KindMemberRole { // the summary says it all
			out = append(out, fieldLines(c.Action, c.Fields, "    ")...)
		}
	}
	blocked := func(b Blocked, line string) {
		out = append(out, line)
		if b.Kind != reconcile.KindMemberRole {
			out = append(out, fieldLines(reconcile.ActionUpdate, b.Fields, "    ")...)
		}
	}
	for _, b := range p.Blocked {
		if !b.WaitsForBoost() {
			blocked(b, fmt.Sprintf("! blocked: %s (%s)", blockedWhat(b), b.Reason))
		}
	}
	for _, b := range p.Blocked {
		if b.WaitsForBoost() {
			blocked(b, fmt.Sprintf("^ waits for boost: %s (%s)", blockedWhat(b), strings.TrimPrefix(b.Reason, "boost: ")))
		}
	}
	for _, n := range p.Notes {
		out = append(out, "note: "+n.Message)
	}
	return out
}

func blockedWhat(b Blocked) string {
	if b.Summary != "" {
		return b.Summary
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s", b.Action, b.Kind, b.Key))
}

// serverLabel is the state of one server: "no changes", "2 change(s), 1 blocked, 1
// waiting for boosts"… The terminal form (markdown false) says "0 change(s)" next to
// blocked operations and counts notes only when there is nothing else; the Markdown form
// always counts them.
func serverLabel(p Plan, markdown bool) string {
	c := Count(p)
	label := "no changes"
	if c.Total() > 0 || (!markdown && c.Blocked > 0) {
		label = fmt.Sprintf("%d change(s)", c.Total())
	}
	if c.Blocked > 0 {
		label += fmt.Sprintf(", %d blocked", c.Blocked)
	}
	if c.Boost > 0 {
		label += fmt.Sprintf(", %d waiting for boosts", c.Boost)
	}
	if len(p.Notes) > 0 && (markdown || c.Total()+c.Blocked == 0) {
		label += fmt.Sprintf(", %d note(s)", len(p.Notes))
	}
	return label
}

// Text writes plans in the terminal format: a header per server, its changes, and a
// summary line.
func Text(w io.Writer, ps []Plan) {
	for _, p := range ps {
		fmt.Fprintf(w, "Server %s: %s\n", p.Server, serverLabel(p, false))
		for _, l := range Lines(p) {
			fmt.Fprintln(w, "  "+l)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, SummaryLine(ps...))
}

// MarkdownOptions configures Markdown.
type MarkdownOptions struct {
	// Marker is a hidden HTML comment identifying the comment (to update it later).
	Marker string
	// Title is the heading (default "Cicord plan").
	Title string
	// Footer is an optional last paragraph (e.g. the commit planned).
	Footer string
	// MaxBytes truncates the body (default 60000, below GitHub's 65536 limit).
	MaxBytes int
}

// Markdown renders a plan result as a pull-request comment.
func Markdown(r Result, o MarkdownOptions) string {
	if o.Title == "" {
		o.Title = "Cicord plan"
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 60000
	}
	var b strings.Builder
	if o.Marker != "" {
		b.WriteString(o.Marker + "\n")
	}
	fmt.Fprintf(&b, "### %s\n\n", o.Title)
	var errs, warns []config.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == config.SeverityError {
			errs = append(errs, d)
		} else {
			warns = append(warns, d)
		}
	}
	if !r.Valid {
		b.WriteString("**Validation failed**: the configuration was rejected, nothing can be planned.\n\n")
	} else {
		fmt.Fprintf(&b, "**%s**\n\n", SummaryLine(r.Plans...))
	}
	writeDiags := func(title string, ds []config.Diagnostic) {
		if len(ds) == 0 {
			return
		}
		fmt.Fprintf(&b, "<details%s><summary>%s (%d)</summary>\n\n```\n", openIf(title == "Errors"), title, len(ds))
		for _, d := range ds {
			b.WriteString(sanitizeFence(d.String()) + "\n")
		}
		b.WriteString("```\n</details>\n\n")
	}
	writeDiags("Errors", errs)
	writeDiags("Warnings", warns)
	for _, p := range r.Plans {
		c := Count(p)
		label := serverLabel(p, true)
		fmt.Fprintf(&b, "<details%s><summary>Server <code>%s</code>: %s</summary>\n\n", openIf(c.Total()+c.Blocked > 0), htmlEscape(p.Server), label)
		lines := Lines(p)
		if len(lines) > 0 {
			b.WriteString("```diff\n")
			for _, l := range lines {
				b.WriteString(sanitizeFence(l) + "\n")
			}
			b.WriteString("```\n")
		}
		b.WriteString("</details>\n\n")
	}
	if o.Footer != "" {
		b.WriteString(o.Footer + "\n")
	}
	out := b.String()
	if len(out) > o.MaxBytes {
		cut := o.MaxBytes - 200
		for cut > 0 && !utf8Start(out[cut]) {
			cut--
		}
		out = out[:cut] + "\n```\n\n*(truncated: the plan is too long for a comment, run `cicord plan --remote` to see all of it)*\n"
	}
	return out
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func openIf(cond bool) string {
	if cond {
		return " open"
	}
	return ""
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// sanitizeFence keeps a line from closing a code fence early.
func sanitizeFence(s string) string { return strings.ReplaceAll(s, "```", "`\u200b``") }

// ───────────────────────────────────────────────────────────── fields

func fieldLines(action string, fields map[string]Field, indent string) []string {
	if len(fields) == 0 {
		return nil
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	width := 0
	for _, n := range names {
		width = max(width, len(n))
	}
	var out []string
	for _, n := range names {
		f := fields[n]
		pad := strings.Repeat(" ", width-len(n))
		if action == reconcile.ActionCreate && f.From == nil {
			if isZero(f.To) {
				continue
			}
			if n == "overwrites" {
				out = append(out, indent+n+":")
				out = append(out, overwriteLines(nil, f.To, indent+"  ")...)
				continue
			}
			out = append(out, fmt.Sprintf("%s%s:%s %s", indent, n, pad, Value(f.To)))
			continue
		}
		switch {
		case n == "overwrites":
			out = append(out, indent+n+":")
			out = append(out, overwriteLines(f.From, f.To, indent+"  ")...)
		case n == "order" || n == "voiceOrder":
			out = append(out, fmt.Sprintf("%s%s:%s %s → %s", indent, n, pad, joinList(f.From), joinList(f.To)))
		case isStringList(f.From) && isStringList(f.To):
			add, del := setDiff(asStrings(f.From), asStrings(f.To))
			var parts []string
			for _, a := range add {
				parts = append(parts, "+"+a)
			}
			for _, d := range del {
				parts = append(parts, "-"+d)
			}
			if len(parts) == 0 {
				parts = []string{"(reordered)"}
			}
			out = append(out, fmt.Sprintf("%s%s:%s %s", indent, n, pad, strings.Join(parts, " ")))
		default:
			out = append(out, fmt.Sprintf("%s%s:%s %s → %s", indent, n, pad, Value(f.From), Value(f.To)))
		}
	}
	return out
}

// overwriteLines diffs two overwrite maps ({target: {allow: [], deny: []}}).
func overwriteLines(from, to any, indent string) []string {
	fm, _ := from.(map[string]any)
	tm, _ := to.(map[string]any)
	targets := map[string]bool{}
	for k := range fm {
		targets[k] = true
	}
	for k := range tm {
		targets[k] = true
	}
	names := make([]string, 0, len(targets))
	for k := range targets {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, t := range names {
		fv, inFrom := fm[t]
		tv, inTo := tm[t]
		fa, fd := owLists(fv)
		ta, td := owLists(tv)
		switch {
		case !inFrom:
			out = append(out, fmt.Sprintf("%s+ %s: %s", indent, t, owString(ta, td)))
		case !inTo:
			out = append(out, fmt.Sprintf("%s- %s: %s", indent, t, owString(fa, fd)))
		default:
			var parts []string
			if add, del := setDiff(fa, ta); len(add)+len(del) > 0 {
				parts = append(parts, "allow "+signed(add, del))
			}
			if add, del := setDiff(fd, td); len(add)+len(del) > 0 {
				parts = append(parts, "deny "+signed(add, del))
			}
			if len(parts) > 0 {
				out = append(out, fmt.Sprintf("%s~ %s: %s", indent, t, strings.Join(parts, "; ")))
			}
		}
	}
	return out
}

func owLists(v any) (allow, deny []string) {
	m, _ := v.(map[string]any)
	return asStrings(m["allow"]), asStrings(m["deny"])
}

func owString(allow, deny []string) string {
	var parts []string
	if len(allow) > 0 {
		parts = append(parts, "allow "+strings.Join(allow, ", "))
	}
	if len(deny) > 0 {
		parts = append(parts, "deny "+strings.Join(deny, ", "))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "; ")
}

func signed(add, del []string) string {
	var parts []string
	for _, a := range add {
		parts = append(parts, "+"+a)
	}
	for _, d := range del {
		parts = append(parts, "-"+d)
	}
	return strings.Join(parts, " ")
}

// setDiff returns the elements added in to and removed from from (sorted).
func setDiff(from, to []string) (added, removed []string) {
	fs := map[string]bool{}
	for _, s := range from {
		fs[s] = true
	}
	ts := map[string]bool{}
	for _, s := range to {
		ts[s] = true
		if !fs[s] {
			added = append(added, s)
		}
	}
	for _, s := range from {
		if !ts[s] {
			removed = append(removed, s)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func isStringList(v any) bool {
	switch tv := v.(type) {
	case nil:
		return true
	case []string:
		return true
	case []any:
		for _, e := range tv {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

func asStrings(v any) []string {
	switch tv := v.(type) {
	case []string:
		return tv
	case []any:
		out := make([]string, 0, len(tv))
		for _, e := range tv {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func joinList(v any) string {
	s := asStrings(v)
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

func isZero(v any) bool {
	switch tv := v.(type) {
	case nil:
		return true
	case string:
		return tv == ""
	case bool:
		return !tv
	case float64:
		return tv == 0
	case int:
		return tv == 0
	case []any:
		return len(tv) == 0
	case []string:
		return len(tv) == 0
	case map[string]any:
		return len(tv) == 0
	}
	return false
}

// Value formats a decoded JSON value for display.
func Value(v any) string {
	switch tv := v.(type) {
	case nil:
		return "(none)"
	case string:
		return strconv.Quote(tv)
	case bool:
		return strconv.FormatBool(tv)
	case float64:
		if tv == math.Trunc(tv) && math.Abs(tv) < 1e15 {
			return strconv.FormatInt(int64(tv), 10)
		}
		return strconv.FormatFloat(tv, 'g', -1, 64)
	case int:
		return strconv.Itoa(tv)
	case []any, []string:
		s := asStrings(tv)
		if len(s) == 0 && !isZero(tv) {
			break
		}
		return "[" + strings.Join(s, ", ") + "]"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(buf.String())
}
