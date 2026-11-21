package config

// MergePatch applies an RFC 7386 JSON merge patch to target and returns the result.
// target may be modified.
func MergePatch(target, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return cloneValue(patch)
	}
	tm, ok := target.(map[string]any)
	if !ok {
		tm = map[string]any{}
	}
	for k, pv := range pm {
		if pv == nil {
			delete(tm, k)
			continue
		}
		tm[k] = MergePatch(tm[k], pv)
	}
	return tm
}

// ApplyDefaults fills missing properties of v with the `default` values declared in a
// JSON Schema (recursively for nested objects with properties).
func ApplyDefaults(schema map[string]any, v map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		sub, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cur, present := v[k]
		if !present {
			if d, ok := sub["default"]; ok {
				v[k] = cloneValue(d)
				cur, present = v[k], true
			}
		}
		if present {
			if obj, ok := cur.(map[string]any); ok {
				ApplyDefaults(sub, obj)
			}
		} else if _, hasProps := sub["properties"]; hasProps && sub["type"] == "object" {
			// Materialize nested objects only if they end up with defaults.
			obj := map[string]any{}
			ApplyDefaults(sub, obj)
			if len(obj) > 0 {
				v[k] = obj
			}
		}
	}
}

func cloneValue(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		return cloneMap(tv)
	case []any:
		out := make([]any, len(tv))
		for i, e := range tv {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return tv
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}
