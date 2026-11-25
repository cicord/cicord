package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/writeback"
)

// The admin UI saves the effective config (defaults applied, also inside arrays): the
// write-back must not copy those defaults into bot.yaml.
func TestStripDefaultsNested(t *testing.T) {
	var schema, raw, effective, want map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","properties":{
	  "title":{"type":"string","default":"Welcome!"},
	  "panel":{"type":"object","properties":{"color":{"type":"string","default":"#fff"},"label":{"type":"string","default":"Go"}}},
	  "steps":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"multiple":{"type":"boolean","default":false},"description":{"type":"string","default":""}}}}
	}}`), &schema)
	// written in bot.yaml: the first step says multiple: false explicitly (kept)
	_ = json.Unmarshal([]byte(`{"steps":[{"id":"a","multiple":false},{"id":"b"}],"panel":{"label":"Go"}}`), &raw)
	// what the UI sends back: effective config with one title edited
	_ = json.Unmarshal([]byte(`{"title":"Welcome!","panel":{"color":"#fff","label":"Go"},
	  "steps":[{"id":"a","multiple":false,"description":""},{"id":"B","multiple":false,"description":""},{"id":"c","multiple":true,"description":""}]}`), &effective)
	_ = json.Unmarshal([]byte(`{"panel":{"label":"Go"},"steps":[{"id":"a","multiple":false},{"id":"B"},{"id":"c","multiple":true}]}`), &want)
	if got := stripDefaults(effective, raw, schema); !reflect.DeepEqual(got, want) {
		b, _ := json.Marshal(got)
		t.Errorf("stripDefaults = %s", b)
	}
}

// Plugin schemas are decoded with json.Number (jsonschema.UnmarshalJSON), so numeric
// defaults in the effective config are json.Number while the config sent by the admin UI
// decodes to float64: a numeric default equal to the sent value must not end up in the
// override (scope server) nor in the base config (scope base).
func TestNumericDefaultsNotWritten(t *testing.T) {
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","properties":{
	  "question":{"type":"string","default":"?"},
	  "maxHours":{"type":"integer","default":168},
	  "ratio":{"type":"number","default":0.5},
	  "sizes":{"type":"array","items":{"type":"integer"},"default":[1,2]},
	  "anonymous":{"type":"boolean","default":false},
	  "choices":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"weight":{"type":"integer","default":1}}}}
	}}`), &schema)
	lp, err := config.ConfigSchemaPlugin(schema)
	if err != nil {
		t.Fatal(err)
	}
	rawBase, _ := config.NormalizeConfig(map[string]any{"choices": []any{map[string]any{"label": "a"}}})
	// What the admin UI sends back: the effective config, through JSON, with one edit.
	eff, _ := json.Marshal(config.EffectiveConfig(lp, rawBase, nil))
	var sent map[string]any
	if err := json.Unmarshal(eff, &sent); err != nil {
		t.Fatal(err)
	}
	sent["question"] = "Lunch?"
	newCfg, _ := config.NormalizeConfig(sent)

	patch := pruneNoopNulls(writeback.DiffMergePatch(config.EffectiveConfig(lp, rawBase, nil), newCfg), rawBase)
	if want := map[string]any{"question": "Lunch?"}; !reflect.DeepEqual(patch, want) {
		b, _ := json.Marshal(patch)
		t.Errorf("server override = %s, want only the edited question", b)
	}
	base := stripDefaults(config.CloneConfig(newCfg), rawBase, lp.SchemaDoc)
	if b, _ := json.Marshal(base); string(b) != `{"choices":[{"label":"a"}],"question":"Lunch?"}` {
		t.Errorf("base config = %s", b)
	}
	if got := changedKeys(config.EffectiveConfig(lp, rawBase, nil), config.EffectiveConfig(lp, rawBase, patch)); !reflect.DeepEqual(got, []string{"question"}) {
		t.Errorf("changed keys = %v", got)
	}
}
