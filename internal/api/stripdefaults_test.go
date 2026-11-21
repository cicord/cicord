package api

import (
	"encoding/json"
	"reflect"
	"testing"
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
