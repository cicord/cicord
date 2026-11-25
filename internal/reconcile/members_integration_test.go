package reconcile

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
)

// withMaintainers declares a hoisted staff role listing users.
func withMaintainers(desired *config.Server, ids ...string) *config.Server {
	d := *desired
	d.Spec.Roles = maps.Clone(desired.Spec.Roles)
	d.Spec.Roles["maintainer"] = config.RoleSpec{Name: "Maintainer", Color: "#9b59b6", Hoist: true, Members: ids}
	return &d
}

func (e *env) hasRole(userID, roleName string) bool {
	e.t.Helper()
	m, ok := e.w.Member(e.srv.ID, userID)
	return ok && m.HasRole(e.role(roleName).ID)
}

func TestSyncRoleMembers(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	e := newEnv(t, desired, root)
	alice, bob, carol := e.w.CreateUser("alice"), e.w.CreateUser("bob"), e.w.CreateUser("carol")
	for _, u := range []discord.User{alice, bob} {
		if err := e.w.AddMember(e.srv.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	e.desired = withMaintainers(e.desired, alice.ID, carol.ID)
	r := e.reconciler(e.session)

	// alice gets the role (the role is created by the same apply); carol is not in the
	// server: a note, the sync succeeds
	p, res, err := r.Sync(ctx)
	if err != nil || len(res.Failed) != 0 {
		t.Fatalf("sync: %v %v\n%s", err, res.Failed, p)
	}
	if !e.hasRole(alice.ID, "Maintainer") || e.hasRole(bob.ID, "Maintainer") {
		t.Fatal("alice must hold Maintainer, bob must not")
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0].Message, carol.ID) {
		t.Fatalf("notes = %+v", p.Notes)
	}
	var given []sim.Call
	for _, c := range e.w.Calls(e.bot.ID) {
		if c.Method == "AddMemberRole" {
			given = append(given, c)
		}
	}
	if len(given) != 1 || given[0].TargetID != alice.ID || given[0].Reason != "cicord sync" || given[0].Err != nil {
		t.Fatalf("AddMemberRole calls = %+v", given)
	}
	e.mustEmptyPlan(r)
	if r.DriftPending() {
		t.Error("a listed user who is not in the server is not drift")
	}

	// a holder who is not listed keeps the role
	if err := e.w.AdminAddMemberRole(e.srv.ID, bob.ID, e.role("Maintainer").ID); err != nil {
		t.Fatal(err)
	}
	e.mustEmptyPlan(r)

	// removing alice from the list never removes the role
	r.SetDesired(withMaintainers(e.desired, carol.ID), e.root)
	if p, _, err := r.Sync(ctx); err != nil || !p.Empty() {
		t.Fatalf("sync: %v\n%s", err, p)
	}
	if !e.hasRole(alice.ID, "Maintainer") || !e.hasRole(bob.ID, "Maintainer") {
		t.Fatal("unlisted holders must keep the role")
	}
	if n := e.w.CountCalls(e.bot.ID, "RemoveMemberRole"); n != 0 {
		t.Fatalf("%d RemoveMemberRole call(s)", n)
	}

	// carol joins: the next sync gives her the role
	if err := e.w.AddMember(e.srv.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	p, _, err = r.Sync(ctx)
	if err != nil || len(p.Changes) != 1 || p.Changes[0].Kind != KindMemberRole || len(p.Notes) != 0 {
		t.Fatalf("sync after join: %v\n%s", err, p)
	}
	if !e.hasRole(carol.ID, "Maintainer") {
		t.Fatal("carol must hold Maintainer after joining")
	}
	before := e.w.CountCalls(e.bot.ID)
	e.mustEmptyPlan(r)
	if p, _, err := r.Sync(ctx); err != nil || !p.Empty() {
		t.Fatalf("idempotent sync: %v\n%s", err, p)
	}
	if after := e.w.CountCalls(e.bot.ID); after != before {
		t.Errorf("%d mutating call(s) on an in-sync server", after-before)
	}

	// the role moved above the bot: blocked with a reason, nothing given
	r.SetDesired(withMaintainers(e.desired, carol.ID, bob.ID, alice.ID), e.root)
	if err := e.w.AdminRemoveMemberRole(e.srv.ID, bob.ID, e.role("Maintainer").ID); err != nil {
		t.Fatal(err)
	}
	if err := e.w.AdminSetRolePositions(e.srv.ID, []discord.RolePosition{{ID: e.role("Maintainer").ID, Position: e.role("cicord").Position}}); err != nil {
		t.Fatal(err)
	}
	p, _, err = r.Sync(ctx)
	if err != nil {
		t.Fatalf("%v\n%s", err, p)
	}
	var blocked *Blocked
	for i := range p.Blocked {
		if p.Blocked[i].Kind == KindMemberRole {
			blocked = &p.Blocked[i]
		}
	}
	if blocked == nil || blocked.Key != "maintainer/"+bob.ID || !strings.HasPrefix(blocked.Reason, `hierarchy: role "Maintainer"`) {
		t.Fatalf("want a blocked role assignment:\n%s", p)
	}
	if e.hasRole(bob.ID, "Maintainer") {
		t.Fatal("a role above the bot was given")
	}
	if !r.DriftPending() {
		t.Error("a blocked role assignment keeps the server out of sync")
	}
}

func TestRoleMembersSelfHeal(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	on := true
	d := *desired
	d.Spec.SyncPolicy.SelfHealP = &on
	e := newEnv(t, &d, root)
	members := discord.MustIntents("ServerMembers")
	if err := e.w.SetPrivilegedIntents(e.app, members); err != nil {
		t.Fatal(err)
	}
	alice, carol := e.w.CreateUser("alice"), e.w.CreateUser("carol")
	if err := e.w.AddMember(e.srv.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	e.desired = withMaintainers(e.desired, alice.ID, carol.ID)
	synced := make(chan Plan, 16)
	r := e.reconciler(e.session, func(o *Options) {
		o.OnSync = func(p Plan, _ Result, err error) {
			if err != nil {
				t.Errorf("selfHeal sync: %v", err)
			}
			synced <- p
		}
	})
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.session.Open(ctx, discord.MustIntents("Servers")|members, func(ev discord.Event) { r.HandleEvent(ev) }); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	drain(synced)

	// a listed user joins: given right away
	if err := e.w.AddMember(e.srv.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, synced, func() bool { return e.hasRole(carol.ID, "Maintainer") })

	// someone takes the role away from a listed member: given back
	if err := e.w.AdminRemoveMemberRole(e.srv.ID, alice.ID, e.role("Maintainer").ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, synced, func() bool { return e.hasRole(alice.ID, "Maintainer") })
	e.w.WaitIdle()
	e.mustEmptyPlan(r)

	// unlisted members are not drift
	bob := e.w.CreateUser("bob")
	r.Plan(ctx)
	if err := e.w.AddMember(e.srv.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	if r.DriftPending() {
		t.Error("an unlisted member joining is not drift")
	}
}
