package config

import "encoding/json"

// ConfigSchemaPlugin returns a LoadedPlugin carrying only a config schema (compiled from
// its JSON document), for validating instance configs against another version of a plugin
// (Store updates) with EffectiveConfig and ValidateInstanceConfig. A nil or empty schema
// gives a plugin without config schema.
func ConfigSchemaPlugin(schema map[string]any) (*LoadedPlugin, error) {
	lp := &LoadedPlugin{SchemaDoc: schema}
	if len(schema) == 0 {
		return lp, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	s, doc, err := compilePluginConfigSchema("mem://config.schema.json", raw)
	if err != nil {
		return nil, err
	}
	lp.ConfigSchema, lp.SchemaDoc = s, doc
	return lp, nil
}
