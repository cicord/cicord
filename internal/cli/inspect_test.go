package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
)

// TestInspect runs `cicord inspect` on the setup simulator's server with a bot invited
// through the inspection link (View Channels only), the channel list omitting what the bot
// cannot view like Discord: the report says what the bot sees, what it could not read and
// what applying changes needs; the plan is empty and the bot made no mutating call. A
// later `cicord plan` in the generated project shows no changes.
func TestInspect(t *testing.T) {
	w := sim.NewWorld(sim.WithHiddenChannels())
	env, err := setup.NewSimEnv(w)
	if err != nil {
		t.Fatal(err)
	}
	srv := env.Servers()[0]
	tok, _ := env.CreateBot("Cicord")
	if err := env.InviteWith(tok, srv.ID, setup.InspectPermissions, false); err != nil {
		t.Fatal(err)
	}
	bot := w.Bots()[0]
	w.ResetCalls()
	useSim(t, w, map[string]string{"DISCORD_TOKEN": tok})
	dir := filepath.Join(t.TempDir(), "pixel-pals")

	code, out, errOut := run(t, "inspect", srv.ID, dir)
	if code != 0 {
		t.Fatalf("inspect = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		`Inspected "Pixel Pals"`,
		"Bot permissions: ViewChannel from its role",
		"Visible to the bot: 1 category, 4 channel(s).",
		"(no category): #general",
		"Community: #rules, #announcements, Lounge (voice)",
		"Discord omits the channels a bot cannot view from the channel list",
		"AutoMod rules: needs ManageServer",
		"Welcome screen: needs ManageServer",
		"ManageChannels: Edit categories and channels",
		"needed by categories.community, channels.announcements, channels.general and 2 more",
		"Administrator: These roles grant Administrator",
		`add "@bot" to its overwrites`,
		"Invite link with exactly these permissions: https://discord.com/oauth2/authorize?",
		"No changes. Discord matches the configuration.",
		"Nothing was changed on Discord:",
		"0 write calls.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "managed by an integration") {
		t.Errorf("stderr: %s", errOut)
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 {
		t.Fatalf("inspect changed Discord: %+v", calls)
	}
	for _, f := range []string{"cicord.yaml", "servers/pixel-pals.yaml", "bots/cicord/bot.yaml", ".cicord/state.db"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if y := readFile(t, filepath.Join(dir, "servers", "pixel-pals.yaml")); strings.Contains(y, "automod:") || strings.Contains(y, "mod-log:") {
		t.Errorf("servers/pixel-pals.yaml:\n%s", y)
	}

	// The generated project plans empty with the regular command, still without a write.
	code, out, errOut = run(t, "plan", dir, "--detailed-exitcode")
	if code != 0 || !strings.Contains(out, "No changes") {
		t.Fatalf("plan after inspect = %d\n%s\n%s", code, out, errOut)
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}

	// A project is never overwritten.
	if code, _, errOut := run(t, "inspect", srv.ID, dir); code == 0 || !strings.Contains(errOut, "already contains a cicord.yaml") {
		t.Errorf("inspect into a project = %d: %s", code, errOut)
	}

	// After the upgrade (Administrator), everything is visible and nothing is missing.
	if err := env.SetBotPermissions(tok, srv.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, "inspect", srv.ID, filepath.Join(t.TempDir(), "again"))
	if code != 0 {
		t.Fatalf("inspect with Administrator = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"Bot permissions: Administrator", "Visible to the bot: 2 categories, 5 channel(s).",
		"Staff [private]: #mod-log [private]", "Hidden from the bot: none (Administrator", "Not read: nothing",
		"the bot already has every permission"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}
