package typegen

import (
	"fmt"
	"github.com/cicord/cicord/internal/config"
	"sort"
	"strings"
)

// Interop generates one augmentation from the versioned contract schemas. A collision
// between distinct installed versions must be resolved explicitly by the author.
func Interop(plugins []*config.LoadedPlugin) (string, error) {
	tables := map[string]map[string]string{}
	add := func(table, key, value string) error {
		if tables[table] == nil {
			tables[table] = map[string]string{}
		}
		if old, ok := tables[table][key]; ok && old != value {
			return fmt.Errorf("conflicting installed schemas for %s", key)
		}
		tables[table][key] = value
		return nil
	}
	ts := func(doc map[string]any) string { g := gen{root: doc}; return g.typeOf(doc, "    ", false, 0) }
	for _, p := range plugins {
		s := p.Manifest.Spec.Interop
		if s == nil {
			continue
		}
		prefix := p.Manifest.Metadata.Name + "."
		for _, t := range s.Publishes {
			key := config.ContractKey(t.Topic, t.V())
			if err := add("EventContracts", prefix+key, ts(p.Contracts[key].InputDoc)); err != nil {
				return "", err
			}
		}
		for _, kind := range []struct {
			table   string
			methods []string
		}{{"QueryContracts", s.Provides.Queries}, {"ActionContracts", s.Provides.Actions}} {
			for _, name := range kind.methods {
				c := p.Contracts[config.ContractKey(name, 1)]
				if err := add(kind.table, prefix+name, "{input: "+ts(c.InputDoc)+"; output: "+ts(c.OutputDoc)+"}"); err != nil {
					return "", err
				}
			}
		}
	}
	if len(tables) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("\ndeclare module \"@cicord/sdk\" {\n")
	for _, name := range []string{"EventContracts", "QueryContracts", "ActionContracts"} {
		rows := tables[name]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  interface %s {\n", name)
		var keys []string
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %q: %s;\n", k, rows[k])
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}
