package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/discord"
)

type OptionalFeature struct {
	When         string   `yaml:"when,omitempty" json:"when,omitempty"`
	Feature      string   `yaml:"feature" json:"feature"`
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Intents      []string `yaml:"intents,omitempty" json:"intents,omitempty"`
}
type InstanceInterop struct {
	Targets      map[string]string `yaml:"targets,omitempty" json:"targets,omitempty"`
	AllowCallers []string          `yaml:"allowCallers,omitempty" json:"allowCallers,omitempty"`
}
type Topic struct {
	Topic       string `yaml:"topic" json:"topic"`
	Scope       string `yaml:"scope,omitempty" json:"scope,omitempty"`
	Version     int    `yaml:"version,omitempty" json:"version,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

func (t *Topic) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		t.Topic = n.Value
		return nil
	}
	type plain Topic
	return n.Decode((*plain)(t))
}
func (t Topic) V() int { return max(1, t.Version) }

type Methods struct {
	Queries []string `yaml:"queries,omitempty" json:"queries,omitempty"`
	Actions []string `yaml:"actions,omitempty" json:"actions,omitempty"`
}
type InteropSpec struct {
	Types      string  `yaml:"types,omitempty" json:"types,omitempty"`
	Publishes  []Topic `yaml:"publishes,omitempty" json:"publishes,omitempty"`
	Subscribes []Topic `yaml:"subscribes,omitempty" json:"subscribes,omitempty"`
	Provides   Methods `yaml:"provides,omitempty" json:"provides,omitempty"`
	Consumes   Methods `yaml:"consumes,omitempty" json:"consumes,omitempty"`
}
type Contract struct {
	Input, Output       *jsonschema.Schema
	InputDoc, OutputDoc map[string]any
}

func ContractKey(name string, version int) string { return fmt.Sprintf("%s.v%d", name, version) }

func (p *LoadedPlugin) LoadContracts() error {
	seenFeatures := map[string]bool{}
	for _, f := range p.Manifest.Spec.Optional {
		if seenFeatures[f.Feature] {
			return fmt.Errorf("duplicate optional feature %q", f.Feature)
		}
		seenFeatures[f.Feature] = true
	}
	interop := p.Manifest.Spec.Interop
	if interop == nil {
		return nil
	}
	p.Contracts = map[string]Contract{}
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	load := func(name string, v int, method bool) error {
		key := ContractKey(name, v)
		if _, exists := p.Contracts[key]; exists {
			return fmt.Errorf("duplicate contract %s", key)
		}
		path := filepath.Join("contracts", key+".json")
		raw, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		input := json.RawMessage(raw)
		var output json.RawMessage
		if method {
			var pair struct{ Input, Output json.RawMessage }
			if err := json.Unmarshal(raw, &pair); err != nil {
				return err
			}
			input, output = pair.Input, pair.Output
			if input == nil || output == nil {
				return fmt.Errorf("%s requires input and output schemas", path)
			}
		}
		var c Contract
		compile := func(raw json.RawMessage) (*jsonschema.Schema, map[string]any, error) {
			// Keep contract compilation offline and independent of host files.
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				return nil, nil, err
			}
			if err := checkContractRefs(doc); err != nil {
				return nil, nil, err
			}
			return compilePluginConfigSchema("urn:cicord:contract:"+key, raw)
		}
		c.Input, c.InputDoc, err = compile(input)
		if err != nil {
			return fmt.Errorf("%s input: %w", path, err)
		}
		if method {
			c.Output, c.OutputDoc, err = compile(output)
			if err != nil {
				return fmt.Errorf("%s output: %w", path, err)
			}
		}
		p.Contracts[key] = c
		return nil
	}
	for _, t := range interop.Publishes {
		if err := load(t.Topic, t.V(), false); err != nil {
			return err
		}
	}
	for _, n := range slices.Concat(interop.Provides.Queries, interop.Provides.Actions) {
		if err := load(n, 1, true); err != nil {
			return err
		}
	}
	return nil
}
func checkContractRefs(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "$id" || k == "$schema" || k == "$ref" || k == "$dynamicRef" {
				s, _ := v.(string)
				if k == "$schema" && s == "https://json-schema.org/draft/2020-12/schema" {
					continue
				}
				if k != "$ref" || !strings.HasPrefix(s, "#/") {
					return fmt.Errorf("unsupported contract keyword or reference %s", k)
				}
			}
			if err := checkContractRefs(v); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := checkContractRefs(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s PluginSpec) Declared() (caps, intents []string) {
	caps = slices.Clone(s.Capabilities)
	intents = slices.Clone(s.Intents)
	for _, f := range s.Optional {
		caps = append(caps, f.Capabilities...)
		intents = append(intents, f.Intents...)
	}
	return
}
func HasGrant(grants []string, cap string) bool {
	if slices.Contains(grants, cap) {
		return true
	}
	if strings.HasPrefix(cap, "plugins.") {
		kind, name, _ := strings.Cut(cap, ":")
		base, _, ok := strings.Cut(name, ".")
		return ok && slices.Contains(grants, kind+":"+base+".*")
	}
	return false
}
func effectiveGrants(s PluginSpec, grants []string) []string {
	caps, _ := s.Declared()
	var out []string
	for _, cap := range caps {
		if HasGrant(grants, cap) {
			out = append(out, cap)
		} else if strings.HasSuffix(cap, ".*") {
			for _, g := range grants {
				if strings.HasPrefix(g, strings.TrimSuffix(cap, "*")) {
					out = append(out, g)
				}
			}
		}
	}
	return out
}
func (l *loader) features(s PluginSpec, grants []string, bot string, config map[string]any) map[string]string {
	out := map[string]string{}
	for _, f := range s.Optional {
		var missing []string
		if f.When != "" {
			var value any = config
			for _, part := range strings.Split(strings.TrimPrefix(f.When, "/"), "/") {
				m, _ := value.(map[string]any)
				value = m[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
			}
			if flag, ok := value.(bool); !ok || !flag {
				missing = append(missing, "disabled by "+f.When)
			}
		}
		for _, cap := range f.Capabilities {
			if !HasGrant(grants, cap) {
				missing = append(missing, cap+" not granted")
			}
		}
		if l.opts.PrivilegedIntents != nil {
			if flags, ok := l.opts.PrivilegedIntents(bot); ok {
				for _, n := range f.Intents {
					v, _ := discord.ParseIntents([]string{n})
					if v&discord.PrivilegedIntents != 0 && !flags.Has(v) {
						missing = append(missing, n+" not enabled")
					}
				}
			}
		}
		out[f.Feature] = strings.Join(missing, ", ")
	}
	return out
}
func (in Instance) EffectiveIntents() []string {
	s := in.Plugin.Manifest.Spec
	out := slices.Clone(s.Intents)
	for _, f := range s.Optional {
		if reason, ok := in.Features[f.Feature]; ok && reason == "" {
			out = append(out, f.Intents...)
		}
	}
	return out
}
func (r *Repo) ApplyOptionalIntents(bot string, enabled discord.Intents) {
	for n := range r.Instances {
		in := &r.Instances[n]
		if in.Bot != bot {
			continue
		}
		for _, f := range in.Plugin.Manifest.Spec.Optional {
			if in.Features[f.Feature] != "" {
				continue
			}
			for _, name := range f.Intents {
				v, _ := discord.ParseIntents([]string{name})
				if v&discord.PrivilegedIntents != 0 && !enabled.Has(v) {
					in.Features[f.Feature] = name + " not enabled"
					break
				}
			}
		}
	}
}
func (l *loader) checkInterop(repo *Repo) {
	seen := map[string]string{}
	for _, i := range repo.Instances {
		s := i.Plugin.Manifest.Spec.Interop
		for _, feature := range i.Plugin.Manifest.Spec.Optional {
			if i.Features[feature.Feature] != "" {
				continue
			}
			for _, cap := range feature.Capabilities {
				_, method, ok := strings.Cut(cap, ":")
				if !ok || !strings.HasPrefix(cap, "plugins.") {
					continue
				}
				provider, _, _ := strings.Cut(method, ".")
				if !slices.ContainsFunc(repo.Instances, func(other Instance) bool {
					return other.Server == i.Server && other.Plugin.Manifest.Metadata.Name == provider
				}) {
					i.Features[feature.Feature] = provider + " not installed"
					break
				}
			}
		}
		if s == nil {
			continue
		}
		for _, sub := range s.Subscribes {
			k := i.Server + "/" + i.Plugin.Manifest.Metadata.Name + "/" + ContractKey(sub.Topic, sub.V())
			if prev := seen[k]; prev != "" {
				l.sink.warnf(repo.Bots[i.Bot].File, 0, "", "%s and %s on %s both subscribe to %s", prev, i.ID(), i.Server, sub.Topic)
			}
			seen[k] = i.ID()
			plugin, topic, _ := strings.Cut(sub.Topic, ".")
			found, match := false, false
			for _, j := range repo.Instances {
				if j.Server != i.Server || j.Plugin.Manifest.Metadata.Name != plugin {
					continue
				}
				found = true
				p := j.Plugin.Manifest.Spec.Interop
				if p == nil {
					match = match || (sub.V() == 1 && j.Bot == i.Bot)
					continue
				}
				for _, t := range p.Publishes {
					match = match || (t.Topic == topic && t.V() == sub.V() && (t.Scope == "server" || j.Bot == i.Bot))
				}
			}
			if found && !match {
				l.sink.warnf(repo.Bots[i.Bot].File, 0, "", "%s subscribes to unavailable %s v%d", i.ID(), sub.Topic, sub.V())
			}
		}
	}
}
