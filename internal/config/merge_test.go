package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyDefaultsArraysAndRefs(t *testing.T) {
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{
	  "type": "object",
	  "$defs": {
	    "step": { "type": "object", "properties": {
	      "multiple": { "type": "boolean", "default": false },
	      "tags": { "type": "array", "items": { "type": "object", "properties": { "w": { "type": "number", "default": 1 } } } }
	    } },
	    "color": { "type": "string", "default": "#fff" }
	  },
	  "properties": {
	    "steps": { "type": "array", "items": { "$ref": "#/$defs/step" } },
	    "rules": { "type": "array", "default": [{}], "items": { "type": "object", "properties": { "n": { "type": "integer", "default": 3 } } } },
	    "tint": { "$ref": "#/$defs/color" },
	    "plain": { "type": "array", "items": { "type": "string", "default": "x" } },
	    "map": { "type": "object", "additionalProperties": { "type": "object", "properties": { "k": { "default": 1 } } } },
	    "hook": { "type": "object", "required": ["url"], "properties": { "url": { "type": "string" }, "format": { "default": "slack" } } }
	  }
	}`), &schema)
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"steps":[{"tags":[{},{"w":5}]},{"multiple":true},"not-an-object"],"plain":["a"],"map":{"a":{}}}`), &cfg)
	ApplyDefaults(schema, cfg)
	var want map[string]any
	_ = json.Unmarshal([]byte(`{
	  "steps":[{"multiple":false,"tags":[{"w":1},{"w":5}]},{"multiple":true},"not-an-object"],
	  "rules":[{"n":3}],
	  "tint":"#fff",
	  "plain":["a"],
	  "map":{"a":{}}
	}`), &want)
	if !reflect.DeepEqual(cfg, want) {
		got, _ := json.Marshal(cfg)
		t.Errorf("defaults = %s", got)
	}
	// The schema's own default value is never mutated through the config.
	rules := schema["properties"].(map[string]any)["rules"].(map[string]any)["default"].([]any)
	if len(rules[0].(map[string]any)) != 0 {
		t.Errorf("schema default mutated: %v", rules)
	}

	// Cyclic refs terminate.
	var cyclic map[string]any
	_ = json.Unmarshal([]byte(`{"$defs":{"n":{"type":"object","properties":{"next":{"$ref":"#/$defs/n"},"v":{"default":1}}}},"$ref":"#/$defs/n"}`), &cyclic)
	c := map[string]any{"next": map[string]any{"next": map[string]any{}}}
	ApplyDefaults(cyclic, c)
	if c["v"] != 1.0 || c["next"].(map[string]any)["v"] != 1.0 {
		t.Errorf("cyclic = %v", c)
	}
}
