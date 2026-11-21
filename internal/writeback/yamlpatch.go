// Package writeback turns UI/API configuration changes into git commits (ADR-0004):
// YAML files are patched at the node level (comments and key order preserved), then
// committed directly or proposed as a pull request according to codi.yaml policies.
package writeback

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// PathElem addresses a node: a mapping key, or a sequence item selected by Index or by
// a field match (e.g. the plugin instance whose `name` is "welcome").
type PathElem struct {
	Key        string
	Index      int
	MatchField string
	MatchValue string
}

func Key(k string) PathElem { return PathElem{Key: k, Index: -1} }
func Match(field, value string) PathElem {
	return PathElem{Index: -1, MatchField: field, MatchValue: value}
}
func Index(i int) PathElem { return PathElem{Index: i} }

func (p PathElem) String() string {
	switch {
	case p.MatchField != "":
		return fmt.Sprintf("[%s=%s]", p.MatchField, p.MatchValue)
	case p.Key != "":
		return p.Key
	default:
		return "[" + strconv.Itoa(p.Index) + "]"
	}
}

// SetYAML sets the value at path inside the YAML document src and returns the new
// document. Intermediate mappings are created as needed. Mappings are merged key by key
// so untouched keys keep their comments; keys absent from a map value are removed.
// A nil value removes the node at path.
func SetYAML(src []byte, path []PathElem, value any) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("expected a single YAML document")
	}
	if err := setNode(doc.Content[0], path, value); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setNode(n *yaml.Node, path []PathElem, value any) error {
	if len(path) == 0 {
		return assign(n, value)
	}
	el, rest := path[0], path[1:]
	switch {
	case el.Key != "":
		if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			*n = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: not a mapping", el)
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == el.Key {
				if len(rest) == 0 && value == nil {
					n.Content = append(n.Content[:i], n.Content[i+2:]...)
					return nil
				}
				return setNode(n.Content[i+1], rest, value)
			}
		}
		if value == nil {
			return nil // removing something absent
		}
		child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: el.Key}, child)
		return setNode(child, rest, value)
	default:
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s: not a sequence", el)
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
			if idx < 0 {
				return fmt.Errorf("no item with %s=%q", el.MatchField, el.MatchValue)
			}
		}
		if idx < 0 || idx >= len(n.Content) {
			return fmt.Errorf("index %d out of range", idx)
		}
		if len(rest) == 0 && value == nil {
			n.Content = append(n.Content[:idx], n.Content[idx+1:]...)
			return nil
		}
		return setNode(n.Content[idx], rest, value)
	}
}

// assign replaces n's value with v, merging mappings recursively to keep comments.
func assign(n *yaml.Node, v any) error {
	if m, ok := v.(map[string]any); ok && n.Kind == yaml.MappingNode {
		// update / delete existing keys in place
		kept := n.Content[:0]
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, val := n.Content[i], n.Content[i+1]
			nv, present := m[k.Value]
			if !present {
				continue
			}
			seen[k.Value] = true
			if err := assign(val, nv); err != nil {
				return err
			}
			kept = append(kept, k, val)
		}
		n.Content = kept
		// append new keys in sorted order for determinism
		var added []string
		for k := range m {
			if !seen[k] {
				added = append(added, k)
			}
		}
		sort.Strings(added)
		for _, k := range added {
			child := &yaml.Node{}
			if err := assign(child, m[k]); err != nil {
				return err
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, child)
		}
		return nil
	}
	var fresh yaml.Node
	if err := fresh.Encode(v); err != nil {
		return err
	}
	// keep comments attached to the old node
	fresh.HeadComment, fresh.LineComment, fresh.FootComment = n.HeadComment, n.LineComment, n.FootComment
	if n.Kind == yaml.ScalarNode && fresh.Kind == yaml.ScalarNode && n.Style != 0 && fresh.Tag == "!!str" {
		fresh.Style = n.Style // keep the quoting style of strings
	}
	*n = fresh
	return nil
}

// AppendYAML appends value to the sequence at path (created when missing or null).
func AppendYAML(src []byte, path []PathElem, value any) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("expected a single YAML document")
	}
	n := doc.Content[0]
	for i, el := range path {
		if el.Key == "" {
			return nil, fmt.Errorf("AppendYAML only supports key paths")
		}
		var next *yaml.Node
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == el.Key {
				next = n.Content[j+1]
			}
		}
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if i == len(path)-1 {
				next = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: el.Key}, next)
		}
		n = next
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" || n.Kind == yaml.SequenceNode && len(n.Content) == 0 {
		*n = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"} // drop flow style of `[]`
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%v is not a sequence", path)
	}
	var item yaml.Node
	if err := item.Encode(value); err != nil {
		return nil, err
	}
	n.Content = append(n.Content, &item)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}
