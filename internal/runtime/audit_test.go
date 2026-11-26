package runtime

import (
	"encoding/json"
	"github.com/cicord/cicord/internal/plugin"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func TestAuditAttributionRequiresActorTargetTypeAndTime(t *testing.T) {
	e := startExample(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := strconv.FormatUint(uint64(now.UnixMilli()-1420070400000)<<22, 10)
	self := e.bot
	entry := discord.AuditLogEntryEvent{ServerID: e.serverID, ID: id, UserID: self.ID, Actor: &self, TargetID: "123456", ActionType: 31, Reason: "cicord: reconcile example"}
	if a := e.rt.attribute(t.Context(), entry); a.Kind != "cicord" || a.Certainty != "probable" {
		t.Fatalf("unconfirmed own bot: %+v", a)
	}
	origin := e.rt.operationScope(discord.Operation{ServerID: e.serverID, ActorID: self.ID, Bot: "moderator", Instance: "probe", Plugin: "observer"})
	origin.Emitted(t.Context(), "UpdateRole", entry.TargetID, now.Add(-31*time.Second))
	if a := e.rt.attribute(t.Context(), entry); a.Certainty != "probable" {
		t.Fatalf("old operation: %+v", a)
	}
	origin.Emitted(t.Context(), "UpdateRole", entry.TargetID, now)
	if a := e.rt.attribute(t.Context(), entry); a.Certainty != "confirmed" || a.Operation == nil || a.Operation.Instance != "probe" {
		t.Fatalf("unique operation: %+v", a)
	}
	human := e.world.CreateUser("human")
	other := entry
	other.UserID = human.ID
	other.Actor = &human
	if a := e.rt.attribute(t.Context(), other); a.Kind != "human" || a.Operation != nil {
		t.Fatalf("copied reason: %+v", a)
	}
	other.ActionType = 72
	if a := e.rt.attribute(t.Context(), other); a.Certainty != "probable" {
		t.Fatal(a)
	}
	other.UserID = ""
	if a := e.rt.attribute(t.Context(), other); a.Certainty != "unknown" {
		t.Fatal(a)
	}
	otherEntry := entry
	otherEntry.ID = strconv.FormatUint(uint64(now.UnixMilli()-1420070400000)<<22|1, 10)
	if a := e.rt.attribute(t.Context(), otherEntry); a.Certainty == "confirmed" {
		t.Fatal("one operation confirmed two different audit entries")
	}
	origin.Emitted(t.Context(), "UpdateRole", entry.TargetID, now.Add(time.Millisecond))
	if a := e.rt.attribute(t.Context(), entry); a.Certainty != "confirmed" {
		t.Fatal("re-reading an already bound entry lost its proof")
	}
	if a := e.rt.attribute(t.Context(), otherEntry); a.Certainty != "probable" {
		t.Fatalf("ambiguous operations: %+v", a)
	}
}

func TestAuditSDKReadsServerEvidence(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		replaceIn(t, filepath.Join(dir, "plugins/welcome/plugin.yaml"), "    - portal.links", "    - discord.auditlog.read\n    - portal.routes\n    - portal.links")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/plugin.yaml"), "[ServerMembers]", "[ServerMembers, ServerModeration]")
		replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "        - discord.dm.send", "        - discord.auditlog.read\n        - portal.routes\n        - discord.dm.send")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  routes: {"GET /audit":{access:"admin",async handler(ctx){
    const entries=await ctx.server.auditLog({action:"role.update",limit:5});return {entries};
  }}},events:{async auditLogEntry(ctx,e){if(e.action==="role.update")await ctx.kv.set("actor",e.attribution.kind)},`)
	}, -1)
	role, _ := e.world.RoleByName(e.serverID, "Member")
	perms := role.Permissions | discord.MustPermissions("ManageMessages")
	if _, err := e.world.AdminUpdateRole(e.serverID, role.ID, discord.RoleParams{Permissions: &perms}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("audit gateway handler", func() bool {
		v, err := e.rt.Store().KV("plugin:moderator/welcome/my-community").Get(t.Context(), "actor")
		return err == nil && string(v) == `"human"`
	})
	resp, _, err := e.rt.HandleRoute(t.Context(), "my-community", "moderator", "welcome", "GET", "/audit", plugin.RouteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Entries []struct {
			Action      string
			TargetID    string
			Changes     []struct{ Key string }
			Attribution discord.Attribution
		}
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) == 0 {
		t.Fatal("no audit entries")
	}
	first := out.Entries[0]
	if first.Action != "role.update" || first.TargetID != role.ID || first.Attribution.Kind != "human" || first.Attribution.Certainty != "confirmed" || len(first.Changes) == 0 || first.Changes[0].Key != "permissions" {
		t.Fatalf("wrong evidence: %s", resp.Body)
	}
}

func TestSDKStructureEventTracksManagementAndPrevious(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  events: {
     async roleUpdate(ctx,e){if(e.role.key==="member")await ctx.kv.set("changed",{previous:e.previous?.name,current:e.role.name,managed:e.managed,willHeal:e.willHeal});}
   ,`)
	}, -1)
	role, ok := e.world.RoleByName(e.serverID, "Member")
	if !ok {
		t.Fatal("missing role")
	}
	name := "Renamed by staff"
	if _, err := e.world.AdminUpdateRole(e.serverID, role.ID, discord.RoleParams{Name: &name}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("structural change from real plugin", func() bool {
		b, err := e.rt.Store().KV("plugin:moderator/welcome/my-community").Get(t.Context(), "changed")
		if err != nil {
			return false
		}
		var value struct {
			Previous, Current string
			Managed           bool
		}
		return json.Unmarshal(b, &value) == nil && value.Previous == "Member" && value.Current == name && value.Managed
	})
}
