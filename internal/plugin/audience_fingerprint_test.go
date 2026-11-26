package plugin

import (
	"github.com/cicord/cicord/internal/discord"
	"testing"
)

func TestArchivedAudienceFingerprint(t *testing.T) {
	ch := discord.Channel{ID: "10", ServerID: "1", Type: discord.ChannelText}
	roles := []discord.Role{{ID: "1", Permissions: contentRead}, {ID: "5"}}
	p := discord.NewAudience(ch, roles)
	before := audienceFingerprint("2", p)
	if before == "" || before == audienceFingerprint("3", p) {
		t.Fatal("owner missing from policy version")
	}
	ch.Overwrites = []discord.Overwrite{{ID: "5", Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}
	if audienceFingerprint("2", discord.NewAudience(ch, roles)) != before {
		t.Fatal("write-only change invalidated read policy")
	}
	ch.Overwrites[0].Allow = discord.MustPermissions("ReadMessageHistory")
	if audienceFingerprint("2", discord.NewAudience(ch, roles)) == before {
		t.Fatal("changed read overwrite went unnoticed")
	}
}
