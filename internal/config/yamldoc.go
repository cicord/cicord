package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// document is a parsed YAML file: the node tree (for line numbers and write-back) and
// its JSON-compatible value (for schema validation).
type document struct {
	file  string // relative path
	root  *yaml.Node
	value any
}

func parseDocument(file string, data []byte) (*document, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	if n.Kind == 0 {
		return nil, fmt.Errorf("empty document")
	}
	root := &n
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	v, err := nodeToValue(root)
	if err != nil {
		return nil, err
	}
	return &document{file: file, root: root, value: v}, nil
}

// nodeToValue converts a YAML node into JSON-compatible Go values
// (map[string]any, []any, string, float64/int64 via json-like numbers, bool, nil).
func nodeToValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return nodeToValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag == "!!merge" {
				return nil, fmt.Errorf("line %d: YAML merge keys are not supported", k.Line)
			}
			v, err := nodeToValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeToValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err != nil {
				return nil, err
			}
			return float64(i), nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, fmt.Errorf("line %d: non-finite numbers are not allowed", n.Line)
			}
			return f, nil
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

// lineOf returns the line of the node addressed by a JSON pointer ("/spec/roles/admin"),
// falling back to the closest existing ancestor.
func (d *document) lineOf(pointer string) int {
	n := d.root
	line := n.Line
	if pointer == "" || pointer == "/" {
		return line
	}
	for _, tok := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		var next *yaml.Node
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == tok {
					line = n.Content[i].Line
					next = n.Content[i+1]
					break
				}
			}
		case yaml.SequenceNode:
			if idx, err := strconv.Atoi(tok); err == nil && idx >= 0 && idx < len(n.Content) {
				next = n.Content[idx]
				line = next.Line
			}
		}
		if next == nil {
			return line
		}
		n = next
	}
	return line
}

func jsonPointer(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// suspiciousComments finds plain (unquoted) scalars followed by a comment that starts
// with "#" directly followed by text (e.g. `message: rendez-vous dans #général`), which
// YAML silently truncates. Returns (line, value, comment) triples.
func (d *document) suspiciousComments() [][3]string {
	var out [][3]string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode && n.Style == 0 && n.Tag == "!!str" &&
			len(n.LineComment) > 1 && n.LineComment[0] == '#' && n.LineComment[1] != ' ' && n.LineComment[1] != '#' {
			out = append(out, [3]string{strconv.Itoa(n.Line), n.Value, n.LineComment})
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(d.root)
	return out
}
