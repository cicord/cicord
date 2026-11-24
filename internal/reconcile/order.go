package reconcile

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
)

// slotOrder reorders the managed items of seq (the IDs listed in desired) among the
// slots they occupy, following desired; other items keep their index. desired must
// only contain IDs present in seq. It reports whether any managed item moved.
func slotOrder(seq, desired []string) ([]string, bool) {
	want := make(map[string]bool, len(desired))
	for _, id := range desired {
		want[id] = true
	}
	out := slices.Clone(seq)
	i, changed := 0, false
	for slot, id := range seq {
		if !want[id] {
			continue
		}
		if out[slot] = desired[i]; desired[i] != id {
			changed = true
		}
		i++
	}
	return out, changed
}

// idLess orders snowflakes numerically (shorter strings are smaller).
func idLess(a, b string) int {
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return cmp.Compare(a, b)
}

// roleSeq returns the IDs of the orderable roles (not @everyone, position below
// botTop), highest first (position desc, then ID asc).
func roleSeq(roles []discord.Role, serverID string, botTop int) []string {
	rs := make([]discord.Role, 0, len(roles))
	for _, r := range roles {
		if r.ID == serverID || r.Position >= botTop {
			continue
		}
		rs = append(rs, r)
	}
	slices.SortFunc(rs, func(a, b discord.Role) int {
		if a.Position != b.Position {
			return cmp.Compare(b.Position, a.Position)
		}
		return idLess(a.ID, b.ID)
	})
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// Channel position groups, like Discord: categories among themselves, and per parent
// two buckets, text-like and voice-like channels.
const catGroup = "\x00categories"

func bucketOf(t discord.ChannelType) string {
	if t == discord.ChannelVoice || t == discord.ChannelStage {
		return "/voice"
	}
	return "/text"
}

func groupOf(c discord.Channel) string {
	if c.Type == discord.ChannelCategory {
		return catGroup
	}
	return c.ParentID + bucketOf(c.Type)
}

// channelGroups returns the live order of each group (position asc, then ID asc).
func channelGroups(chans []discord.Channel) map[string][]string {
	sorted := slices.Clone(chans)
	slices.SortFunc(sorted, func(a, b discord.Channel) int {
		if a.Position != b.Position {
			return cmp.Compare(a.Position, b.Position)
		}
		return idLess(a.ID, b.ID)
	})
	groups := map[string][]string{}
	for _, c := range sorted {
		g := groupOf(c)
		groups[g] = append(groups[g], c.ID)
	}
	return groups
}

// normalizeChannelName mimics Discord for text-like channels: lowercase, whitespace
// runs replaced by a single "-" (other types keep their name).
func normalizeChannelName(t discord.ChannelType, name string) string {
	switch t {
	case discord.ChannelText, discord.ChannelAnnouncement, discord.ChannelForum, discord.ChannelMedia:
	default:
		return name
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsSpace(r) {
			if !dash {
				b.WriteRune('-')
				dash = true
			}
			continue
		}
		dash = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// desiredRoleOrder: hierarchy (highest first), then the other declared roles in key order.
func desiredRoleOrder(sp *config.ServerSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range sp.Hierarchy {
		if _, ok := sp.Roles[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range sortedKeys(sp.Roles) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

// posLess orders by position ascending (nil after explicit positions), then key.
func posLess(pa, pb *int, ka, kb string) int {
	switch {
	case pa != nil && pb != nil && *pa != *pb:
		return cmp.Compare(*pa, *pb)
	case pa != nil && pb == nil:
		return -1
	case pa == nil && pb != nil:
		return 1
	}
	return cmp.Compare(ka, kb)
}

func desiredCategoryOrder(sp *config.ServerSpec) []string {
	keys := sortedKeys(sp.Categories)
	slices.SortFunc(keys, func(a, b string) int {
		return posLess(sp.Categories[a].Position, sp.Categories[b].Position, a, b)
	})
	return keys
}

// desiredChannelOrder returns the channel keys of one category ("" = none) and
// bucket ("/text", "/voice", or "" for both), in order.
func desiredChannelOrder(sp *config.ServerSpec, category, bucket string) []string {
	var keys []string
	for _, k := range sortedKeys(sp.Channels) {
		c := sp.Channels[k]
		if c.Category != category {
			continue
		}
		if bucket != "" {
			t, err := discord.ParseChannelType(c.Type)
			if err != nil || bucketOf(t) != bucket {
				continue
			}
		}
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return posLess(sp.Channels[a].Position, sp.Channels[b].Position, a, b)
	})
	return keys
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
