package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// env is a simulated server managed by a bot, plus a state store.
type env struct {
	t       *testing.T
	w       *sim.World
	srv     discord.Server
	bot     discord.User
	session discord.Session
	store   *sqlite.Store
	desired *config.Server
	root    string
}

func loadExample(t *testing.T) (*config.Server, string) {
	t.Helper()
	repo, diags := config.Load("../../examples/basic", config.LoadOptions{})
	if repo == nil || diags.HasErrors() {
		t.Fatalf("load example: %v", diags.Errors())
	}
	s := *repo.Servers["ma-commu"] // shallow copy; maps are not mutated by tests below without cloning
	return &s, repo.Root
}

func newEnv(t *testing.T, desired *config.Server, root string) *env {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	token, bot, _ := w.CreateBot("cicord")
	srv := w.CreateServer("Fresh", owner.ID, sim.WithGeneralChannel())
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	sess, err := w.Connect(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d := *desired
	d.Spec.DiscordID = srv.ID
	return &env{t: t, w: w, srv: srv, bot: bot, session: sess, store: store, desired: &d, root: root}
}

func (e *env) reconciler(c discord.Client, mod ...func(*Options)) *Reconciler {
	o := Options{
		Server: e.desired, RepoRoot: e.root, Client: c,
		Resources: e.store.Resources(), KV: e.store.KV("reconcile:" + e.desired.Metadata.Name),
		BotUserID: e.bot.ID, SelfHealDelay: 20 * time.Millisecond,
	}
	for _, m := range mod {
		m(&o)
	}
	r := New(o)
	e.t.Cleanup(r.Close)
	return r
}

func (e *env) mustEmptyPlan(r *Reconciler) {
	e.t.Helper()
	p, err := r.Plan(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	if !p.Empty() {
		e.t.Fatalf("plan should be empty after sync:\n%s", p)
	}
}

func (e *env) role(name string) discord.Role {
	e.t.Helper()
	r, ok := e.w.RoleByName(e.srv.ID, name)
	if !ok {
		e.t.Fatalf("role %q not found", name)
	}
	return r
}

func (e *env) channel(name string) discord.Channel {
	e.t.Helper()
	c, ok := e.w.ChannelByName(e.srv.ID, name)
	if !ok {
		e.t.Fatalf("channel %q not found", name)
	}
	return c
}

func TestSyncExample(t *testing.T) {
	desired, root := loadExample(t)
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	ctx := context.Background()

	p, res, err := r.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, p)
	}
	if len(res.Failed) != 0 || len(res.Applied) != len(p.Changes) {
		t.Fatalf("applied %d/%d, failed %v", len(res.Applied), len(p.Changes), res.Failed)
	}
	t.Logf("first plan:\n%s", p)
	e.mustEmptyPlan(r)

	// server profile
	s, _ := e.w.Server(e.srv.ID)
	if s.Name != "Ma Commu" || s.VerificationLevel != "medium" || s.DefaultNotifications != "onlyMentions" {
		t.Errorf("server = %+v", s)
	}
	if s.SystemChannelID != e.channel("général").ID || s.RulesChannelID != e.channel("règlement").ID {
		t.Errorf("system/rules channel = %s/%s", s.SystemChannelID, s.RulesChannelID)
	}
	// @everyone
	every, _ := e.w.Role(e.srv.ID, e.srv.ID)
	if want := discord.MustPermissions("ViewChannel", "ReadMessageHistory", "AddReactions", "UseApplicationCommands"); every.Permissions != want {
		t.Errorf("@everyone permissions = %v", every.Permissions.Names())
	}
	// roles: hierarchy below the bot's role
	admin, modo, member := e.role("Admin"), e.role("Modo"), e.role("Membre")
	botRole := e.role("cicord")
	if botRole.Position <= admin.Position || admin.Position <= modo.Position || modo.Position <= member.Position {
		t.Errorf("positions bot=%d admin=%d modo=%d member=%d", botRole.Position, admin.Position, modo.Position, member.Position)
	}
	if admin.Color != 0xe74c3c || !admin.Hoist || admin.Permissions != discord.MustPermissions("Administrator") {
		t.Errorf("admin = %+v", admin)
	}
	// channels
	info, community, staff := e.channel("Infos"), e.channel("Communauté"), e.channel("Staff")
	if info.Position >= community.Position || community.Position >= staff.Position {
		t.Errorf("category positions %d %d %d", info.Position, community.Position, staff.Position)
	}
	rules, ann, general, voice, staffChat := e.channel("règlement"), e.channel("annonces"), e.channel("général"), e.channel("Vocal"), e.channel("staff")
	if rules.ParentID != info.ID || ann.ParentID != info.ID || general.ParentID != community.ID || voice.ParentID != community.ID || staffChat.ParentID != staff.ID {
		t.Error("wrong parents")
	}
	if ann.Type != discord.ChannelAnnouncement || voice.Type != discord.ChannelVoice || voice.UserLimit != 10 {
		t.Errorf("announcement/voice = %+v / %+v", ann, voice)
	}
	if rules.Position > ann.Position {
		t.Errorf("rules (%d) should be above announcements (%d)", rules.Position, ann.Position)
	}
	if general.Topic != "Discussions générales" {
		t.Errorf("topic = %q", general.Topic)
	}
	// synced overwrites
	if !slices.EqualFunc(staffChat.Overwrites, staff.Overwrites, func(a, b discord.Overwrite) bool { return a == b }) && len(normOverwrites(staffChat.Overwrites)) != 2 {
		t.Errorf("staff-chat overwrites %+v vs category %+v", staffChat.Overwrites, staff.Overwrites)
	}
	if !e.w.Permissions(e.srv.ID, staffChat.ID, e.bot.ID).Has(discord.MustPermissions("ViewChannel")) {
		t.Error("bot should see staff-chat (administrator)")
	}
	// the pre-existing unmanaged #general channel is untouched
	if g := e.channel("general"); g.ParentID != "" {
		t.Errorf("unmanaged #general moved: %+v", g)
	}
	// mapping recorded
	list, _ := e.store.Resources().List(ctx, "ma-commu")
	if len(list) != len(e.desired.Spec.Roles)+len(e.desired.Spec.Categories)+len(e.desired.Spec.Channels) {
		t.Errorf("resource map has %d entries", len(list))
	}

	// a second Sync does nothing on Discord
	before := e.w.CountCalls(e.bot.ID)
	if p, _, err := r.Sync(ctx); err != nil || !p.Empty() {
		t.Fatalf("second sync: %v\n%s", err, p)
	}
	if after := e.w.CountCalls(e.bot.ID); after != before {
		t.Errorf("second sync made %d mutating calls", after-before)
	}
}

func TestSyncIcon(t *testing.T) {
	desired, _ := loadExample(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "servers", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "servers", "assets", "icon.png"), []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := *desired
	prof := *d.Spec.Profile
	prof.Icon = "assets/icon.png"
	d.Spec.Profile = &prof
	d.File = "servers/ma-commu.yaml"
	e := newEnv(t, &d, dir)
	r := e.reconciler(e.session)
	if _, _, err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.w.Server(e.srv.ID); s.IconURL == "" {
		t.Fatal("icon not uploaded")
	}
	e.mustEmptyPlan(r)
	// a new reconciler (restart) with the same store: still empty
	e.mustEmptyPlan(e.reconciler(e.session))
}

func TestAdoptExisting(t *testing.T) {
	desired, root := loadExample(t)
	e := newEnv(t, desired, root)
	// resources created by hand before Cicord manages the server
	admin, err := e.w.AdminCreateRole(e.srv.ID, discord.RoleParams{Name: new("Admin")})
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := e.w.AdminCreateChannel(e.srv.ID, discord.ChannelParams{Name: new("Communauté"), Type: discord.ChannelCategory})
	gen, _ := e.w.AdminCreateChannel(e.srv.ID, discord.ChannelParams{Name: new("général"), Type: discord.ChannelText})
	r := e.reconciler(e.session)
	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	adopted := map[string]string{}
	for _, c := range p.Changes {
		if c.Action == ActionAdopt {
			adopted[c.Kind+"/"+c.Key] = c.ID
		}
	}
	if adopted["role/admin"] != admin.ID || adopted["category/community"] != cat.ID || adopted["channel/general"] != gen.ID || len(adopted) != 3 {
		t.Fatalf("adopted = %v\n%s", adopted, p)
	}
	if _, err := r.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	e.mustEmptyPlan(r)
	if got := e.channel("général"); got.ID != gen.ID || got.ParentID != cat.ID || got.Topic != "Discussions générales" {
		t.Errorf("adopted channel = %+v", got)
	}
	n := 0
	for _, ro := range e.w.Roles(e.srv.ID) {
		if ro.Name == "Admin" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d Admin roles", n)
	}
}

func TestDriftAndSelfHeal(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()

	t.Run("report only", func(t *testing.T) {
		e := newEnv(t, desired, root)
		r := e.reconciler(e.session)
		if _, _, err := r.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		if err := e.session.Open(ctx, discord.MustIntents("Servers"), func(ev discord.Event) { r.HandleEvent(ev) }); err != nil {
			t.Fatal(err)
		}
		e.w.WaitIdle()
		r.Plan(ctx) // clears the drift flag set by our own events
		if r.DriftPending() {
			t.Fatal("no drift expected")
		}
		gen := e.channel("général")
		if _, err := e.w.AdminUpdateChannel(gen.ID, discord.ChannelParams{Name: new("general-chat")}); err != nil {
			t.Fatal(err)
		}
		e.w.WaitIdle()
		if !r.DriftPending() {
			t.Fatal("drift not detected from the gateway event")
		}
		d, err := r.Drift(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(d) != 1 || d[0].Key != "general" || d[0].Field != "name" || d[0].Expected != "général" || d[0].Actual != "general-chat" {
			t.Fatalf("drift = %+v", d)
		}
		time.Sleep(60 * time.Millisecond)
		if got := e.channel("general-chat"); got.ID != gen.ID {
			t.Fatal("selfHeal is off: the change must not be reverted")
		}
		// unmanaged resources produce no drift
		other := e.channel("general")
		r.Plan(ctx)
		e.w.AdminUpdateChannel(other.ID, discord.ChannelParams{Topic: new("x")})
		e.w.WaitIdle()
		if d, _ := r.Drift(ctx); len(d) != 1 {
			t.Fatalf("unmanaged change reported as drift: %+v", d)
		}
	})

	t.Run("selfHeal", func(t *testing.T) {
		d := *desired
		on := true
		d.Spec.SyncPolicy.SelfHealP = &on
		e := newEnv(t, &d, root)
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
		if err := e.session.Open(ctx, discord.MustIntents("Servers"), func(ev discord.Event) { r.HandleEvent(ev) }); err != nil {
			t.Fatal(err)
		}
		e.w.WaitIdle()
		drain(synced)

		gen := e.channel("général")
		modo := e.role("Modo")
		e.w.AdminUpdateChannel(gen.ID, discord.ChannelParams{Name: new("hacked")})
		e.w.AdminUpdateRole(e.srv.ID, modo.ID, discord.RoleParams{Permissions: new(discord.MustPermissions("Administrator"))})
		e.w.AdminDeleteChannel(e.channel("Vocal").ID)
		e.w.WaitIdle()
		waitFor(t, synced, func() bool {
			c, ok := e.w.Channel(gen.ID)
			m, _ := e.w.Role(e.srv.ID, modo.ID)
			_, voice := e.w.ChannelByName(e.srv.ID, "Vocal")
			return ok && c.Name == "général" && m.Permissions == discord.MustPermissions("KickMembers", "ModerateMembers", "ManageMessages", "ManageThreads") && voice
		})
		e.w.WaitIdle()
		e.mustEmptyPlan(r)
	})
}

func drain(ch chan Plan) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitFor(t *testing.T, synced chan Plan, ok func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok() {
		select {
		case <-synced:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("drift was not reverted")
		}
	}
}

// failingClient fails every mutating call after the first n.
type failingClient struct {
	discord.Client
	n, calls atomic.Int64
}

var errCrash = errors.New("simulated crash")

func (f *failingClient) step() error {
	if f.calls.Add(1) > f.n.Load() {
		return errCrash
	}
	return nil
}

func (f *failingClient) UpdateServer(ctx context.Context, id string, p discord.ServerParams) (discord.Server, error) {
	if err := f.step(); err != nil {
		return discord.Server{}, err
	}
	return f.Client.UpdateServer(ctx, id, p)
}

func (f *failingClient) CreateRole(ctx context.Context, s string, p discord.RoleParams, r string) (discord.Role, error) {
	if err := f.step(); err != nil {
		return discord.Role{}, err
	}
	return f.Client.CreateRole(ctx, s, p, r)
}

func (f *failingClient) UpdateRole(ctx context.Context, s, id string, p discord.RoleParams, r string) (discord.Role, error) {
	if err := f.step(); err != nil {
		return discord.Role{}, err
	}
	return f.Client.UpdateRole(ctx, s, id, p, r)
}

func (f *failingClient) DeleteRole(ctx context.Context, s, id, r string) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.Client.DeleteRole(ctx, s, id, r)
}

func (f *failingClient) SetRolePositions(ctx context.Context, s string, p []discord.RolePosition) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.Client.SetRolePositions(ctx, s, p)
}

func (f *failingClient) CreateChannel(ctx context.Context, s string, p discord.ChannelParams, r string) (discord.Channel, error) {
	if err := f.step(); err != nil {
		return discord.Channel{}, err
	}
	return f.Client.CreateChannel(ctx, s, p, r)
}

func (f *failingClient) UpdateChannel(ctx context.Context, id string, p discord.ChannelParams, r string) (discord.Channel, error) {
	if err := f.step(); err != nil {
		return discord.Channel{}, err
	}
	return f.Client.UpdateChannel(ctx, id, p, r)
}

func (f *failingClient) DeleteChannel(ctx context.Context, id, r string) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.Client.DeleteChannel(ctx, id, r)
}

func (f *failingClient) SetChannelPositions(ctx context.Context, s string, p []discord.ChannelPosition) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.Client.SetChannelPositions(ctx, s, p)
}

// lossyMap loses the mapping write after a successful create (crash between the
// Discord call and the state write).
type lossyMap struct {
	state.ResourceMap
	drop atomic.Int64 // drop the n-th Put (1-based)
	puts atomic.Int64
}

func (l *lossyMap) Put(ctx context.Context, r state.Resource) error {
	if l.puts.Add(1) == l.drop.Load() {
		return errCrash
	}
	return l.ResourceMap.Put(ctx, r)
}

func countNames(e *env) map[string]int {
	m := map[string]int{}
	for _, r := range e.w.Roles(e.srv.ID) {
		m["role:"+r.Name]++
	}
	for _, c := range e.w.Channels(e.srv.ID) {
		m["channel:"+c.Name]++
	}
	return m
}

func TestCrashResume(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	ref := newEnv(t, desired, root)
	if _, _, err := ref.reconciler(ref.session).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	wantNames := countNames(ref)
	total := ref.w.CountCalls(ref.bot.ID)

	for n := 0; n < total; n++ {
		t.Run(fmt.Sprintf("fail after %d calls", n), func(t *testing.T) {
			e := newEnv(t, desired, root)
			fc := &failingClient{Client: e.session}
			fc.n.Store(int64(n))
			if _, _, err := e.reconciler(fc).Sync(ctx); !errors.Is(err, errCrash) {
				t.Fatalf("want simulated failure, got %v", err)
			}
			r := e.reconciler(e.session) // "restart"
			if p, _, err := r.Sync(ctx); err != nil {
				t.Fatalf("resume: %v\n%s", err, p)
			}
			e.mustEmptyPlan(r)
			if got := countNames(e); !mapsEqual(got, wantNames) {
				t.Fatalf("resources after resume = %v, want %v", got, wantNames)
			}
		})
	}

	puts := len(desired.Spec.Roles) + len(desired.Spec.Categories) + len(desired.Spec.Channels)
	for n := 1; n <= puts; n++ {
		t.Run(fmt.Sprintf("mapping write %d lost", n), func(t *testing.T) {
			e := newEnv(t, desired, root)
			lm := &lossyMap{ResourceMap: e.store.Resources()}
			lm.drop.Store(int64(n))
			if _, _, err := e.reconciler(e.session, func(o *Options) { o.Resources = lm }).Sync(ctx); err == nil {
				t.Fatal("want failure")
			}
			r := e.reconciler(e.session)
			if p, _, err := r.Sync(ctx); err != nil {
				t.Fatalf("resume: %v\n%s", err, p)
			}
			e.mustEmptyPlan(r)
			if got := countNames(e); !mapsEqual(got, wantNames) {
				t.Fatalf("duplicates after resume: %v", got)
			}
		})
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestPruneAndOwnership(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	without := func(d *config.Server, key string) *config.Server {
		c := *d
		c.Spec.Channels = map[string]config.Channel{}
		for k, v := range d.Spec.Channels {
			if k != key {
				c.Spec.Channels[k] = v
			}
		}
		return &c
	}
	setup := func(t *testing.T, prune, protect bool) (*env, string, string) {
		e := newEnv(t, desired, root)
		if _, _, err := e.reconciler(e.session).Sync(ctx); err != nil {
			t.Fatal(err)
		}
		// a plugin-owned channel inside a managed category
		tk, err := e.session.CreateChannel(ctx, e.srv.ID, discord.ChannelParams{Name: new("ticket-1"), Type: discord.ChannelText, ParentID: new(e.channel("Staff").ID)}, "")
		if err != nil {
			t.Fatal(err)
		}
		e.store.Resources().Put(ctx, state.Resource{Server: "ma-commu", Kind: state.KindChannel, Key: "ticket-1", ID: tk.ID, Owner: "plugin:moderator/tickets"})
		d := without(e.desired, "voice")
		d.Spec.SyncPolicy = config.SyncPolicy{PruneP: &prune, DeletionProtectionP: &protect}
		e.desired = d
		return e, e.channel("Vocal").ID, tk.ID
	}

	t.Run("no prune: stop managing", func(t *testing.T) {
		e, voiceID, _ := setup(t, false, true)
		r := e.reconciler(e.session)
		p, _, err := r.Sync(ctx)
		if err != nil || len(p.Changes) != 1 || p.Changes[0].Action != ActionUnmanage {
			t.Fatalf("%v\n%s", err, p)
		}
		if _, ok := e.w.Channel(voiceID); !ok {
			t.Fatal("channel deleted without prune")
		}
		e.mustEmptyPlan(r)
	})
	t.Run("prune with deletionProtection: blocked", func(t *testing.T) {
		e, voiceID, _ := setup(t, true, true)
		r := e.reconciler(e.session)
		p, _, err := r.Sync(ctx)
		if err != nil || !p.Empty() || len(p.Blocked) != 1 || p.Blocked[0].Reason != "deletionProtection" {
			t.Fatalf("%v\n%s", err, p)
		}
		if _, ok := e.w.Channel(voiceID); !ok {
			t.Fatal("protected channel deleted")
		}
		if d, _ := r.Drift(ctx); len(d) != 1 || d[0].Key != "voice" || d[0].Field != "exists" || d[0].Expected != false {
			t.Fatalf("drift = %+v", d)
		}
	})
	t.Run("prune: delete, plugin-owned kept", func(t *testing.T) {
		e, voiceID, ticketID := setup(t, true, false)
		r := e.reconciler(e.session)
		p, _, err := r.Sync(ctx)
		if err != nil || len(p.Changes) != 1 || p.Changes[0].Action != ActionDelete {
			t.Fatalf("%v\n%s", err, p)
		}
		if _, ok := e.w.Channel(voiceID); ok {
			t.Fatal("channel not deleted")
		}
		if _, ok := e.w.Channel(ticketID); !ok {
			t.Fatal("plugin-owned channel deleted")
		}
		e.mustEmptyPlan(r)
		if _, err := e.store.Resources().Get(ctx, "ma-commu", state.KindChannel, "voice"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("mapping not removed: %v", err)
		}
	})
}

func TestHierarchyBlocked(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// someone moves Admin above the bot's role and changes its color
	admin := e.role("Admin")
	top := e.role("cicord").Position
	if err := e.w.AdminSetRolePositions(e.srv.ID, []discord.RolePosition{{ID: admin.ID, Position: top}}); err != nil {
		t.Fatal(err)
	}
	e.w.AdminUpdateRole(e.srv.ID, admin.ID, discord.RoleParams{Color: new(0x123456)})
	p, _, err := r.Sync(ctx)
	if err != nil {
		t.Fatalf("%v\n%s", err, p)
	}
	if len(p.Blocked) == 0 || p.Blocked[0].Kind != KindRole || p.Blocked[0].Key != "admin" {
		t.Fatalf("want blocked admin update:\n%s", p)
	}
	if got := e.role("Admin"); got.Color != 0x123456 {
		t.Fatal("role above the bot was modified")
	}
	e.mustEmptyPlan(r)
	if !r.DriftPending() {
		t.Error("blocked differences must keep the server out of sync")
	}
	d, err := r.Drift(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || d[0].Key != "admin" || d[0].Field != "color" || d[0].Expected != "#e74c3c" || d[0].Actual != "#123456" {
		t.Fatalf("drift = %+v", d)
	}
}

func TestTypeChangeReplace(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	e := newEnv(t, desired, root)
	if _, _, err := e.reconciler(e.session).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	old := e.channel("annonces")
	d := *e.desired
	d.Spec.Channels = map[string]config.Channel{}
	for k, v := range e.desired.Spec.Channels {
		if k == "announcements" {
			v.Type = "text"
		}
		d.Spec.Channels[k] = v
	}
	prune, protect := true, false
	d.Spec.SyncPolicy = config.SyncPolicy{PruneP: &prune, DeletionProtectionP: &protect}
	e.desired = &d
	r := e.reconciler(e.session)
	if p, _, err := r.Sync(ctx); err != nil {
		t.Fatalf("%v\n%s", err, p)
	}
	if _, ok := e.w.Channel(old.ID); ok {
		t.Fatal("old channel not deleted")
	}
	if c := e.channel("annonces"); c.Type != discord.ChannelText || c.ParentID != e.channel("Infos").ID {
		t.Fatalf("replacement = %+v", c)
	}
	e.mustEmptyPlan(r)
}

func TestReorderRepair(t *testing.T) {
	desired, root := loadExample(t)
	ctx := context.Background()
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	modo := e.role("Modo")
	// an unmanaged role placed between Admin and Modo keeps its place
	booster, _ := e.w.AdminCreateRole(e.srv.ID, discord.RoleParams{Name: new("Booster")})
	e.w.AdminSetRolePositions(e.srv.ID, []discord.RolePosition{{ID: booster.ID, Position: modo.Position + 1}})
	// someone swaps Admin and Modo, and the two channels of "Infos"
	admin, modo := e.role("Admin"), e.role("Modo")
	e.w.AdminSetRolePositions(e.srv.ID, []discord.RolePosition{{ID: admin.ID, Position: modo.Position}, {ID: modo.ID, Position: admin.Position}})
	rules, ann := e.channel("règlement"), e.channel("annonces")
	e.w.AdminSetChannelPositions(e.srv.ID, []discord.ChannelPosition{{ID: rules.ID, Position: ann.Position}, {ID: ann.ID, Position: rules.Position}})
	if e.role("Modo").Position < e.role("Admin").Position {
		t.Fatal("setup: swap failed")
	}

	p, _, err := r.Sync(ctx)
	if err != nil {
		t.Fatalf("%v\n%s", err, p)
	}
	t.Logf("repair plan:\n%s", p)
	var got []string
	for _, c := range p.Changes {
		got = append(got, c.Action+" "+c.Kind+" "+c.Key)
	}
	if !slices.Equal(got, []string{"reorder role hierarchy", "reorder channel info"}) {
		t.Fatalf("plan:\n%s", p)
	}
	e.mustEmptyPlan(r)
	a, b, m := e.role("Admin"), e.role("Booster"), e.role("Modo")
	if a.Position <= b.Position || b.Position <= m.Position {
		t.Errorf("positions admin=%d booster=%d modo=%d", a.Position, b.Position, m.Position)
	}
	if e.channel("règlement").Position > e.channel("annonces").Position {
		t.Error("channels not reordered")
	}
}

func TestApplyDecodedPlan(t *testing.T) {
	desired, root := loadExample(t)
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	var decoded Plan
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	res, err := r.Apply(context.Background(), decoded)
	if !errors.Is(err, errNoOp) || len(res.Applied) != 0 || len(res.Failed) != len(p.Changes) {
		t.Fatalf("err=%v applied=%d failed=%d", err, len(res.Applied), len(res.Failed))
	}
}
