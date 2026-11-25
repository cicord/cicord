package analytics

import (
	"context"
	"slices"
	"strings"

	"github.com/cicord/cicord/internal/state"
)

// Channels created by a plugin (state.Resource.Owner "plugin:<bot>/<instance>", or
// remembered as such once deleted) are temporary by nature: a "join to create" voice
// room, a ticket. The channel rankings and series group them into one entry per
// plugin instance, whose dimension is the owner itself ("plugin:<bot>/<instance>",
// never a Discord ID) and whose label is the instance name (Kind GroupKind). The
// summary's rankings list a group's channels as its children.

// GroupKind is the Label kind of a group of plugin-created channels.
const GroupKind = "group"

// MaxGroupChildren is how many channels a ranking lists under a group, largest
// first; the others are summed into an "other" child.
const MaxGroupChildren = 25

const pluginOwnerPrefix = "plugin:"

// PluginOwner reports whether a resource owner is a plugin instance
// ("plugin:<bot>/<instance>").
func PluginOwner(owner string) bool { return strings.HasPrefix(owner, pluginOwnerPrefix) }

// IsGroup reports whether a channel dimension value is a group of plugin-created
// channels.
func IsGroup(dim string) bool { return PluginOwner(dim) && strings.Contains(dim, "/") }

// GroupLabel labels a group: the plugin instance's name.
func GroupLabel(group string) Label {
	name := strings.TrimPrefix(group, pluginOwnerPrefix)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return Label{Name: name, Kind: GroupKind}
}

// Owners tells which plugin instance created channels: it returns the owner
// ("plugin:<bot>/<instance>") of the ids it knows to be plugin-created and leaves the
// others out.
type Owners interface {
	Owners(ctx context.Context, server string, ids []string) map[string]string
}

// OwnersChain asks each resolver in turn: the first that knows an id wins.
type OwnersChain []Owners

func (oc OwnersChain) Owners(ctx context.Context, server string, ids []string) map[string]string {
	out := map[string]string{}
	rest := ids
	for _, o := range oc {
		if o == nil || len(rest) == 0 {
			continue
		}
		got := o.Owners(ctx, server, rest)
		next := rest[:0:0]
		for _, id := range rest {
			if owner := got[id]; PluginOwner(owner) {
				out[id] = owner
			} else {
				next = append(next, id)
			}
		}
		rest = next
	}
	return out
}

// groupable reports whether a metric's dimensions are grouped: channels, with values
// that add up (counters).
func groupable(m Metric) bool { return m.Dim == "channel" && m.Kind == state.AnalyticsCounter }

// ownerMemo resolves owners once per request (several metrics share channels).
type ownerMemo struct {
	r      *Reader
	server string
	known  map[string]string // id → owner ("" when not plugin-created)
}

func (r *Reader) ownerMemo(server string) *ownerMemo {
	return &ownerMemo{r: r, server: server, known: map[string]string{}}
}

// of returns the owners of the dims of ds (only the plugin-created ones).
func (o *ownerMemo) of(ctx context.Context, ds []state.AnalyticsDim) map[string]string {
	if o == nil || o.r.Owners == nil {
		return nil
	}
	var ask []string
	for _, d := range ds {
		if _, ok := o.known[d.Dim]; !ok && d.Dim != "" && d.Dim != OtherDim {
			o.known[d.Dim] = ""
			ask = append(ask, d.Dim)
		}
	}
	for chunk := range slices.Chunk(ask, state.MaxListLimit) {
		for id, owner := range o.r.Owners.Owners(ctx, o.server, chunk) {
			if PluginOwner(owner) {
				o.known[id] = owner
			}
		}
	}
	out := map[string]string{}
	for _, d := range ds {
		if owner := o.known[d.Dim]; owner != "" {
			out[d.Dim] = owner
		}
	}
	return out
}

// grouped is a dimension, or a group with its channels (largest first).
type grouped struct {
	Dim     string
	Value   float64
	Members []state.AnalyticsDim
}

// groupDims groups the plugin-created channels of ds (owner: id → owner) and sorts
// the entries, largest first (ties by dim). include, when set, keeps only the
// channels it accepts. The total "" and "other" are left out.
func groupDims(ds []state.AnalyticsDim, owner map[string]string, include func(id string) bool) []grouped {
	var out []grouped
	at := map[string]int{}
	for _, d := range ds {
		if d.Dim == "" || d.Dim == OtherDim || include != nil && !include(d.Dim) {
			continue
		}
		g, ok := owner[d.Dim]
		if !ok {
			out = append(out, grouped{Dim: d.Dim, Value: d.Value})
			continue
		}
		i, seen := at[g]
		if !seen {
			i = len(out)
			at[g] = i
			out = append(out, grouped{Dim: g})
		}
		out[i].Value += d.Value
		out[i].Members = append(out[i].Members, d)
	}
	for i := range out {
		sortDims(out[i].Members)
	}
	slices.SortStableFunc(out, func(a, b grouped) int {
		switch {
		case a.Value > b.Value:
			return -1
		case a.Value < b.Value:
			return 1
		}
		return strings.Compare(a.Dim, b.Dim)
	})
	return out
}

func sortDims(ds []state.AnalyticsDim) {
	slices.SortStableFunc(ds, func(a, b state.AnalyticsDim) int {
		switch {
		case a.Value > b.Value:
			return -1
		case a.Value < b.Value:
			return 1
		}
		return strings.Compare(a.Dim, b.Dim)
	})
}

// membersOf returns the channels of a group among ds.
func membersOf(ds []state.AnalyticsDim, owner map[string]string, group string) []string {
	var out []string
	for _, d := range ds {
		if owner[d.Dim] == group {
			out = append(out, d.Dim)
		}
	}
	return out
}

// ───────────────────────────────────────────────────────────── remembered labels

// StoredLabeler labels channels, roles and scheduled events from the labels the
// collector remembered (state.AnalyticsLabels): it knows the resources that no longer
// exist. As an Owners resolver, it tells which plugin instance created a channel,
// deleted or not.
type StoredLabeler struct {
	Store state.AnalyticsLabels
	// Live, when set, tells whether the labelers before this one know the server's
	// current channels and roles (the runtime's cache is loaded): a channel or role
	// they did not label is then deleted, whatever was remembered.
	Live func(server string) bool
}

// storedDims are the dimensions the collector remembers labels of.
var storedDims = map[string]bool{"channel": true, "role": true, "event": true}

func (l StoredLabeler) Labels(ctx context.Context, server string, dims []DimRef) map[string]Label {
	if l.Store == nil {
		return nil
	}
	byDim := map[string][]string{}
	for _, d := range dims {
		if storedDims[d.Kind] {
			byDim[d.Kind] = append(byDim[d.Kind], d.Value)
		}
	}
	live := l.Live != nil && len(byDim) > 0 && l.Live(server)
	out := map[string]Label{}
	for dim, ids := range byDim {
		for chunk := range slices.Chunk(ids, state.MaxListLimit) {
			ls, err := l.Store.GetLabels(ctx, server, dim, chunk)
			if err != nil {
				return out
			}
			for _, s := range ls {
				kind := s.Type
				if kind == "" {
					kind = labelKind(dim)
				}
				// A thread may be missing from the live cache while it exists (archived).
				gone := live && dim != "event" && kind != "thread"
				out[s.ID] = Label{Name: s.Name, Kind: kind, Color: s.Color, Deleted: s.Deleted || gone}
			}
		}
	}
	return out
}

func (l StoredLabeler) Owners(ctx context.Context, server string, ids []string) map[string]string {
	if l.Store == nil || len(ids) == 0 {
		return nil
	}
	out := map[string]string{}
	for chunk := range slices.Chunk(ids, state.MaxListLimit) {
		ls, err := l.Store.GetLabels(ctx, server, "channel", chunk)
		if err != nil {
			return out
		}
		for _, s := range ls {
			if PluginOwner(s.Owner) {
				out[s.ID] = s.Owner
			}
		}
	}
	return out
}
