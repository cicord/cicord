package plugin

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// accessEnv: roles with positions and permissions, an owner and spec.access.
func accessEnv(owner string) func(*Env) {
	return func(env *Env) {
		r := newFakeResolver()
		r.roles["10"] = discord.Role{ID: "10", Name: "@everyone", Permissions: discord.MustPermissions("ViewChannel", "SendMessages")}
		r.roles["200"] = discord.Role{ID: "200", Name: "Membre", Position: 1, Mentionable: true}
		r.roles["201"] = discord.Role{ID: "201", Name: "Modo", Position: 5, Permissions: discord.MustPermissions("ModerateMembers", "KickMembers")}
		r.roles["202"] = discord.Role{ID: "202", Name: "Admin", Position: 9, Permissions: discord.MustPermissions("Administrator")}
		env.Resolver = r
		env.OwnerID = func() string { return owner }
		env.Access = Access{AdminUsers: []string{"77"}, ModeratorRoles: []string{"moderator"}}
	}
}

const accessPlugin = `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    who: command({ description: "x", options: { r: { type: "role", description: "r" } }, async run(ctx, it) {
      const m = it.member;
      ctx.log.info("who", {
        owner: ctx.server.ownerId ?? "none",
        access: m.access,
        permissions: [...m.permissions],
        positions: m.roles.map((r) => r.position ?? -1),
        mentionable: m.roles.map((r) => r.mentionable ?? null),
        option: it.options.r ? [it.options.r.position ?? -1, it.options.r.mentionable ?? null] : [],
      });
    } }),
  },
});`

func whoLog(t *testing.T, h *harness, roles []string, res discord.Resolved, opts []discord.InteractionOption) map[string]any {
	t.Helper()
	it := discord.Interaction{ID: "it", Token: "tok", ApplicationID: "app", Type: discord.InteractionCommand,
		ServerID: "10", ChannelID: "100", User: discord.User{ID: "42", Username: "alice"},
		Member:      &discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, RoleIDs: roles},
		CommandName: "who", Options: opts, Resolved: res}
	if err := h.inst.HandleInteraction(context.Background(), it, "command", "who"); err != nil {
		t.Fatal(err)
	}
	got := h.logs.Find("who")
	if len(got) == 0 {
		t.Fatal("no log")
	}
	return got[len(got)-1].Attrs
}

func TestMemberPermissionsAccessAndRolePositions(t *testing.T) {
	h := start(t, accessPlugin, accessEnv("99"))

	a := whoLog(t, h, []string{"200", "201", "999"}, discord.Resolved{}, nil)
	if a["owner"] != "99" || a["access"] != "moderator" {
		t.Errorf("owner/access = %v / %v", a["owner"], a["access"])
	}
	// Unknown role 999: no position (-1 here), no mentionable, no permissions.
	if !reflect.DeepEqual(a["positions"], []any{1.0, 5.0, -1.0}) || !reflect.DeepEqual(a["mentionable"], []any{true, false, nil}) {
		t.Errorf("roles = %v %v", a["positions"], a["mentionable"])
	}
	if !reflect.DeepEqual(a["permissions"], []any{"KickMembers", "ModerateMembers", "SendMessages", "ViewChannel"}) {
		t.Errorf("permissions = %v", a["permissions"])
	}

	// Administrator gives every permission; a role option resolved by Discord has its position.
	opt := []discord.InteractionOption{{Name: "r", Type: discord.OptionRole, Value: "300"}}
	res := discord.Resolved{Roles: map[string]discord.Role{"300": {ID: "300", Name: "x", Position: 3, Mentionable: true}}}
	a = whoLog(t, h, []string{"202"}, res, opt)
	if perms := a["permissions"].([]any); len(perms) != len(allPermissions.Names()) || a["access"] != "member" {
		t.Errorf("admin role: %d permissions, access %v", len(perms), a["access"])
	}
	if !reflect.DeepEqual(a["option"], []any{3.0, true}) {
		t.Errorf("role option = %v", a["option"])
	}
}

func TestOwnerAndAdminAccess(t *testing.T) {
	h := start(t, accessPlugin, accessEnv("42")) // the invoker owns the server
	a := whoLog(t, h, nil, discord.Resolved{}, nil)
	if a["access"] != "admin" || len(a["permissions"].([]any)) != len(allPermissions.Names()) {
		t.Errorf("owner: access %v, %d permissions", a["access"], len(a["permissions"].([]any)))
	}

	h = start(t, accessPlugin, func(env *Env) {
		accessEnv("")(env)
		env.Access.AdminUsers = []string{"42"}
	})
	a = whoLog(t, h, nil, discord.Resolved{}, nil)
	if a["owner"] != "none" || a["access"] != "admin" {
		t.Errorf("unknown owner, admin user: %v %v", a["owner"], a["access"])
	}
	if !reflect.DeepEqual(a["permissions"], []any{"SendMessages", "ViewChannel"}) {
		t.Errorf("permissions = %v", a["permissions"])
	}
}

func TestAllowedMentionIDLists(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    ping: command({ description: "x", async run(ctx, it) {
      const role = ctx.server.role("moderator");
      await ctx.server.channel("general").send({ content: role.mention, allowedMentions: { roles: [role], users: [] } });
      await ctx.server.channel("general").send({ content: "x", allowedMentions: { users: ["42", it.user], roles: true, repliedUser: true } });
      // In JS a type is either parsed (true) or listed, never both: the ids and the limit
      // are what can be wrong.
      for (const bad of [{ roles: new Array(101).fill("1") }, { roles: ["moderator"] }, { users: "42" }]) {
        try {
          await ctx.server.channel("general").send({ content: "x", allowedMentions: bad });
          ctx.log.info("accepted");
        } catch (e) {
          ctx.log.info("refused", { message: String(e.message) });
        }
      }
      await it.reply({ content: "ok", allowedMentions: { users: [it.user.id] } });
    } }),
  },
});`, nil)
	if err := h.command("ping", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	sends := h.dc.Calls("SendMessage")
	if len(sends) != 2 {
		t.Fatalf("%d messages sent, want 2 (the invalid ones are refused)", len(sends))
	}
	m1 := sends[0].Args[1].(discord.MessageCreate).AllowedMentions
	if !reflect.DeepEqual(*m1, discord.AllowedMentions{RoleIDs: []string{"201"}, UserIDs: []string{}}) {
		t.Errorf("first = %+v", *m1)
	}
	m2 := sends[1].Args[1].(discord.MessageCreate).AllowedMentions
	if !reflect.DeepEqual(*m2, discord.AllowedMentions{UserIDs: []string{"42", "42"}, Roles: true, RepliedUser: true}) {
		t.Errorf("second = %+v", *m2)
	}
	refused := h.logs.Find("refused")
	if len(refused) != 3 || len(h.logs.Find("accepted")) != 0 {
		t.Fatalf("refused = %v", refused)
	}
	for k, want := range []string{"at most 100", "snowflake", "boolean or an array"} {
		if msg, _ := refused[k].Attrs["message"].(string); !strings.Contains(msg, want) {
			t.Errorf("refusal %d = %q, want %q", k, msg, want)
		}
	}
	if am := h.responses()[0].Data.AllowedMentions; am == nil || !reflect.DeepEqual(am.UserIDs, []string{"42"}) || am.Users {
		t.Errorf("reply allowed mentions = %+v", am)
	}
}
