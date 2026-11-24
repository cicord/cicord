package typegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/config"
)

// TestDefaultsConformance checks that typegen and config.ApplyDefaults agree: every field
// typegen declares non-optional is present in ctx.config once the runtime has applied the
// schema defaults to a minimal config (required fields only), and every field it declares
// optional while having a default is one the runtime does not fill.
func TestDefaultsConformance(t *testing.T) {
	cases := map[string]struct{ schema, config string }{
		"nested objects": {
			`{"type":"object","properties":{"o":{"type":"object","properties":{"a":{"type":"integer","default":1},"b":{"type":"string"}}}}}`,
			`{}`,
		},
		"array items": {
			`{"type":"object","required":["steps"],"properties":{"steps":{"type":"array","items":{"type":"object","required":["id"],"properties":{"id":{"type":"string"},"multiple":{"type":"boolean","default":false},"opts":{"type":"array","items":{"type":"object","properties":{"w":{"type":"number","default":2}}}}}}}}}`,
			`{"steps":[{"id":"a","opts":[{}]},{"id":"b"}]}`,
		},
		"array default": {
			`{"type":"object","properties":{"rules":{"type":"array","default":[{}],"items":{"type":"object","properties":{"n":{"type":"integer","default":3}}}}}}`,
			`{}`,
		},
		"refs": {
			`{"type":"object","$defs":{"color":{"type":"string","default":"#fff"},"item":{"type":"object","properties":{"c":{"$ref":"#/$defs/color"}}}},"properties":{"tint":{"$ref":"#/$defs/color"},"items":{"type":"array","items":{"$ref":"#/$defs/item"}},"box":{"$ref":"#/$defs/item"}}}`,
			`{"items":[{}]}`,
		},
		"optional object with a required field": {
			`{"type":"object","properties":{"hook":{"type":"object","required":["url"],"properties":{"url":{"type":"string"},"format":{"default":"slack"}}},"opts":{"type":"object","required":["n"],"properties":{"n":{"default":1}}}}}`,
			`{}`,
		},
		"maps and alternatives are not defaulted": {
			`{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"object","properties":{"x":{"type":"string","default":"a"}}}},"alt":{"oneOf":[{"type":"object","properties":{"y":{"type":"string","default":"b"}}},{"type":"string"}]}}}`,
			`{"m":{"k":{}},"alt":{}}`,
		},
	}
	for _, name := range []string{"welcome", "onboarding"} {
		schema, err := os.ReadFile(filepath.Join(examples, name, "config.schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := `{"channel":"general","steps":[{"id":"a","title":"A","options":[{"role":"r"}]}]}`
		cases["example "+name] = struct{ schema, config string }{string(schema), cfg}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var schema, cfg map[string]any
			if err := json.Unmarshal([]byte(c.schema), &schema); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.config), &cfg); err != nil {
				t.Fatal(err)
			}
			config.ApplyDefaults(schema, cfg)
			ts, err := Generate([]byte(c.schema))
			if err != nil {
				t.Fatal(err)
			}
			checkPresence(t, parseDecl(t, ts), cfg, "")
		})
	}
}

// field is a property of a generated object type.
type field struct {
	optional bool
	array    bool
	children map[string]*field // object type (or array of objects)
}

var propLine = regexp.MustCompile(`^\s*("?[^":?]+"?)(\??): (.*)$`)

// parseDecl parses the PluginConfig body of a generated file (inline object types only).
func parseDecl(t *testing.T, ts string) map[string]*field {
	t.Helper()
	lines := strings.Split(ts, "\n")
	var start int
	for i, l := range lines {
		if strings.Contains(l, "interface PluginConfig {") {
			start = i + 1
		}
	}
	root := map[string]*field{}
	stack := []map[string]*field{root}
	var open []*field
	for _, l := range lines[start:] {
		tl := strings.TrimSpace(l)
		if strings.HasPrefix(tl, "}") {
			if len(open) == 0 {
				break
			}
			f := open[len(open)-1]
			open, stack = open[:len(open)-1], stack[:len(stack)-1]
			f.array = strings.HasPrefix(tl, "}[]")
			continue
		}
		m := propLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		f := &field{optional: m[2] == "?"}
		stack[len(stack)-1][strings.Trim(m[1], `"`)] = f
		if strings.HasSuffix(m[3], "{") {
			f.children = map[string]*field{}
			stack = append(stack, f.children)
			open = append(open, f)
		}
	}
	return root
}

func checkPresence(t *testing.T, fields map[string]*field, v map[string]any, path string) {
	t.Helper()
	for k, f := range fields {
		val, ok := v[k]
		if !f.optional && !ok {
			t.Errorf("%s/%s is non-optional in the generated type but missing after ApplyDefaults: %v", path, k, v)
			continue
		}
		if !ok || f.children == nil {
			continue
		}
		switch tv := val.(type) {
		case map[string]any:
			if !f.array {
				checkPresence(t, f.children, tv, path+"/"+k)
			}
		case []any:
			for i, e := range tv {
				if m, ok := e.(map[string]any); ok {
					checkPresence(t, f.children, m, path+"/"+k+"/"+string(rune('0'+i)))
				}
			}
		}
	}
}
