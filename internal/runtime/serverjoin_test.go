package runtime

import (
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// TestCommandsReRegisteredOnServerJoin: after the bot is removed from a server of its spec
// and invited again, the runtime registers the slash commands again (the set did not
// change, so without the join event the command hash would skip it).
func TestCommandsReRegisteredOnServerJoin(t *testing.T) {
	e := startExample(t)
	w := e.world
	registered := func() int {
		n := 0
		for _, c := range w.Calls(e.bot.ID) {
			if c.Method == "SetServerCommands" && c.ServerID == e.serverID && c.Err == nil {
				n++
			}
		}
		return n
	}
	e.waitFor("first registration", func() bool { return registered() >= 1 })
	before := registered()

	if err := w.AdminKickMember(e.serverID, e.bot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddBotToServer(e.serverID, e.bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	e.waitFor("commands registered again", func() bool { return registered() == before+1 })
	if len(w.Commands(e.appID, e.serverID)) == 0 {
		t.Error("no commands after the re-invite")
	}
}
