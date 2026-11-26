package plugin

import (
	"fmt"
	"github.com/cicord/cicord/internal/config"
	"regexp"
	"slices"
)

// esbuild outputs plain property accesses and double quoted literal arguments.
// Computed topics always receive the authoritative check at publication.
var publishLiteral = regexp.MustCompile(`\.events\.publish\(\s*"([a-zA-Z][a-zA-Z0-9]*)"\s*[,)]`)

func (d *Definition) CheckInterop(spec config.PluginSpec, code string) []string {
	var problems []string
	for _, kind := range []struct {
		what  string
		funcs []string
	}{{"queries", d.Queries}, {"actions", d.Actions}, {"subscriptions", d.Subscriptions}} {
		if spec.Interop == nil {
			if kind.what != "subscriptions" && len(kind.funcs) > 0 {
				problems = append(problems, kind.what+" require spec.interop")
			}
			continue
		}
		var declared []string
		switch kind.what {
		case "queries":
			declared = spec.Interop.Provides.Queries
		case "actions":
			declared = spec.Interop.Provides.Actions
		case "subscriptions":
			for _, s := range spec.Interop.Subscribes {
				declared = append(declared, s.Topic)
			}
		}
		for _, n := range declared {
			if !slices.Contains(kind.funcs, n) {
				problems = append(problems, fmt.Sprintf("declared %s.%s has no handler", kind.what, n))
			}
		}
		for _, n := range kind.funcs {
			if !slices.Contains(declared, n) {
				problems = append(problems, fmt.Sprintf("%s.%s is not declared", kind.what, n))
			}
		}
	}
	if s := spec.Interop; s != nil {
		if spec.Delivery != "at-least-once" {
			problems = append(problems, "spec.interop requires spec.delivery: at-least-once")
		}
		for _, m := range publishLiteral.FindAllStringSubmatch(code, -1) {
			if !slices.ContainsFunc(s.Publishes, func(t config.Topic) bool { return t.Topic == m[1] }) {
				problems = append(problems, "undeclared literal event topic "+m[1])
			}
		}
	}
	return problems
}
