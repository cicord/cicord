package config

import "strings"

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
// JSON Schema: recursively for nested objects (`properties`), for every object element of
// arrays (`items`, including elements that come from an array's own `default`), and
// through local `$ref`s (#/$defs/…, #/definitions/…). Values of `additionalProperties`
// maps and `oneOf`/`anyOf` alternatives are left alone (which alternative applies is
// ambiguous). `codi plugin typegen` (internal/typegen) follows the same rules to decide
// which config fields are non-optional.
func ApplyDefaults(schema map[string]any, v map[string]any) {
	applyObjectDefaults(schema, schema, v, 0)
}

// maxDefaultsDepth bounds recursion through (possibly cyclic) $refs.
const maxDefaultsDepth = 32

// ResolveSchemaRef follows local `$ref`s of s against root. Sibling keywords override the
// target's (JSON Schema 2020-12 allows keywords next to $ref). An unresolvable ref gives
// an empty schema.
func ResolveSchemaRef(root, s map[string]any) map[string]any {
	for i := 0; i < 8 && s != nil; i++ {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		target := schemaPointer(root, ref)
		if target == nil {
			return map[string]any{}
		}
		merged := make(map[string]any, len(target)+len(s))
		for k, v := range target {
			merged[k] = v
		}
		for k, v := range s {
			if k != "$ref" {
				merged[k] = v
			}
		}
		s = merged
	}
	return s
}

func schemaPointer(root map[string]any, ref string) map[string]any {
	p, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil
	}
	cur := any(root)
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	m, _ := cur.(map[string]any)
	return m
}

func applyObjectDefaults(root, schema map[string]any, v map[string]any, depth int) {
	if depth > maxDefaultsDepth {
		return
	}
	schema = ResolveSchemaRef(root, schema)
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		sub, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sub = ResolveSchemaRef(root, sub)
		cur, present := v[k]
		if !present {
			if d, ok := sub["default"]; ok {
				v[k] = cloneValue(d)
				cur, present = v[k], true
			}
		}
		if present {
			applyValueDefaults(root, sub, cur, depth+1)
		} else if _, hasProps := sub["properties"]; hasProps && sub["type"] == "object" {
			// Materialize an absent object only if it ends up with defaults and they cover
			// all its required properties (an optional object with a required field must
			// stay absent, or the config would no longer validate).
			obj := map[string]any{}
			applyObjectDefaults(root, sub, obj, depth+1)
			if len(obj) > 0 && hasRequired(sub, obj) {
				v[k] = obj
			}
		}
	}
}

// hasRequired reports whether obj has every property listed in schema's `required`.
func hasRequired(schema map[string]any, obj map[string]any) bool {
	req, _ := schema["required"].([]any)
	for _, r := range req {
		if name, ok := r.(string); ok {
			if _, present := obj[name]; !present {
				return false
			}
		}
	}
	return true
}

// applyValueDefaults applies defaults inside a present value: an object, or each object
// element of an array.
func applyValueDefaults(root, schema map[string]any, v any, depth int) {
	switch tv := v.(type) {
	case map[string]any:
		applyObjectDefaults(root, schema, tv, depth)
	case []any:
		if depth > maxDefaultsDepth {
			return
		}
		items, ok := ResolveSchemaRef(root, schema)["items"].(map[string]any)
		if !ok {
			return
		}
		for _, e := range tv {
			applyValueDefaults(root, items, e, depth+1)
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
