package config

import "fmt"

// Helpers used by the HTTP API (internal/api) to validate plugin configuration edits
// made in the admin UI exactly like Load validates bot.yaml (https://cicord.dev/docs/configuration/bot/, §10.5).

// NormalizeConfig converts YAML-decoded values (int, map[string]any …) into
// JSON-compatible ones (float64 numbers), as Load does. A nil map gives an empty map.
func NormalizeConfig(m map[string]any) (map[string]any, error) { return normalizeMap(m) }

// CloneConfig deep-copies a JSON-compatible config map.
func CloneConfig(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return cloneMap(m)
}

// EffectiveConfig computes base ⊕ override (RFC 7386 merge patch) with the plugin's
// schema defaults applied, without modifying its arguments. Inputs must be normalized.
func EffectiveConfig(lp *LoadedPlugin, base, override map[string]any) map[string]any {
	cfg := CloneConfig(base)
	if override != nil {
		cfg, _ = MergePatch(cfg, cloneMap(override)).(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
		}
	}
	if lp != nil && lp.SchemaDoc != nil {
		ApplyDefaults(lp.SchemaDoc, cfg)
	}
	return cfg
}

// ValidateInstanceConfig validates an effective plugin config (defaults already
// applied, see EffectiveConfig) for one server: the plugin's config schema, then the
// x-cicord-ref references against the server's resource keys. Diagnostics are reported in
// file (the bot.yaml, relative to the repo root) under path (e.g. "/spec/plugins/0/config"),
// with messages prefixed by "[server <key>]" like Load.
func ValidateInstanceConfig(lp *LoadedPlugin, cfg map[string]any, serverKey string, srv *ServerSpec, file, path string, instanceNetwork ...[]string) Diagnostics {
	if lp == nil {
		return nil
	}
	doc := &document{file: file}
	var out Diagnostics
	if lp.ConfigSchema != nil {
		tmp := diagSink{}
		if !validateValue(&tmp, doc, lp.ConfigSchema, cfg, path) {
			for _, d := range tmp.ds {
				d.Message = fmt.Sprintf("[server %s] %s", serverKey, d.Message)
				out = append(out, d)
			}
			return out
		}
	}
	if srv != nil {
		for _, p := range checkRefs(lp.SchemaDoc, cfg, srv, "") {
			out = append(out, Diagnostic{Severity: SeverityError, File: file, Path: path + p.path,
				Message: fmt.Sprintf("[server %s] %s", serverKey, p.msg)})
		}
	}
	if len(instanceNetwork) > 0 {
		for _, p := range checkNetworkURLs(lp.SchemaDoc, lp.SchemaDoc, cfg, instanceNetwork[0], "", 0) {
			out = append(out, Diagnostic{Severity: SeverityError, File: file, Path: path + p.path, Message: fmt.Sprintf("[server %s] %s", serverKey, p.msg)})
		}
	}
	if lp.Manifest != nil {
		for _, p := range checkProviderLimits(lp.Manifest.Spec, cfg) {
			out = append(out, Diagnostic{Severity: SeverityError, File: file, Path: path + p.path, Message: fmt.Sprintf("[server %s] %s", serverKey, p.msg)})
		}
	}
	out.Sort()
	return out
}
