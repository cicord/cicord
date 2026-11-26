package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

const roleMembersPlugin = `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    staff: command({ description: "x", async run(ctx, it) {
      const role = ctx.server.role("moderator");
      const all = await role.members();
      const first = await role.members({ limit: 1 });
      const after = await role.members({ after: "9" });
      const byId = await ctx.server.role({ id: "201" }).members({ limit: 1000 });
      const errs: string[] = [];
      for (const f of [() => role.members({ limit: 0 }), () => role.members({ limit: 1001 }), () => role.members({ limit: 1.5 }),
                       () => ctx.server.role({ id: "10" }).members(), () => role.members({ after: "x" })]) {
        try { await f(); errs.push("no error"); } catch (e) { errs.push((e as Error).name + ": " + (e as Error).message); }
      }
      const m = all[0];
      await it.reply(JSON.stringify({
        all: all.map((m) => m.id), first: first.map((m) => m.id), after: after.map((m) => m.id), byId: byId.length,
        member: [m.displayName, m.hasRole("moderator"), m.roles.map((r) => r.key ?? r.id), typeof m.roles[0].members],
        frozen: Object.isFrozen(all), errs,
        json: JSON.stringify(role),
      }));
    } }),
  },
});`

func roleMembersEnv(env *Env) { env.ServerMembers = true }

func staffReply(t *testing.T, h *harness) map[string]any {
	t.Helper()
	if err := h.command("staff", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &got); err != nil {
		t.Fatal(err, contentOf(h.responses()[0]))
	}
	return got
}

func addMembers(h *harness) {
	for _, m := range []discord.Member{
		{ServerID: "10", User: discord.User{ID: "100", Username: "zed"}, RoleIDs: []string{"201"}},
		{ServerID: "10", User: discord.User{ID: "77", Username: "bob"}, RoleIDs: []string{"200", "201"}},
		{ServerID: "10", User: discord.User{ID: "9", Username: "ann"}, Nick: "Annie", RoleIDs: []string{"201"}},
		{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, RoleIDs: []string{"200"}},
	} {
		h.dc.members[m.User.ID] = m
	}
}

func TestServerMembersMetadata(t *testing.T) {
	const src = `import {command,definePlugin} from "@cicord/sdk"; export default definePlugin({commands:{staff:command({description:"list", async run(ctx,it){
 const members=await ctx.server.members({after:"9",limit:1}); const m=members[0]; await it.reply(JSON.stringify({id:m.id,created:m.user.createdAt.toISOString(),flags:m.user.flags,memberFlags:m.flags,pending:m.pending,locale:ctx.server.preferredLocale}));
 }})}})`
	h := start(t, src, func(e *Env) { e.ServerMembers = true; e.ServerPreferredLocale = "fr" })
	addMembers(h)
	m := h.dc.members["42"]
	m.User.PublicFlags = (1 << 16) | (1 << 20)
	m.Flags = 5
	pending := false
	m.Pending = &pending
	h.dc.members["42"] = m
	got := staffReply(t, h)
	want := map[string]any{"id": "42", "created": "2015-01-01T00:00:00.000Z", "flags": []any{"verifiedBot"}, "memberFlags": []any{"didRejoin", "bypassesVerification"}, "pending": false, "locale": "fr"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func TestRoleMembers(t *testing.T) {
	h := start(t, roleMembersPlugin, roleMembersEnv)
	addMembers(h)
	got := staffReply(t, h)
	want := map[string]any{
		// Numeric user ID order (9 < 77 < 100), only the holders of the role.
		"all":    []any{"9", "77", "100"},
		"first":  []any{"9"},
		"after":  []any{"77", "100"},
		"byId":   float64(3),
		"member": []any{"Annie", true, []any{"moderator"}, "function"},
		"frozen": true,
		"json":   `{"id":"201","key":"moderator","name":"Moderator","mention":"<@&201>","position":0,"managed":false,"declaredMembers":false,"mentionable":false,"permissions":"0"}`,
	}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}
	errs, _ := got["errs"].([]any)
	wantErrs := []string{
		"TypeError: role.members(): limit must be an integer between 1 and 1000",
		"TypeError: role.members(): limit must be an integer between 1 and 1000",
		"TypeError: role.members(): limit must be an integer between 1 and 1000",
		"TypeError: role.members(): every member has the @everyone role",
		`TypeError: invalid user id "x"`,
	}
	if len(errs) != len(wantErrs) {
		t.Fatalf("errs = %q", errs)
	}
	for i, w := range wantErrs {
		if s, _ := errs[i].(string); !strings.HasPrefix(s, w) {
			t.Errorf("errs[%d] = %q, want %q…", i, s, w)
		}
	}
	// Without Env.Members, every call lists the server (4 successful calls).
	if n := len(h.dc.Calls("ListMembers")); n != 4 {
		t.Errorf("ListMembers calls = %d", n)
	}
}

func TestRoleMembersUsesEnvMembers(t *testing.T) {
	var calls atomic.Int32
	h := start(t, roleMembersPlugin, func(env *Env) {
		env.ServerMembers = true
		env.Members = func(context.Context) ([]discord.Member, error) {
			calls.Add(1)
			return []discord.Member{{ServerID: "10", User: discord.User{ID: "5", Username: "eve"}, RoleIDs: []string{"201"}}}, nil
		}
	})
	got := staffReply(t, h)
	if !reflect.DeepEqual(got["all"], []any{"5"}) || calls.Load() != 4 || len(h.dc.Calls("ListMembers")) != 0 {
		t.Fatalf("all = %v, Env.Members calls = %d, ListMembers calls = %d", got["all"], calls.Load(), len(h.dc.Calls("ListMembers")))
	}
}

const roleMembersErrPlugin = `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    staff: command({ description: "x", async run(ctx, it) {
      try { await ctx.server.role("moderator").members(); await it.reply("ok"); }
      catch (e) { await it.reply((e as Error).name + ": " + (e as Error).message); }
    } }),
  },
});`

func TestRoleMembersRequirements(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Env)
		want string
	}{
		{"intent", nil, "Error: role.members() needs the ServerMembers intent: declare it in plugin.yaml (spec.intents)"},
		{"capability", func(env *Env) {
			env.ServerMembers = true
			env.Grants = slices.DeleteFunc(slices.Clone(allGrants), func(c string) bool { return c == capMembersRead })
		}, "CapabilityError: "},
		{"discord", func(env *Env) {
			env.ServerMembers = true
			env.Members = func(context.Context) ([]discord.Member, error) { return nil, discord.ErrForbidden }
		}, "Error: role.members(): discord: missing permissions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := start(t, roleMembersErrPlugin, c.mod)
			if err := h.command("staff", nil, discord.Resolved{}); err != nil {
				t.Fatal(err)
			}
			if got := contentOf(h.responses()[0]); !strings.HasPrefix(got, c.want) {
				t.Errorf("reply = %q, want %q…", got, c.want)
			}
			if c.name == "capability" && !strings.Contains(contentOf(h.responses()[0]), capMembersRead) {
				t.Errorf("reply = %q", contentOf(h.responses()[0]))
			}
		})
	}
}

func TestProtectionRequiresReadGrant(t *testing.T) {
	const src = `
import {command,definePlugin} from "@cicord/sdk";
export default definePlugin({commands:{
  staff:command({description:"inventory",async run(ctx,it){
   try {await it.reply(JSON.stringify(await ctx.server.protection()))}
   catch(e){await it.reply(e.name)}
  }})
}})`
	h := start(t, src, func(e *Env) {
		e.Grants = []string{capMessagesSend}
		e.Protection = func(context.Context) ServerProtection {
			t.Fatal("read refused before inspecting inventory")
			return ServerProtection{}
		}
	})
	if err := h.command("staff", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != "CapabilityError" {
		t.Fatal(got)
	}
	h = start(t, src, nil)
	if got := staffReply(t, h); got["known"] != false || len(got["unmanaged"].([]any)) != 0 {
		t.Fatal(got)
	}
}
