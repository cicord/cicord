package reconcile

import (
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
)

// withMembers lists users on a role of the fixture and fetches the live members.
func (f *fixture) withMembers(role string, ids []string, live ...discord.Member) {
	spec := f.desired.Spec.Roles[role]
	spec.Members = ids
	f.desired.Spec.Roles[role] = spec
	f.live.HasMembers = true
	f.live.Members = map[string]discord.Member{}
	for _, m := range live {
		f.live.Members[m.User.ID] = m
	}
}

func member(id, name string, roles ...string) discord.Member {
	return discord.Member{ServerID: "100", User: discord.User{ID: id, Username: name}, RoleIDs: roles}
}

func TestPlanRoleMembers(t *testing.T) {
	t.Run("gives the role to listed members who lack it", func(t *testing.T) {
		f := base(t)
		f.withMembers("member", []string{"1001", "1002", "1003"},
			member("1001", "alice"), member("1002", "bob", "202"))
		p := f.plan(t)
		if got := sig(p); !slices.Equal(got, []string{"update memberRole member/1001 [assigned]"}) {
			t.Fatalf("changes = %v", got)
		}
		c := p.Changes[0]
		if c.Summary != `give role "Member" to @alice (1001)` || c.ID != "1001" || c.op.phase != phaseRoleMembers {
			t.Errorf("change = %+v", c)
		}
		// 1003 is not in the server: a note, not an error nor a blocked operation
		if len(p.Blocked) != 0 || len(p.Notes) != 1 || p.Notes[0].Key != "member/1003" ||
			!strings.Contains(p.Notes[0].Message, "user 1003 is not in the server") {
			t.Fatalf("blocked = %v, notes = %+v", p.Blocked, p.Notes)
		}
		d := DriftOf(p)
		if len(d) != 1 || d[0] != (DriftEntry{Kind: KindMemberRole, Key: "member/1001", Field: "assigned", Expected: true, Actual: false}) {
			t.Errorf("drift = %+v", d)
		}
	})

	t.Run("additive: unlisted holders keep the role", func(t *testing.T) {
		f := base(t)
		// bob holds the role without being listed (removed from the list): nothing to do
		f.withMembers("member", []string{"1001"}, member("1001", "alice", "202"), member("1002", "bob", "202"))
		p := f.plan(t)
		if !p.Empty() || len(p.Blocked) != 0 || len(p.Notes) != 0 {
			t.Fatalf("plan = %s", p)
		}
	})

	t.Run("blocked at or above the bot's highest role", func(t *testing.T) {
		f := base(t)
		f.botTop = 2 // Admin (position 2) is now level with the bot
		f.withMembers("admin", []string{"1001"}, member("1001", "alice"))
		p := f.plan(t)
		if len(sig(p)) != 0 {
			t.Fatalf("changes = %v", sig(p))
		}
		if got := blockedSig(p); !slices.Equal(got, []string{"update memberRole admin/1001 hierarchy"}) {
			t.Fatalf("blocked = %v", got)
		}
		b := p.Blocked[0]
		if b.Reason != `hierarchy: role "Admin" is at or above the bot's highest role` || b.Summary != `give role "Admin" to @alice (1001)` ||
			b.Fields["assigned"].To != true {
			t.Errorf("blocked = %+v", b)
		}
		if h := ExplainBlocked(b); h.Code != ErrCodeRoleHierarchy || h.Message != `Cannot give role "Admin" to @alice (1001): the role is at or above the bot's highest role` {
			t.Errorf("explained = %+v", h)
		}
		if d := DriftOf(p); len(d) != 1 || d[0].Field != "assigned" {
			t.Errorf("drift = %+v", d)
		}
	})

	t.Run("a role created by the plan is given after it exists", func(t *testing.T) {
		f := base(t)
		f.desired.Spec.Roles["staff"] = config.RoleSpec{Name: "Staff"}
		f.withMembers("staff", []string{"1001"}, member("1001", "alice"))
		p := f.plan(t)
		got := sig(p)
		if len(got) == 0 || got[len(got)-1] != "update memberRole staff/1001 [assigned]" || !slices.Contains(got, "create role staff") {
			t.Fatalf("changes = %v", got)
		}
	})

	t.Run("members not fetched: nothing compared", func(t *testing.T) {
		f := base(t)
		f.withMembers("member", []string{"1001"})
		f.live.HasMembers, f.live.Members = false, nil
		if p := f.plan(t); !p.Empty() || len(p.Notes) != 0 {
			t.Fatalf("plan = %s", p)
		}
	})

	t.Run("integration roles are blocked", func(t *testing.T) {
		f := base(t)
		f.role("202").Managed = true
		f.withMembers("member", []string{"1001"}, member("1001", "alice"))
		p := f.plan(t)
		if !slices.Contains(blockedSig(p), "update memberRole member/1001 managed") || slices.ContainsFunc(sig(p), func(s string) bool { return strings.Contains(s, "memberRole") }) {
			t.Fatalf("changes = %v, blocked = %v", sig(p), blockedSig(p))
		}
	})

	t.Run("preflight needs Manage Roles", func(t *testing.T) {
		f := base(t)
		f.role("900").Permissions = discord.MustPermissions("ManageChannels")
		f.withMembers("member", []string{"1001"}, member("1001", "alice"))
		p := f.plan(t)
		if p.preflight == nil || !strings.Contains(p.preflight.Error(), "roles.member.members") {
			t.Fatalf("preflight = %v", p.preflight)
		}
	})
}

func TestListedMembers(t *testing.T) {
	sp := &config.ServerSpec{Roles: map[string]config.RoleSpec{
		"a": {Members: []string{"3", "1"}}, "b": {Members: []string{"1", "2"}}, "c": {},
	}}
	if got := FetchOptionsFor(sp, nil).Members; !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Errorf("members = %v", got)
	}
	if got := FetchOptionsFor(&config.ServerSpec{}, nil).Members; got != nil {
		t.Errorf("no members listed: %v", got)
	}
}
