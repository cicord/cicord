package runtime

import (
	"fmt"
	"github.com/cicord/cicord/internal/config"
	"slices"
	"strings"
)

type InteropEdge struct {
	Kind     string `json:"kind"`
	From     string `json:"from"`
	To       string `json:"to"`
	Contract string `json:"contract"`
	Status   string `json:"status"`
}
type InteropGraph struct {
	Edges    []InteropEdge                `json:"edges"`
	Features map[string]map[string]string `json:"features"`
}

func (r *Runtime) InteropGraph(server string) InteropGraph {
	graph := InteropGraph{Edges: []InteropEdge{}, Features: map[string]map[string]string{}}
	a := r.Active()
	if a == nil {
		return graph
	}
	for _, i := range a.instances {
		if i.cfg.Server != server {
			continue
		}
		id := i.cfg.Bot + "/" + i.cfg.Name
		graph.Features[id] = i.cfg.Features
		s := i.cfg.Plugin.Manifest.Spec.Interop
		if s == nil {
			continue
		}
		for _, sub := range s.Subscribes {
			full := fmt.Sprintf("%s v%d", sub.Topic, sub.V())
			name, topic, _ := strings.Cut(sub.Topic, ".")
			matched := false
			for _, p := range a.instances {
				if p.cfg.Server != server || p.cfg.Plugin.Manifest.Metadata.Name != name {
					continue
				}
				pub := p.cfg.Plugin.Manifest.Spec.Interop
				ok := pub == nil && sub.V() == 1 && p.cfg.Bot == i.cfg.Bot
				if pub != nil {
					for _, t := range pub.Publishes {
						if t.Topic == topic && t.V() == sub.V() && (t.Scope == "server" || p.cfg.Bot == i.cfg.Bot) {
							ok = true
							break
						}
					}
				}
				if ok {
					st := "active"
					if !slices.Contains(i.cfg.Grants, "events.subscribe") {
						st = "missing events.subscribe"
					}
					if !slices.Contains(p.cfg.Grants, "events.publish") {
						st = "missing events.publish"
					}
					graph.Edges = append(graph.Edges, InteropEdge{"event", p.cfg.Bot + "/" + p.cfg.Name, id, full, st})
					matched = true
				}
			}
			if !matched {
				graph.Edges = append(graph.Edges, InteropEdge{"event", name, id, full, "publisher or version unavailable"})
			}
		}
		for _, k := range []struct {
			kind, prefix string
			methods      []string
		}{{"query", "plugins.query:", s.Consumes.Queries}, {"action", "plugins.call:", s.Consumes.Actions}} {
			for _, method := range k.methods {
				name, n, _ := strings.Cut(method, ".")
				to := name
				status := "active"
				p, err := resolvePeer(i, name)
				if err != nil {
					status = err.Error()
				} else {
					to = p.cfg.Bot + "/" + p.cfg.Name
					spec := p.cfg.Plugin.Manifest.Spec.Interop
					if spec == nil || (k.kind == "query" && !slices.Contains(spec.Provides.Queries, n)) || (k.kind == "action" && !slices.Contains(spec.Provides.Actions, n)) {
						status = "contract unavailable"
					}
					if p.cfg.Interop.AllowCallers != nil && !slices.Contains(p.cfg.Interop.AllowCallers, id) {
						status = "caller denied"
					}
				}
				if !config.HasGrant(i.cfg.Grants, k.prefix+method) {
					status = "missing " + k.prefix + method
				}
				graph.Edges = append(graph.Edges, InteropEdge{k.kind, id, to, method, status})
			}
		}
	}
	return graph
}
