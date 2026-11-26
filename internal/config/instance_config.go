package config

import "fmt"

// Helpers used by the HTTP API (internal/api) to validate plugin configuration edits
// made in the admin UI exactly like Load validates bot.yaml (https://cicord.dev/docs/configuration/bot/, §10.5).

// Disabled recognizes the reserved installation switch. Existing configurations
// without the switch remain active.
func Disabled(cfg map[string]any) bool { enabled, ok := cfg["enabled"].(bool); return ok && !enabled }
func (in Instance) Disabled() bool     { return Disabled(in.Config) }

// An active explicit switch also works with older strict schemas which do not
// declare the host-owned property. It grants no exemption to the other fields.
func schemaInput(lp *LoadedPlugin, cfg map[string]any) map[string]any {
	if enabled, ok := cfg["enabled"].(bool); ok && enabled {
		props, _ := lp.SchemaDoc["properties"].(map[string]any)
		if _, declared := props["enabled"]; !declared {
			copy := CloneConfig(cfg)
			delete(copy, "enabled")
			return copy
		}
	}
	return cfg
}

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
	if !Disabled(cfg) && lp != nil && lp.SchemaDoc != nil {
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
	if lp == nil || Disabled(cfg) {
		return nil
	}
	doc := &document{file: file}
	var out Diagnostics
	if v, present := cfg["enabled"]; present {
		if _, ok := v.(bool); !ok {
			return Diagnostics{{Severity: SeverityError, File: file, Path: path + "/enabled", Message: "enabled must be a boolean"}}
		}
	}
	if lp.ConfigSchema != nil {
		tmp := diagSink{}
		if !validateValue(&tmp, doc, lp.ConfigSchema, cfg, path) {
			if enabled, _ := cfg["enabled"].(bool); enabled {
				tmp = diagSink{}
				validateValue(&tmp, doc, lp.ConfigSchema, schemaInput(lp, cfg), path)
			}
		}
		if len(tmp.ds) > 0 {
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
