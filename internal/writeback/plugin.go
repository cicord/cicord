package writeback

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"reflect"
)

// DiffMergePatch returns the minimal RFC 7386 merge patch turning from into to
// (nil when they are equal).
func DiffMergePatch(from, to map[string]any) map[string]any {
	patch := map[string]any{}
	for k, tv := range to {
		fv, ok := from[k]
		if !ok {
			patch[k] = tv
			continue
		}
		fm, fIsMap := fv.(map[string]any)
		tm, tIsMap := tv.(map[string]any)
		if fIsMap && tIsMap {
			if sub := DiffMergePatch(fm, tm); sub != nil {
				patch[k] = sub
			}
			continue
		}
		if !reflect.DeepEqual(fv, tv) {
			patch[k] = tv
		}
	}
	for k := range from {
		if _, ok := to[k]; !ok {
			patch[k] = nil
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

// PluginConfigEdit returns an edit of bots/<bot>/bot.yaml that sets the configuration of
// one plugin instance.
//   - server == "": replaces the base `config`;
//   - server != "": writes `overrides.<server>.config` = patch (removed when patch is nil).
//
// For the server case, pass the minimal patch from DiffMergePatch(baseEffective, desired).
func PluginConfigEdit(instance, server string, value map[string]any) EditFunc {
	return func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, fmt.Errorf("bot file does not exist")
		}
		base := []PathElem{Key("spec"), Key("plugins"), Match("name", instance)}
		if server == "" {
			if len(value) == 0 {
				return SetYAML(cur, append(base, Key("config")), nil)
			}
			return SetYAML(cur, append(base, Key("config")), value)
		}
		if value == nil {
			out, err := SetYAML(cur, append(base, Key("overrides"), Key(server)), nil)
			if err != nil {
				return nil, err
			}
			return dropEmptyOverrides(out, base)
		}
		return SetYAML(cur, append(base, Key("overrides"), Key(server), Key("config")), toNullable(value))
	}
}

// toNullable keeps explicit nulls of a merge patch as YAML nulls.
func toNullable(m map[string]any) map[string]any { return m }

func dropEmptyOverrides(doc []byte, base []PathElem) ([]byte, error) {
	var probe struct {
		Spec struct {
			Plugins []struct {
				Name      string         `yaml:"name"`
				Overrides map[string]any `yaml:"overrides"`
			} `yaml:"plugins"`
		} `yaml:"spec"`
	}
	if err := yamlUnmarshal(doc, &probe); err != nil {
		return doc, nil
	}
	name := base[len(base)-1].MatchValue
	for _, p := range probe.Spec.Plugins {
		if p.Name == name && p.Overrides != nil && len(p.Overrides) == 0 {
			return SetYAML(doc, append(base, Key("overrides")), nil)
		}
	}
	return doc, nil
}

func yamlUnmarshal(data []byte, out any) error { return yaml.Unmarshal(data, out) }
