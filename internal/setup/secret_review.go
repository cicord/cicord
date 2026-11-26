package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

type SecretBudgetView struct {
	Provider string `json:"provider"`
	Config   string `json:"config"`
	Limit    int    `json:"limit"`
}
type BoundPolicy struct {
	Hosts  []string          `json:"hosts"`
	Budget *SecretBudgetView `json:"budget,omitempty"`
}
type SecretChange struct {
	Name    string      `json:"name"`
	Before  BoundPolicy `json:"before"`
	After   BoundPolicy `json:"after"`
	Widened bool        `json:"widened"`
}

func hostCovered(narrow, wide string) bool {
	n, np, _ := strings.Cut(narrow, ":")
	w, wp, _ := strings.Cut(wide, ":")
	if np == "" {
		np = "443"
	}
	if wp == "" {
		wp = "443"
	}
	if np != wp {
		return false
	}
	return n == w || strings.HasPrefix(w, "*.") && strings.HasSuffix(n, w[1:])
}

func boundPolicy(declarations []config.PluginSecret, name string, network []string, cfg map[string]any) BoundPolicy {
	p := BoundPolicy{Hosts: []string{}}
	for _, s := range declarations {
		if s.Name != name {
			continue
		}
		for _, host := range s.Hosts {
			for _, allowed := range network {
				// Overlapping host patterns have a well-defined narrower member.
				switch {
				case hostCovered(host, allowed):
					p.Hosts = append(p.Hosts, strings.TrimSuffix(host, ":443"))
				case hostCovered(allowed, host):
					p.Hosts = append(p.Hosts, strings.TrimSuffix(allowed, ":443"))
				}
			}
		}
		if s.Budget != nil {
			limit, _ := s.BudgetLimit(cfg)
			p.Budget = &SecretBudgetView{Provider: s.Budget.Provider, Config: s.Budget.DailyLimitConfig, Limit: limit}
		}
		break
	}
	slices.Sort(p.Hosts)
	p.Hosts = slices.Compact(p.Hosts)
	return p
}
func widerPolicy(old, next BoundPolicy) bool {
	for _, h := range next.Hosts {
		if !slices.ContainsFunc(old.Hosts, func(prev string) bool { return hostCovered(h, prev) }) {
			return true
		}
	}
	return old.Budget != nil && (next.Budget == nil || next.Budget.Provider != old.Budget.Provider || next.Budget.Limit > old.Budget.Limit)
}

func boundChanges(old *config.Instance, pi config.PluginInstance, spec UpdateSpec, cfg map[string]any) []SecretChange {
	var changes []SecretChange
	network := slices.Clone(spec.Network)
	if pi.Network != nil {
		network = append(network, pi.Network.Allow...)
	}
	for _, name := range sortedKeys(pi.Secrets) {
		before := BoundPolicy{Hosts: []string{}}
		if old != nil && old.Plugin != nil && old.Plugin.Manifest != nil {
			before = boundPolicy(old.Plugin.Manifest.Spec.Secrets, name, old.NetworkAllow, old.Config)
		}
		after := boundPolicy(spec.SecretDeclarations, name, network, cfg)
		sameBudget := before.Budget == nil && after.Budget == nil || before.Budget != nil && after.Budget != nil && *before.Budget == *after.Budget
		if sameBudget && slices.Equal(before.Hosts, after.Hosts) {
			continue
		}
		changes = append(changes, SecretChange{name, before, after, widerPolicy(before, after)})
	}
	return changes
}

type secretReviewEntry struct {
	Server, Bot, Instance, Binding string
	Change                         SecretChange
}

func secretReviewToken(spec UpdateSpec, entries []secretReviewEntry) string {
	if len(entries) == 0 {
		return ""
	}
	// Bind approval to the reviewed artifact and bindings. Only the digest goes
	// to the client. No credential is read while planning an update.
	integrity := ""
	if spec.Bundled() {
		integrity = registry.Integrity(spec.Files)
	} else {
		integrity = spec.Lock.Integrity
	}
	payload, _ := json.Marshal(struct {
		Source, Version, Integrity string
		Lock                       *registry.LockEntry
		Entries                    []secretReviewEntry
	}{spec.Source, spec.Version, integrity, spec.Lock, entries})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (p *UpdatePlan) AcceptsSecretReview(token string) bool {
	return p.SecretReview == "" || token == p.SecretReview
}
