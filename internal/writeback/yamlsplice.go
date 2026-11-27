package writeback

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// PatchYAML is SetYAML with a minimal textual change. When the value at path is a
// string or a list of strings and the node it replaces is a scalar or a sequence of
// scalars (flow or block style), only that node's text is rewritten (or its key line
// inserted or removed): blank lines, comment alignment and the style of every other node
// stay byte for byte, and a flow sequence stays a flow sequence (wrapped like the
// original when it spans several lines).
//
// The spliced document is re-parsed: it must decode to the same data as SetYAML's output
// and keep every comment line of src (a splice never touches a comment). Otherwise, and
// for any other shape, PatchYAML returns SetYAML's output (the whole document
// re-encoded).
func PatchYAML(src []byte, path []PathElem, value any) ([]byte, error) {
	want, err := SetYAML(src, path, value)
	if err != nil {
		return nil, err
	}
	if out, ok := splice(src, path, value); ok && sameData(out, want) && sameComments(out, src) {
		return out, nil
	}
	return want, nil
}

func sameData(a, b []byte) bool {
	var va, vb any
	return yaml.Unmarshal(a, &va) == nil && yaml.Unmarshal(b, &vb) == nil && reflect.DeepEqual(va, vb)
}

// sameComments reports whether two documents carry the same comment lines, wherever
// yaml.v3 attaches them.
func sameComments(a, b []byte) bool {
	var na, nb yaml.Node
	if yaml.Unmarshal(a, &na) != nil || yaml.Unmarshal(b, &nb) != nil {
		return false
	}
	ca, cb := commentLines(&na, nil), commentLines(&nb, nil)
	slices.Sort(ca)
	slices.Sort(cb)
	return slices.Equal(ca, cb)
}

func commentLines(n *yaml.Node, out []string) []string {
	if n == nil {
		return out
	}
	for _, c := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, l := range strings.Split(c, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
	}
	for _, c := range n.Content {
		out = commentLines(c, out)
	}
	return out
}

// splice edits src textually (see PatchYAML); ok is false when the shape is not handled.
func splice(src []byte, path []PathElem, value any) ([]byte, bool) {
	if len(path) == 0 || path[len(path)-1].Key == "" || bytes.IndexByte(src, '\r') >= 0 {
		return nil, false
	}
	var strs []string
	str, isStr := value.(string)
	switch v := value.(type) {
	case nil, string:
	case []string:
		strs = v
	default:
		return nil, false
	}
	var doc yaml.Node
	if yaml.Unmarshal(src, &doc) != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, false
	}
	parent := walk(doc.Content[0], path[:len(path)-1])
	if parent == nil || parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0 {
		return nil, false
	}
	t := text{src: src, starts: lineStarts(src)}
	key := path[len(path)-1].Key
	ki := -1
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			ki = i
		}
	}
	if ki < 0 {
		if value == nil {
			return src, true
		}
		return t.insert(parent, key, value)
	}
	k, v := parent.Content[ki], parent.Content[ki+1]
	if v.Anchor != "" || v.Kind == yaml.AliasNode || v.Style&yaml.TaggedStyle != 0 {
		return nil, false
	}
	switch {
	case value == nil:
		return t.remove(k, v)
	case isStr && v.Kind == yaml.ScalarNode:
		start, end, ok := t.scalarSpan(v)
		if !ok {
			return nil, false
		}
		enc, ok := encodeScalar(str, v.Style)
		if !ok {
			return nil, false
		}
		return t.replace(start, end, enc), true
	case !isStr && v.Kind == yaml.SequenceNode && plainItems(v):
		if v.Style&yaml.FlowStyle != 0 {
			start, end, ok := t.flowSpan(v)
			if !ok {
				return nil, false
			}
			enc, ok := t.flowSeq(start, end, strs)
			if !ok {
				return nil, false
			}
			return t.replace(start, end, enc), true
		}
		first, last, prefix, ok := t.blockLines(v)
		if !ok || len(strs) == 0 {
			return nil, false
		}
		var b strings.Builder
		for _, s := range strs {
			enc, ok := encodeScalar(s, 0)
			if !ok {
				return nil, false
			}
			b.WriteString(prefix + "- " + enc + "\n")
		}
		return t.replace(t.starts[first-1], t.lineEnd(last), b.String()), true
	}
	return nil, false
}

// walk follows path like setNode, without creating anything (nil when absent).
func walk(n *yaml.Node, path []PathElem) *yaml.Node {
	for _, el := range path {
		var next *yaml.Node
		switch {
		case el.Key != "":
			if n.Kind != yaml.MappingNode {
				return nil
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == el.Key {
					next = n.Content[i+1]
				}
			}
		default:
			if n.Kind != yaml.SequenceNode {
				return nil
			}
			idx := el.Index
			if el.MatchField != "" {
				idx = -1
				for i, item := range n.Content {
					if item.Kind != yaml.MappingNode {
						continue
					}
					for j := 0; j+1 < len(item.Content); j += 2 {
						if item.Content[j].Value == el.MatchField && item.Content[j+1].Value == el.MatchValue {
							idx = i
						}
					}
				}
			}
			if idx >= 0 && idx < len(n.Content) {
				next = n.Content[idx]
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// plainItems: every item is a scalar without comment, anchor or tag.
func plainItems(seq *yaml.Node) bool {
	for _, it := range seq.Content {
		if it.Kind != yaml.ScalarNode || it.Anchor != "" || it.Style&yaml.TaggedStyle != 0 ||
			it.HeadComment != "" || it.LineComment != "" || it.FootComment != "" {
			return false
		}
	}
	return true
}

// encodeScalar renders a string as a one-line scalar of the given style (0: plain when
// possible, quoted otherwise).
func encodeScalar(s string, style yaml.Style) (string, bool) {
	style &= yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle
	out, err := yaml.Marshal(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: style})
	if err != nil {
		return "", false
	}
	enc := strings.TrimSuffix(string(out), "\n")
	return enc, enc != "" && !strings.Contains(enc, "\n")
}

// encodeFlowItem renders a string as an item of a flow sequence.
func encodeFlowItem(s string) (string, bool) {
	out, err := yaml.Marshal(&yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle,
		Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}}})
	if err != nil {
		return "", false
	}
	enc := strings.TrimSuffix(string(out), "\n")
	if !strings.HasPrefix(enc, "[") || !strings.HasSuffix(enc, "]") || strings.Contains(enc, "\n") {
		return "", false
	}
	return enc[1 : len(enc)-1], true
}

type text struct {
	src    []byte
	starts []int // byte offset of each line (line n starts at starts[n-1])
}

func lineStarts(src []byte) []int {
	out := []int{0}
	for i, c := range src {
		if c == '\n' && i+1 < len(src) {
			out = append(out, i+1)
		}
	}
	return out
}

// offset converts a yaml.v3 position (1-based line, 1-based column in characters).
func (t text) offset(line, col int) (int, bool) {
	if line < 1 || line > len(t.starts) || col < 1 {
		return 0, false
	}
	off := t.starts[line-1]
	for range col - 1 {
		if off >= len(t.src) || t.src[off] == '\n' {
			return 0, false
		}
		_, w := utf8.DecodeRune(t.src[off:])
		off += w
	}
	return off, true
}

// lineEnd is the offset just past line's newline (or the end of the document).
func (t text) lineEnd(line int) int {
	if line < len(t.starts) {
		return t.starts[line]
	}
	return len(t.src)
}

// lineOf is the 1-based line of a byte offset.
func (t text) lineOf(off int) int {
	l, _ := slices.BinarySearch(t.starts, off+1)
	return l
}

// onlySpaces: the text of line before off is indentation.
func (t text) onlySpaces(off int) bool {
	for i := t.starts[t.lineOf(off)-1]; i < off; i++ {
		if t.src[i] != ' ' {
			return false
		}
	}
	return true
}

// restIsBlank: after off, line holds nothing but spaces (and no comment).
func (t text) restIsBlank(off int) bool {
	for i := off; i < len(t.src) && t.src[i] != '\n'; i++ {
		if t.src[i] != ' ' && t.src[i] != '\t' {
			return false
		}
	}
	return true
}

func (t text) replace(start, end int, with string) []byte {
	// slices.Concat sizes the result once and checks the sum for overflow.
	return slices.Concat(t.src[:start], []byte(with), t.src[end:])
}

// scalarSpan locates a one-line scalar.
func (t text) scalarSpan(n *yaml.Node) (int, int, bool) {
	start, ok := t.offset(n.Line, n.Column)
	if !ok {
		return 0, 0, false
	}
	switch n.Style {
	case 0:
		end := start + len(n.Value)
		if n.Value == "" || end > len(t.src) || string(t.src[start:end]) != n.Value {
			return 0, 0, false
		}
		return start, end, true
	case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
		q := t.src[start]
		if (n.Style == yaml.DoubleQuotedStyle && q != '"') || (n.Style == yaml.SingleQuotedStyle && q != '\'') {
			return 0, 0, false
		}
		for i := start + 1; i < len(t.src) && t.src[i] != '\n'; i++ {
			switch {
			case q == '"' && t.src[i] == '\\':
				i++
			case t.src[i] == q && q == '\'' && i+1 < len(t.src) && t.src[i+1] == '\'':
				i++
			case t.src[i] == q:
				return start, i + 1, true
			}
		}
	}
	return 0, 0, false
}

// flowSpan locates a flow sequence of plain or quoted scalars, from '[' to its ']'.
func (t text) flowSpan(n *yaml.Node) (int, int, bool) {
	start, ok := t.offset(n.Line, n.Column)
	if !ok || t.src[start] != '[' {
		return 0, 0, false
	}
	for i := start + 1; i < len(t.src); i++ {
		switch c := t.src[i]; c {
		case ']':
			return start, i + 1, true
		case '[', '{', '}', '#':
			return 0, 0, false
		case '"', '\'':
			for i++; i < len(t.src) && t.src[i] != c; i++ {
				if c == '"' && t.src[i] == '\\' {
					i++
				}
			}
		}
	}
	return 0, 0, false
}

// wrapWidth is the width past which a flow sequence written on one line is wrapped.
const wrapWidth = 100

// flowSeq renders items in place of the flow sequence src[start:end], keeping its inner
// padding ("[ a ]") and its wrapping: continuation lines aligned on the first item, no
// line longer than the longest original one (or wrapWidth for a one-line sequence that no
// longer fits).
func (t text) flowSeq(start, end int, items []string) (string, bool) {
	orig := string(t.src[start:end])
	if strings.HasPrefix(orig, "[\n") || strings.HasSuffix(strings.TrimRight(orig, " ]"), "\n") {
		return "", false // brackets on lines of their own: not a layout reproduced here
	}
	pad := ""
	if strings.HasPrefix(orig, "[ ") {
		pad = " "
	}
	encs := make([]string, len(items))
	for i, s := range items {
		enc, ok := encodeFlowItem(s)
		if !ok {
			return "", false
		}
		encs[i] = enc
	}
	if len(encs) == 0 {
		return "[]", true
	}
	// Width: the longest line the sequence covers, at least wrapWidth for a sequence on
	// one line (it stays on one line while it fits); col: where '[' is.
	first, last := t.lineOf(start), t.lineOf(end-1)
	width := 0
	for l := first; l <= last; l++ {
		width = max(width, utf8.RuneCount(bytes.TrimRight(t.src[t.starts[l-1]:t.lineEnd(l)], "\n")))
	}
	col := utf8.RuneCount(t.src[t.starts[first-1]:start])
	if first == last {
		one := "[" + pad + strings.Join(encs, ", ") + pad + "]"
		rest := utf8.RuneCount(bytes.TrimRight(t.src[end:t.lineEnd(last)], "\n"))
		if width = max(width, wrapWidth); col+utf8.RuneCountInString(one)+rest <= width {
			return one, true
		}
	}
	indent := strings.Repeat(" ", col+1+len(pad))
	var b strings.Builder
	b.WriteString("[" + pad)
	cur := col + 1 + len(pad)
	for i, enc := range encs {
		tail := ","
		if i == len(encs)-1 {
			tail = pad + "]"
		}
		sep := ""
		if i > 0 {
			sep = " "
		}
		if i > 0 && cur+len(sep)+utf8.RuneCountInString(enc+tail) > width {
			b.WriteString("\n" + indent)
			cur, sep = len(indent), ""
		}
		b.WriteString(sep + enc + tail)
		cur += len(sep) + utf8.RuneCountInString(enc+tail)
	}
	return b.String(), true
}

// blockLines locates a block sequence written one item per line ("- item"), on
// consecutive lines indented with spaces. prefix is the indentation before "-".
func (t text) blockLines(n *yaml.Node) (first, last int, prefix string, ok bool) {
	if len(n.Content) == 0 {
		return 0, 0, "", false
	}
	for i, it := range n.Content {
		line := n.Content[0].Line + i
		if it.Line != line {
			return 0, 0, "", false
		}
		start, end, ok := t.scalarSpan(it)
		if !ok || !t.restIsBlank(end) {
			return 0, 0, "", false
		}
		ls := t.starts[line-1]
		p := strings.TrimRight(string(t.src[ls:start]), " ")
		if !strings.HasSuffix(p, "-") || strings.Trim(p[:len(p)-1], " ") != "" {
			return 0, 0, "", false
		}
		if i == 0 {
			prefix = p[:len(p)-1]
		} else if p[:len(p)-1] != prefix {
			return 0, 0, "", false
		}
	}
	return n.Content[0].Line, n.Content[len(n.Content)-1].Line, prefix, true
}

// remove deletes the lines of key k and its value v.
func (t text) remove(k, v *yaml.Node) ([]byte, bool) {
	ks, ok := t.offset(k.Line, k.Column)
	if !ok || !t.onlySpaces(ks) || k.HeadComment != "" || k.LineComment != "" || k.FootComment != "" {
		return nil, false
	}
	var end int
	switch {
	case v.Kind == yaml.ScalarNode:
		_, end, ok = t.scalarSpan(v)
	case v.Kind == yaml.SequenceNode && plainItems(v) && v.Style&yaml.FlowStyle != 0:
		_, end, ok = t.flowSpan(v)
	case v.Kind == yaml.SequenceNode && plainItems(v):
		var last int
		_, last, _, ok = t.blockLines(v)
		end = t.lineEnd(last) - 1
	default:
		return nil, false
	}
	if !ok || !t.restIsBlank(end) {
		return nil, false
	}
	return t.replace(t.starts[k.Line-1], t.lineEnd(t.lineOf(end)), ""), true
}

// insert adds "key: value" to a block mapping, after its leading one-line entries.
func (t text) insert(m *yaml.Node, key string, value any) ([]byte, bool) {
	after := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if v.Kind != yaml.ScalarNode || v.Line != k.Line || v.FootComment != "" || k.FootComment != "" {
			break
		}
		if _, end, ok := t.scalarSpan(v); !ok || t.lineOf(end) != k.Line {
			break
		}
		after = i
	}
	if after < 0 {
		return nil, false
	}
	k := m.Content[after]
	ks, ok := t.offset(k.Line, k.Column)
	if !ok {
		return nil, false
	}
	indent := strings.Repeat(" ", utf8.RuneCount(t.src[t.starts[k.Line-1]:ks]))
	ek, ok := encodeScalar(key, 0)
	if !ok {
		return nil, false
	}
	var ev string
	switch v := value.(type) {
	case string:
		ev, ok = encodeScalar(v, 0)
	case []string:
		ev, ok = "[]", true
		if len(v) > 0 {
			parts := make([]string, len(v))
			for i, s := range v {
				if parts[i], ok = encodeFlowItem(s); !ok {
					break
				}
			}
			ev = "[" + strings.Join(parts, ", ") + "]"
		}
	default:
		ok = false
	}
	if !ok {
		return nil, false
	}
	at := t.lineEnd(k.Line)
	line := indent + ek + ": " + ev + "\n"
	if at == len(t.src) && (len(t.src) == 0 || t.src[len(t.src)-1] != '\n') {
		line = "\n" + line
	}
	return t.replace(at, at, line), true
}
