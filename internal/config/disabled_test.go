package config

import "testing"

func TestDisabledConfigurationUsesStrictActiveSchema(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"channel"}, "properties": map[string]any{"channel": map[string]any{"type": "string"}}, "additionalProperties": false}
	lp, err := ConfigSchemaPlugin(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cfg   map[string]any
		valid bool
	}{{map[string]any{"enabled": false}, true}, {map[string]any{"enabled": true}, false}, {map[string]any{"enabled": "false"}, false}, {map[string]any{"enabled": true, "channel": "x", "unexpected": 1}, false}, {map[string]any{"channel": "x", "enabled": true}, true}, {map[string]any{"channel": "x"}, true}} {
		if ds := ValidateInstanceConfig(lp, test.cfg, "space", nil, "bot.yaml", "/config"); test.valid == ds.HasErrors() {
			t.Fatalf("%v: %v", test.cfg, ds)
		}
	}
}
