package typegen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const examples = "../../examples/basic/plugins"

// examplePlugins lists the plugin directories whose src/codi-env.d.ts must match typegen.
var examplePlugins = []string{
	"basic/plugins/welcome", "basic/plugins/onboarding",
	"showcase/plugins/tickets", "showcase/plugins/polls", "showcase/plugins/levels", "showcase/plugins/moderation",
}

// codeLines drops comment lines, so hand-written headers do not matter.
func codeLines(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func TestExamplesMatchHandWritten(t *testing.T) {
	for _, name := range examplePlugins {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("../../examples", name)
			schema, err := os.ReadFile(filepath.Join(dir, "config.schema.json"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Generate(schema)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, "src", "codi-env.d.ts"))
			if err != nil {
				t.Fatal(err)
			}
			if codeLines(got) != codeLines(string(want)) {
				t.Errorf("generated:\n%s\nhand-written:\n%s", got, want)
			}
			if !strings.HasPrefix(got, Header+"\n") {
				t.Errorf("missing header:\n%s", got)
			}
		})
	}
}

func TestNestedNote(t *testing.T) {
	// Array item defaults are applied by the runtime: no note for onboarding any more.
	for _, name := range []string{"onboarding", "welcome"} {
		schema, _ := os.ReadFile(filepath.Join(examples, name, "config.schema.json"))
		got, _ := Generate(schema)
		if strings.Contains(got, nestedNote) {
			t.Errorf("%s: unexpected nested-defaults note:\n%s", name, got)
		}
	}
	got, _ := Generate([]byte(`{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"object","properties":{"x":{"type":"string","default":"a"}}}}}}`))
	if !strings.Contains(got, nestedNote) {
		t.Errorf("nested-defaults note missing for additionalProperties defaults:\n%s", got)
	}
}

func TestTypes(t *testing.T) {
	schema := `{
	  "type": "object",
	  "required": ["mode"],
	  "$defs": { "color": { "type": "string", "pattern": "^#" } },
	  "properties": {
	    "mode": { "enum": ["a", "b"] },
	    "level": { "const": 3 },
	    "maybe": { "type": ["string", "null"] },
	    "roles": { "type": "array", "items": { "type": "string", "x-codi-ref": "role" } },
	    "refArr": { "type": "array", "x-codi-ref": "channel", "items": { "type": "string" } },
	    "tint": { "$ref": "#/$defs/color" },
	    "limits": {
	      "type": "object",
	      "properties": {
	        "max": { "type": "integer", "default": 5 },
	        "min": { "type": "integer" }
	      }
	    },
	    "map": { "type": "object", "additionalProperties": { "type": "number" } },
	    "free": { "type": "object" },
	    "either": { "oneOf": [ { "type": "string" }, { "type": "array", "items": { "enum": [1, 2] } } ] },
	    "kebab-name": { "type": "boolean", "default": false }
	  }
	}`
	got, err := Generate([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`    mode: "a" | "b";`,
		`    level?: 3;`,
		`    maybe?: string | null;`,
		`    roles?: string[];`,
		`    refArr?: string[];`,
		`    tint?: string;`,
		"    limits: {\n      max: number;\n      min?: number;\n    };", // materialized by its default
		`    map?: Record<string, number>;`,
		`    free?: Record<string, unknown>;`,
		`    either?: string | (1 | 2)[];`,
		`    "kebab-name": boolean;`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	empty, _ := Generate(nil)
	if !strings.Contains(empty, "interface PluginConfig {}") {
		t.Errorf("empty schema:\n%s", empty)
	}
}

// TestTypeCheck compiles the generated declarations together with the example plugin
// sources with tsc (skipped when the JS toolchain is not installed).
func TestTypeCheck(t *testing.T) {
	root, _ := filepath.Abs("../..")
	tsc := filepath.Join(root, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skip("tsc not installed (pnpm install)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	sdk := filepath.Join(root, "spec", "sdk", "codi-sdk.d.ts")
	for _, name := range examplePlugins {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(root, "examples", filepath.FromSlash(name))
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(filepath.Join(src, "src"))
			for _, e := range entries {
				if e.Name() == "codi-env.d.ts" || e.IsDir() {
					continue
				}
				data, _ := os.ReadFile(filepath.Join(src, "src", e.Name()))
				os.WriteFile(filepath.Join(dir, "src", e.Name()), data, 0o644)
			}
			schema, _ := os.ReadFile(filepath.Join(src, "config.schema.json"))
			got, err := Generate(schema)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, "src", "codi-env.d.ts"), []byte(got), 0o644)
			// the plugin's own tsconfig, with the SDK path made absolute
			raw, err := os.ReadFile(filepath.Join(src, "tsconfig.json"))
			if err != nil {
				t.Fatal(err)
			}
			var tsconfig map[string]any
			if err := json.Unmarshal(raw, &tsconfig); err != nil {
				t.Fatal(err)
			}
			co, _ := tsconfig["compilerOptions"].(map[string]any)
			co["paths"] = map[string]any{"@codi/sdk": []string{sdk}}
			data, _ := json.Marshal(tsconfig)
			os.WriteFile(filepath.Join(dir, "tsconfig.json"), data, 0o644)
			out, err := exec.Command(tsc, "-p", dir).CombinedOutput()
			if err != nil {
				t.Fatalf("tsc failed: %v\n%s", err, out)
			}
		})
	}
}
