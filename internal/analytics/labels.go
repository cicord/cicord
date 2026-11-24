package analytics

import (
	"context"
	"regexp"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/state"
)

// Labels: the API answers dimensions as stored (Discord IDs, emojis, codes) and
// resolves them to display labels with a Labeler. The runtime plugs its live
// knowledge of the server (internal/api); ResourceLabeler works from the resource
// map and the configuration; FallbackLabel covers the rest.

// DimRef is a dimension value to label: Kind is the metric's dimension (Metric.Dim:
// "channel", "role", "emoji", "event", "invite", "rule", "command"…).
type DimRef struct {
	Kind  string
	Value string
}

// Labeler resolves dimension values of a server to labels. It returns the labels
// of the values it knows (keyed by value) and leaves the others out, for the next
// labeler or FallbackLabel.
type Labeler interface {
	Labels(ctx context.Context, server string, dims []DimRef) map[string]Label
}

// Labelers tries each labeler in turn: the first that knows a value wins.
type Labelers []Labeler

func (ls Labelers) Labels(ctx context.Context, server string, dims []DimRef) map[string]Label {
	out := map[string]Label{}
	rest := dims
	for _, l := range ls {
		if l == nil || len(rest) == 0 {
			continue
		}
		got := l.Labels(ctx, server, rest)
		next := rest[:0:0]
		for _, d := range rest {
			if lb, ok := got[d.Value]; ok {
				out[d.Value] = lb
			} else {
				next = append(next, d)
			}
		}
		rest = next
	}
	return out
}

// ResolveLabels labels every value of dims with l (nil: none) and FallbackLabel
// for the values l does not know. The total "" and "other" are never labeled.
func ResolveLabels(ctx context.Context, l Labeler, server string, dims []DimRef) map[string]Label {
	out := map[string]Label{}
	var todo []DimRef
	seen := map[string]bool{}
	for _, d := range dims {
		if d.Value == "" || d.Value == "other" || seen[d.Value] {
			continue
		}
		seen[d.Value] = true
		todo = append(todo, d)
	}
	if l != nil && len(todo) > 0 {
		for v, lb := range l.Labels(ctx, server, todo) {
			if seen[v] {
				out[v] = lb
			}
		}
	}
	for _, d := range todo {
		if _, ok := out[d.Value]; !ok {
			out[d.Value] = FallbackLabel(d)
		}
	}
	return out
}

// labelKind maps a metric dimension to the Label kind.
func labelKind(dim string) string {
	switch dim {
	case "channel", "role", "emoji", "event", "invite":
		return dim
	}
	return "label"
}

// idDim reports whether the values of a dimension are Discord IDs of things that
// can be deleted.
func idDim(dim string) bool {
	switch dim {
	case "channel", "role", "event", "rule":
		return true
	}
	return false
}

// FallbackLabel is the label of a value no labeler knows: a custom emoji gets its
// name and CDN image, an unknown channel, role, event or AutoMod rule ID is shown
// as deleted with the ID as name, anything else (invite codes, commands, fixed
// labels such as "<1d" or "ban") is its own name.
func FallbackLabel(d DimRef) Label {
	if d.Kind == "emoji" {
		return EmojiLabel(d.Value)
	}
	return Label{Name: d.Value, Kind: labelKind(d.Kind), Deleted: idDim(d.Kind)}
}

// ParseCustomEmoji splits a custom emoji dimension "name:id" (or "a:name:id" for
// an animated one). ok is false for a unicode emoji.
func ParseCustomEmoji(v string) (name, id string, animated, ok bool) {
	i := strings.LastIndexByte(v, ':')
	if i <= 0 || i == len(v)-1 {
		return "", "", false, false
	}
	name, id = v[:i], v[i+1:]
	for _, c := range id {
		if c < '0' || c > '9' {
			return "", "", false, false
		}
	}
	if n, ok := strings.CutPrefix(name, "a:"); ok {
		name, animated = n, true
	}
	return name, id, animated, name != ""
}

// EmojiImageURL is the Discord CDN image of a custom emoji.
func EmojiImageURL(id string, animated bool) string {
	ext := ".png"
	if animated {
		ext = ".gif"
	}
	return "https://cdn.discordapp.com/emojis/" + id + ext
}

// EmojiLabel labels an emoji dimension: a custom emoji by name with its image, a
// unicode emoji by itself.
func EmojiLabel(v string) Label {
	if name, id, animated, ok := ParseCustomEmoji(v); ok {
		return Label{Name: name, Kind: "emoji", ImageURL: EmojiImageURL(id, animated)}
	}
	return Label{Name: v, Kind: "emoji"}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ResourceLabeler labels channels, roles, custom emojis and AutoMod rules declared
// in git: the resource map gives the YAML key of a Discord ID and the server
// configuration its name. It knows nothing of resources created outside git (use
// a live labeler first for those).
type ResourceLabeler struct {
	Resources state.ResourceMap
	// Spec returns the configuration of a server (nil when unknown).
	Spec func(server string) *config.ServerSpec
}

func (l ResourceLabeler) Labels(ctx context.Context, server string, dims []DimRef) map[string]Label {
	if l.Resources == nil {
		return nil
	}
	rs, err := l.Resources.List(ctx, server)
	if err != nil || len(rs) == 0 {
		return nil
	}
	byID := make(map[string]state.Resource, len(rs))
	for _, r := range rs {
		if _, dup := byID[r.ID]; !dup {
			byID[r.ID] = r
		}
	}
	var spec *config.ServerSpec
	if l.Spec != nil {
		spec = l.Spec(server)
	}
	if spec == nil {
		spec = &config.ServerSpec{}
	}
	out := map[string]Label{}
	for _, d := range dims {
		id := d.Value
		var animated, custom bool
		if d.Kind == "emoji" {
			if _, id, animated, custom = ParseCustomEmoji(d.Value); !custom {
				continue
			}
		}
		r, ok := byID[id]
		if !ok {
			continue
		}
		if lb, ok := resourceLabel(spec, d.Kind, r, animated); ok {
			out[d.Value] = lb
		}
	}
	return out
}

func resourceLabel(spec *config.ServerSpec, dim string, r state.Resource, animated bool) (Label, bool) {
	or := func(name string) string {
		if name != "" {
			return name
		}
		return r.Key
	}
	switch {
	case dim == "channel" && r.Kind == state.KindChannel:
		c := spec.Channels[r.Key]
		kind := "channel"
		if c.Type == "voice" || c.Type == "stage" {
			kind = "voice"
		}
		return Label{Name: or(c.Name), Kind: kind}, true
	case dim == "channel" && r.Kind == state.KindCategory:
		return Label{Name: or(spec.Categories[r.Key].Name), Kind: "channel"}, true
	case dim == "role" && r.Kind == state.KindRole:
		rs := spec.Roles[r.Key]
		lb := Label{Name: or(rs.Name), Kind: "role"}
		if hexColor.MatchString(rs.Color) {
			lb.Color = strings.ToLower(rs.Color)
		}
		return lb, true
	case dim == "emoji" && r.Kind == state.KindEmoji:
		return Label{Name: or(spec.Emojis[r.Key].Name), Kind: "emoji", ImageURL: EmojiImageURL(r.ID, animated)}, true
	case dim == "rule" && r.Kind == state.KindAutoMod:
		return Label{Name: or(spec.AutoMod[r.Key].Name), Kind: "label"}, true
	}
	return Label{}, false
}
