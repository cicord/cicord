package plugin

import (
	"context"
	"github.com/cicord/cicord/internal/discord"
	"strings"
	"testing"
)

func TestMemberOverwriteReadsLiveGuildRow(t *testing.T) {
	h := start(t, `
import {definePlugin} from "@cicord/sdk";
export default definePlugin({routes:{"GET /row":{access:"admin",async handler(ctx,r){return await ctx.server.channel("general").memberOverwrite(r.query.user) ?? null;}}}});`, nil)
	set := func(server string, typ discord.ChannelType, rows []discord.Overwrite) {
		h.dc.mu.Lock()
		h.dc.channels["100"] = discord.Channel{ID: "100", ServerID: server, Type: typ, Overwrites: rows}
		h.dc.mu.Unlock()
	}
	get := func(user string) (string, error) {
		r, err := h.inst.HandleRoute(t.Context(), "GET", "/row", RouteRequest{Query: map[string]string{"user": user}})
		return string(r.Body), err
	}
	set("10", discord.ChannelVoice, []discord.Overwrite{{Type: discord.OverwriteRole, ID: "42", Allow: discord.MustPermissions("Administrator")}, {Type: discord.OverwriteMember, ID: "42", Allow: discord.MustPermissions("Connect"), Deny: discord.MustPermissions("Speak")}})
	if v, err := get("42"); err != nil || v != `{"allow":["Connect"],"deny":["Speak"]}` {
		t.Fatal(v, err)
	}
	if v, err := get("43"); err != nil || v != "null" {
		t.Fatal(v, err)
	}
	set("10", discord.ChannelVoice, nil)
	if v, err := get("42"); err != nil || v != "null" {
		t.Fatal(v, err)
	}
	set("11", discord.ChannelVoice, nil)
	if _, err := get("42"); err == nil {
		t.Fatal("cross guild channel permitted")
	}
	set("", discord.ChannelDM, nil)
	if _, err := get("42"); err == nil {
		t.Fatal("DM permitted")
	}
	h.dc.mu.Lock()
	delete(h.dc.channels, "100")
	h.dc.mu.Unlock()
	if _, err := get("42"); err == nil {
		t.Fatal("missing channel permitted")
	}
}
func TestMemberOverwriteRequiresGrant(t *testing.T) {
	h := start(t, `import {definePlugin} from "@cicord/sdk";export default definePlugin({events:{async messageCreate(ctx,m){await m.channel.memberOverwrite("42");}}});`, func(e *Env) { e.Grants = nil })
	err := h.inst.HandleEvent(context.Background(), discord.MessageCreateEvent{Message: discord.Message{ServerID: "10", ChannelID: "100"}})
	if err == nil || !strings.Contains(err.Error(), capChannelsRead) || len(h.dc.Calls("GetChannel")) != 0 {
		t.Fatalf("missing grant: %v", err)
	}
}
