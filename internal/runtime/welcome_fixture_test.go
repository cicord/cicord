package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Fixture edits of the examples/basic welcome plugin that do not depend on its manifest's
// YAML style (inline lists or block lists with comments, as the official plugins write them)
// nor on what its optional features list.

// requireInWelcome adds required capabilities and intents to the example's welcome
// manifest, in the inline or the block YAML style (like auditObserverWith).
func requireInWelcome(t *testing.T, dir string, caps, intents []string) {
	t.Helper()
	manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
	if len(caps) > 0 {
		replaceFirst(t, manifest, "\n  capabilities:\n", "\n  capabilities:\n    - "+strings.Join(caps, "\n    - ")+"\n")
	}
	if len(intents) == 0 {
		return
	}
	if data, _ := os.ReadFile(manifest); strings.Contains(string(data), "[ServerMembers]") {
		replaceIn(t, manifest, "[ServerMembers]", "[ServerMembers, "+strings.Join(intents, ", ")+"]")
	} else {
		replaceFirst(t, manifest, "\n  intents:\n", "\n  intents:\n    - "+strings.Join(intents, "\n    - ")+"\n")
	}
}

// grantWelcome adds grants to the welcome instance of bots/moderator/bot.yaml (the first
// plugin, the only one granted discord.dm.send).
func grantWelcome(t *testing.T, dir string, caps ...string) {
	t.Helper()
	const anchor = "        - discord.dm.send"
	replaceFirst(t, filepath.Join(dir, "bots/moderator/bot.yaml"), anchor, anchor+"\n        - "+strings.Join(caps, "\n        - "))
}

// welcomeWith requires and grants caps to welcome, with at-least-once delivery or the
// legacy (unspecified) delivery mode.
func welcomeWith(t *testing.T, dir string, atLeastOnce bool, caps ...string) {
	t.Helper()
	manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	const line = "\n  delivery: at-least-once\n"
	switch has := strings.Contains(string(data), line); {
	case atLeastOnce && !has:
		replaceFirst(t, manifest, "\nspec:\n", "\nspec:"+line)
	case !atLeastOnce && has:
		replaceFirst(t, manifest, line, "\n")
		// Versioned interop requires at-least-once delivery: the legacy mode keeps only
		// legacy (unversioned) topics.
		dropWelcomeSpec(t, dir, "interop")
	}
	requireInWelcome(t, dir, caps, nil)
	grantWelcome(t, dir, caps...)
}

// replaceWelcomeCode prepares the welcome manifest for a test plugin that replaces its
// code: it drops the manifest's own spec.privacy and spec.interop (records and
// subscriptions the test code does not handle), so the test can declare its own.
func replaceWelcomeCode(t *testing.T, dir string) {
	t.Helper()
	dropWelcomeSpec(t, dir, "privacy", "interop")
}

// dropWelcomeSpec removes top-level spec keys (and their nested lines) from the welcome
// manifest.
func dropWelcomeSpec(t *testing.T, dir string, keys ...string) {
	t.Helper()
	manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	skip := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			key, _, _ := strings.Cut(strings.TrimSpace(line), ":")
			skip = slices.Contains(keys, key)
		} else if !strings.HasPrefix(line, " ") {
			skip = false
		}
		if !skip {
			out = append(out, line)
		}
	}
	if err := os.WriteFile(manifest, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// onboardingScope is the task scope of the examples/basic onboarding plugin, which
// schedules its own stored-choices migration at start: tests that claim due tasks by hand
// leave it to the scheduler (ClaimDue's paused scopes).
const onboardingScope = "plugin:moderator/onboarding/my-community"
