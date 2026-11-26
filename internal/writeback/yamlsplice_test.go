package writeback

import (
	"strings"
	"testing"
)

// A bot.yaml written by hand: flow sequences and maps, a flow sequence wrapped on two
// lines, aligned inline comments, blank lines between entries, an emoji.
const handBot = `apiVersion: cicord.dev/v1alpha1
kind: Bot
metadata:
  name: cicord
spec:
  presence:
    activity: { type: watching, name: the config repo }
  servers: [cicord]
  plugins:
    # Greets new members.
    - name: welcome
      source: cicord/welcome
      version: ^0.1.0
      grants: [discord.messages.send, discord.channels.read, discord.members.read,
               discord.roles.assign, discord.dm.send, portal.links, storage.kv]
      config:
        channel: general
        autoRole: member
        dmOnboarding: false       # no DM: no onboarding page

    # Moderation.
    - name: moderation
      source: cicord/moderation
      version: "^0.1.1"
      grants: [ discord.messages.send, storage.kv ]
      config:
        escalation:
          - { warnings: 3, action: timeout, durationMinutes: 60 }
          - { warnings: 5, action: kick }
        log: { joins: false, leaves: false }

    - name: voice-rooms
      source: cicord/voice-rooms
      version: ^0.1.0
      grants:
        - discord.channels.read
        - storage.kv
      config:
        hubs:
          - channel: create-room
            name: "🔊 {member}'s channel"
            allowedRoles: [maintainer]   # maintainers only for now

    - name: polls
      source: cicord/polls
      config:
        channels: [general]
`

func plugin(name string, rest ...PathElem) []PathElem {
	return append([]PathElem{Key("spec"), Key("plugins"), Match("name", name)}, rest...)
}

// patch applies PatchYAML and checks the result is exactly src with old replaced by new.
func patch(t *testing.T, src string, path []PathElem, value any, old, new string) string {
	t.Helper()
	out, err := PatchYAML([]byte(src), path, value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, old) {
		t.Fatalf("test: %q not in the source", old)
	}
	if want := strings.Replace(src, old, new, 1); string(out) != want {
		t.Fatalf("PatchYAML %v:\n%s\nwant:\n%s", path, out, want)
	}
	return string(out)
}

func TestPatchYAMLKeepsTheRestOfTheDocument(t *testing.T) {
	// Scalars keep their quoting.
	patch(t, handBot, plugin("welcome", Key("version")), "^0.2.0", "version: ^0.1.0\n      grants: [discord.messages.send", "version: ^0.2.0\n      grants: [discord.messages.send")
	patch(t, handBot, plugin("moderation", Key("version")), "^0.2.0", `version: "^0.1.1"`, `version: "^0.2.0"`)

	// A wrapped flow sequence stays wrapped and aligned.
	patch(t, handBot, plugin("welcome", Key("grants")),
		[]string{"discord.messages.send", "discord.channels.read", "discord.members.read", "discord.roles.assign", "discord.dm.send", "portal.links", "storage.kv", "events.subscribe"},
		"portal.links, storage.kv]", "portal.links, storage.kv,\n               events.subscribe]")
	patch(t, handBot, plugin("welcome", Key("grants")), []string{"storage.kv", "discord.messages.send"},
		"[discord.messages.send, discord.channels.read, discord.members.read,\n               discord.roles.assign, discord.dm.send, portal.links, storage.kv]",
		"[storage.kv, discord.messages.send]")
	// Inner padding of a flow sequence.
	patch(t, handBot, plugin("moderation", Key("grants")), []string{"storage.kv", "scheduler"},
		"[ discord.messages.send, storage.kv ]", "[ storage.kv, scheduler ]")
	// A block sequence stays a block sequence.
	patch(t, handBot, plugin("voice-rooms", Key("grants")), []string{"discord.channels.read", "discord.voice.move", "storage.kv"},
		"        - discord.channels.read\n        - storage.kv\n", "        - discord.channels.read\n        - discord.voice.move\n        - storage.kv\n")
	// Removed and added keys touch their own line only.
	patch(t, handBot, plugin("moderation", Key("grants")), nil, "      grants: [ discord.messages.send, storage.kv ]\n", "")
	patch(t, handBot, plugin("voice-rooms", Key("grants")), nil, "      grants:\n        - discord.channels.read\n        - storage.kv\n", "")
	patch(t, handBot, plugin("polls", Key("grants")), []string{"storage.kv"}, "source: cicord/polls\n", "source: cicord/polls\n      grants: [storage.kv]\n")
	patch(t, handBot, plugin("polls", Key("version")), "^0.2.0", "source: cicord/polls\n", "source: cicord/polls\n      version: ^0.2.0\n")

	// Two edits in a row, as an update does.
	out := patch(t, handBot, plugin("welcome", Key("version")), "^0.2.0", "version: ^0.1.0", "version: ^0.2.0")
	patch(t, out, plugin("moderation", Key("grants")), []string{"storage.kv"}, "[ discord.messages.send, storage.kv ]", "[ storage.kv ]")
}

func TestPatchYAMLFallsBack(t *testing.T) {
	// A value that is not a string (list): the whole document is re-encoded, like SetYAML.
	for _, c := range []struct {
		src   string
		path  []PathElem
		value any
	}{
		{handBot, plugin("welcome", Key("config")), map[string]any{"channel": "x"}},
		{"a: &x [b]\nc: *x\n", []PathElem{Key("a")}, []string{"d"}},
		{"a: [b, # c\n  d]\n", []PathElem{Key("a")}, []string{"e"}},
		{"a: |\n  text\n", []PathElem{Key("a")}, "other"},
	} {
		want, err := SetYAML([]byte(c.src), c.path, c.value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := PatchYAML([]byte(c.src), c.path, c.value)
		if err != nil || string(got) != string(want) {
			t.Errorf("PatchYAML(%q) = %q, %v; want SetYAML's %q", c.src, got, err, want)
		}
	}
	if _, err := PatchYAML([]byte(handBot), plugin("nope", Key("grants")), []string{"a"}); err == nil {
		t.Error("an unknown instance must fail like SetYAML")
	}
}

func TestPatchYAMLWrapsALongFlowSequence(t *testing.T) {
	src := "spec:\n  grants: [discord.messages.send, discord.channels.read, storage.kv]  # c\n  other: { a: 1 }\n"
	patch(t, src, []PathElem{Key("spec"), Key("grants")},
		[]string{"discord.messages.send", "discord.channels.read", "storage.kv", "scheduler", "events.subscribe", "discord.messages.read", "portal.routes"},
		"[discord.messages.send, discord.channels.read, storage.kv]",
		"[discord.messages.send, discord.channels.read, storage.kv, scheduler, events.subscribe,\n           discord.messages.read, portal.routes]")
}
