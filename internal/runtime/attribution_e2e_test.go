package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// auditObserver records the attribution of every audit entry the welcome plugin
// receives, keyed by action and target.
func auditObserver(t *testing.T) *env {
	return auditObserverWith(t, nil)
}

// auditObserverWith is auditObserver with a further edit of the repository.
func auditObserverWith(t *testing.T, edit func(dir string)) *env {
	return startExampleResync(t, nil, func(dir string) {
		if edit != nil {
			edit(dir)
		}
		// Required capabilities and intents, in the inline or the block YAML style.
		manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
		replaceFirst(t, manifest, "\n  capabilities:\n", "\n  capabilities:\n    - discord.auditlog.read\n")
		if data, _ := os.ReadFile(manifest); strings.Contains(string(data), "[ServerMembers]") {
			replaceIn(t, manifest, "[ServerMembers]", "[ServerMembers, ServerModeration]")
		} else {
			replaceFirst(t, manifest, "\n  intents:\n", "\n  intents:\n    - ServerModeration\n")
		}
		replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "        - discord.dm.send", "        - discord.auditlog.read\n        - discord.dm.send")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  events: {async auditLogEntry(ctx,e){
    const key="audit:"+e.action+":"+(e.targetId||"")+":"+(e.options&&e.options.id||"")+":"+e.id;
    await ctx.kv.set(key,e.attribution.kind+"/"+e.attribution.certainty)},`)
	}, -1)
}

// replaceFirst replaces the first occurrence of old in file.
func replaceFirst(t *testing.T, file, old, new string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s: %q not found", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// attributions returns the recorded attributions of action on target.
func attributions(e *env, action, target string) []string {
	items, _, err := e.rt.Store().KV(welcomeScope).List(e.t.Context(), "audit:"+action+":"+target+":", 100, "")
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		var v string
		_ = json.Unmarshal(it.Value, &v)
		out = append(out, v)
	}
	return out
}

func (e *env) ownCall(t *testing.T) (discord.Session, context.Context) {
	h := e.rt.bot("moderator")
	if h == nil {
		t.Fatal("moderator bot missing")
	}
	scope := e.rt.operationScope(discord.Operation{ServerID: e.serverID, ActorID: h.session.Self().ID, Bot: "moderator", Revision: "test"})
	return h.session, discord.WithOperations(t.Context(), scope)
}

// CORE-R4, CORE-R5, MOD-01, MOD-02: the simulator writes Discord's own audit entries
// (overwrite creates, updates and deletes, webhook creation, role updates of a
// member), delivers gateway entries without the actor, and may deliver the entry of
// a creation before the runtime journals it. Every one of Cicord's own actions is
// still confirmed, and none raises the CORE-04 alert.
func TestOwnActionsAreConfirmedEndToEnd(t *testing.T) {
	old := auditRecheck
	auditRecheck = 300 * time.Millisecond
	t.Cleanup(func() { auditRecheck = old })
	e := auditObserver(t)
	session, ctx := e.ownCall(t)
	general, ok := e.world.ChannelByName(e.serverID, "general")
	if !ok {
		t.Fatal("no general channel")
	}
	member := e.world.CreateUser("target")
	if err := e.world.AddMember(e.serverID, member.ID); err != nil {
		t.Fatal(err)
	}
	// Overwrites: one created, then one updated and one deleted in the same PATCH.
	ows := append(append([]discord.Overwrite(nil), general.Overwrites...), discord.Overwrite{ID: member.ID, Type: discord.OverwriteMember, Deny: discord.MustPermissions("SendMessages")})
	if _, err := session.UpdateChannel(ctx, general.ID, discord.ChannelParams{Overwrites: &ows}, "lock"); err != nil {
		t.Fatal(err)
	}
	ows[len(ows)-1].Deny = discord.MustPermissions("SendMessages", "AddReactions")
	if _, err := session.UpdateChannel(ctx, general.ID, discord.ChannelParams{Overwrites: &ows}, "lock harder"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateChannel(ctx, general.ID, discord.ChannelParams{Overwrites: &general.Overwrites}, "unlock"); err != nil {
		t.Fatal(err)
	}
	// A creation whose REST response arrives after the gateway entry (CORE-R4 (1)).
	e.world.SetCreateJournalDelay(100 * time.Millisecond)
	created, err := session.CreateRole(ctx, e.serverID, discord.RoleParams{Name: ptrTo("raid-lock")}, "raid")
	if err != nil {
		t.Fatal(err)
	}
	e.world.SetCreateJournalDelay(0)
	// Two identical role changes on one member within 30 s (MOD-02).
	if err := session.AddMemberRole(ctx, e.serverID, member.ID, created.ID, "grant"); err != nil {
		t.Fatal(err)
	}
	if err := session.RemoveMemberRole(ctx, e.serverID, member.ID, created.ID, "revoke"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		action, target string
		n              int
	}{
		{"overwrite.create", general.ID, 1},
		{"overwrite.update", general.ID, 1},
		{"overwrite.delete", general.ID, 1},
		{"member.roleUpdate", member.ID, 2},
		{"role.create", created.ID, 1},
	} {
		e.waitFor(want.action+" observed", func() bool { return len(attributions(e, want.action, want.target)) >= want.n })
		for _, got := range attributions(e, want.action, want.target) {
			if got != "cicord/confirmed" {
				t.Errorf("%s on %s: %s, want cicord/confirmed", want.action, want.target, got)
			}
		}
	}
	for _, s := range e.rt.Status().Servers {
		if s.UnconfirmedAudit != nil {
			t.Fatalf("own actions raised the CORE-04 alert: %+v", s.UnconfirmedAudit)
		}
	}
	if strings.Contains(e.logs.String(), "unconfirmed audit activity") {
		t.Fatal("own actions logged as unconfirmed")
	}
}

// CORE-04: an audited action by the bot that Cicord did not emit (a leaked token)
// reaches the server status, hence the alert notifier and cicord status.
func TestUnconfirmedOwnActionRaisesTheAlert(t *testing.T) {
	old := auditRecheck
	auditRecheck = 50 * time.Millisecond
	t.Cleanup(func() { auditRecheck = old })
	e := auditObserver(t)
	role, _ := e.world.RoleByName(e.serverID, "Member")
	// Own entries the journal can never prove (an onboarding prompt, an entry without
	// a target) stay probable without an alert: their missing proof is no evidence.
	if err := e.world.AuditLog(e.serverID, e.bot.ID, 163, e.serverID); err != nil {
		t.Fatal(err)
	}
	if err := e.world.AuditLog(e.serverID, e.bot.ID, 31, ""); err != nil {
		t.Fatal(err)
	}
	e.waitFor("unprovable entries observed", func() bool {
		return len(attributions(e, "discord.163", e.serverID)) == 1 && len(attributions(e, "role.update", "")) == 1
	})
	for _, s := range e.rt.Status().Servers {
		if s.UnconfirmedAudit != nil {
			t.Fatalf("an unprovable own entry raised the alert: %+v", s.UnconfirmedAudit)
		}
	}
	if err := e.world.AuditLog(e.serverID, e.bot.ID, 31, role.ID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("role.update observed", func() bool { return len(attributions(e, "role.update", role.ID)) == 1 })
	if got := attributions(e, "role.update", role.ID)[0]; got != "cicord/probable" {
		t.Fatalf("forged entry attributed %s", got)
	}
	e.waitFor("alert in status", func() bool {
		for _, s := range e.rt.Status().Servers {
			if u := s.UnconfirmedAudit; u != nil && u.Count == 1 && u.Bot == "moderator" && u.LastAction == "role.update" {
				return true
			}
		}
		return false
	})
	for _, s := range e.rt.Status().Servers {
		if s.Health != HealthHealthy {
			t.Fatalf("the advisory changed the server health: %s", s.Health)
		}
	}
}
