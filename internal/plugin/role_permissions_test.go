package plugin

import (
	"reflect"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// Role.permissions is the Discord bitfield as a decimal string for known roles, and
// undefined (never "0") for a role the runtime does not know. ctx.server.cicordBotIds
// names every project bot, read at each access.
func TestRolePermissionsAndProjectBots(t *testing.T) {
	const src = `import {command,definePlugin} from "@cicord/sdk"; export default definePlugin({commands:{staff:command({description:"x", async run(ctx,it){
 const mod=ctx.server.role("moderator"), unknown=(await ctx.server.members({limit:1}))[0].roles[0];
 const first=ctx.server.cicordBotIds; let frozen=Object.isFrozen(first);
 await it.reply(JSON.stringify({mod:mod.permissions, admin:(BigInt(mod.permissions)&(1n<<3n))!==0n, unknown:unknown.permissions===undefined, first, second:ctx.server.cicordBotIds, frozen}));
 }})}})`
	calls := 0
	h := start(t, src, func(e *Env) {
		e.ServerMembers = true
		e.CicordBotIDs = func() []string {
			calls++
			if calls == 1 {
				return []string{"500"}
			}
			return []string{"500", "30"}
		}
	})
	h.dc.members["42"] = discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, RoleIDs: []string{"999"}}
	res := h.inst.env.Resolver.(*fakeResolver)
	r := res.roles["201"]
	r.Permissions = discord.MustPermissions("Administrator", "ManageRoles")
	res.roles["201"] = r
	got := staffReply(t, h)
	want := map[string]any{
		"mod": "268435464", "admin": true, "unknown": true, "frozen": true,
		// The instance's own bot (id 1) is always included; ascending snowflake order.
		"first":  []any{"1", "500"},
		"second": []any{"1", "30", "500"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
