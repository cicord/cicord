package setup

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// TestReadOnlyClientRefusesEveryWrite calls every method of discord.Client through the
// read-only client on a simulated server: only Get*/List* reach Discord, every other
// method fails with ErrReadOnly and the simulator records no mutating call.
func TestReadOnlyClientRefusesEveryWrite(t *testing.T) {
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("S", owner.ID, sim.WithGeneralChannel())
	tok, bot, _ := w.CreateBot("Cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	sess, err := w.Connect(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(context.Background())
	w.ResetCalls()
	ro := ReadOnly(sess)
	it := reflect.TypeFor[discord.Client]()
	v := reflect.ValueOf(ro)
	reads, writes := 0, 0
	for i := range it.NumMethod() {
		m := it.Method(i)
		args := []reflect.Value{reflect.ValueOf(t.Context())}
		for j := 1; j < m.Type.NumIn(); j++ {
			in := m.Type.In(j)
			if in.Kind() == reflect.String {
				args = append(args, reflect.ValueOf(srv.ID).Convert(in)) // a real ID: a passed-through write would succeed
				continue
			}
			args = append(args, reflect.Zero(in))
		}
		out := v.MethodByName(m.Name).Call(args)
		err, _ := out[len(out)-1].Interface().(error)
		if strings.HasPrefix(m.Name, "Get") || strings.HasPrefix(m.Name, "List") {
			reads++
			continue
		}
		writes++
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err = %v, want ErrReadOnly", m.Name, err)
		}
	}
	if calls := w.Calls(""); len(calls) != 0 {
		t.Fatalf("mutating calls reached the simulator: %+v", calls)
	}
	if ro.Reads() != reads || ro.Blocked() != writes || writes < 40 {
		t.Fatalf("reads %d/%d, blocked %d/%d", ro.Reads(), reads, ro.Blocked(), writes)
	}
}

// inspectWorld is the setup simulator's server with a bot invited through the inspection
// link (View Channels only) and one private channel the owner opened to the bot.
func inspectWorld(t *testing.T) (*SimEnv, discord.Server, string, discord.User, string) {
	t.Helper()
	w := sim.NewWorld()
	env, err := NewSimEnv(w)
	if err != nil {
		t.Fatal(err)
	}
	srv := env.Servers()[0]
	tok, appID := env.CreateBot("Cicord")
	if err := env.InviteWith(tok, srv.ID, InspectPermissions, false); err != nil {
		t.Fatal(err)
	}
	var bot discord.User
	for _, b := range w.Bots() {
		bot = b
	}
	if _, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("bot-log"), Type: discord.ChannelText, Overwrites: &[]discord.Overwrite{
		{ID: srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")},
		{ID: bot.ID, Type: discord.OverwriteMember, Allow: discord.MustPermissions("ViewChannel")},
	}}); err != nil {
		t.Fatal(err)
	}
	w.ResetCalls()
	return env, srv, tok, bot, appID
}

func names(cs []ChannelAccess, visible bool) []string {
	var out []string
	for _, c := range cs {
		if c.Visible == visible {
			n := c.Name
			if c.Private {
				n += "*"
			}
			out = append(out, n)
		}
	}
	return out
}

func TestInspectWithViewChannelsOnly(t *testing.T) {
	env, srv, tok, bot, appID := inspectWorld(t)
	w := env.World
	sess, err := w.Connect(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(context.Background())
	dir := t.TempDir()
	rep, err := Inspect(t.Context(), InspectOptions{Client: sess, ServerID: srv.ID, BotUserID: bot.ID, BotName: bot.Username,
		BotKey: "cicord", ApplicationID: appID, SecretRef: "DISCORD_TOKEN", Dir: dir, Download: w.Download})
	if err != nil {
		t.Fatal(err)
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 || rep.Writes != 0 || rep.Reads == 0 {
		t.Fatalf("inspection changed Discord: calls %+v, writes %d, reads %d", calls, rep.Writes, rep.Reads)
	}
	if rep.Administrator || !slices.Equal(rep.RolePermissions, []string{"ViewChannel"}) || !slices.Contains(rep.Permissions, "SendMessages") || rep.Key != "pixel-pals" {
		t.Fatalf("access = admin %v %v / %v key %s", rep.Administrator, rep.RolePermissions, rep.Permissions, rep.Key)
	}
	if got := names(rep.Categories, true); !slices.Equal(got, []string{"Community"}) {
		t.Errorf("visible categories = %v", got)
	}
	if got := names(rep.Categories, false); !slices.Equal(got, []string{"Staff*"}) {
		t.Errorf("hidden categories = %v", got)
	}
	if got := names(rep.Channels, false); !slices.Equal(got, []string{"mod-log*"}) {
		t.Errorf("hidden channels = %v", got)
	}
	if got := names(rep.Channels, true); !slices.Contains(got, "bot-log*") || !slices.Contains(got, "general") || !slices.Contains(got, "rules") {
		t.Errorf("visible channels = %v", got)
	}
	var unread []string
	for _, u := range rep.Unreadable {
		unread = append(unread, u.Part+":"+u.Needs)
	}
	if !slices.Equal(unread, []string{"widget:ManageServer", "welcomeScreen:ManageServer", "automod:ManageServer"}) {
		t.Errorf("unreadable = %v", unread)
	}
	need := map[string]Need{}
	for _, n := range rep.Needs {
		need[n.Permission] = n
	}
	for _, p := range []string{"ManageRoles", "ManageChannels", "ManageServer", "BanMembers", "Administrator"} {
		if _, ok := need[p]; !ok {
			t.Errorf("needs %s: %+v", p, rep.Needs)
		}
	}
	if a := need["Administrator"]; !slices.Equal(a.Sources, []string{"roles.admin.permissions"}) || !strings.Contains(a.Why, "only to change their permissions") {
		t.Errorf("Administrator need = %+v", a)
	}
	if _, ok := need["ViewChannel"]; ok {
		t.Errorf("ViewChannel is held: %+v", rep.Needs)
	}
	if rep.Ready || rep.Hierarchy.Status != StatusOK {
		t.Errorf("ready %v, hierarchy %+v", rep.Ready, rep.Hierarchy)
	}
	if strings.Contains(rep.UpgradeInviteURL, "permissions=8&") || !strings.Contains(rep.AdminInviteURL, "permissions=8&") {
		t.Errorf("invite links: %s / %s", rep.UpgradeInviteURL, rep.AdminInviteURL)
	}
	if !rep.Plan.Empty() {
		t.Fatalf("plan = %+v", rep.Plan)
	}
	yaml, err := os.ReadFile(filepath.Join(dir, "servers", "pixel-pals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(yaml), "automod:") || !strings.Contains(string(yaml), "mod-log:") {
		t.Errorf("servers/pixel-pals.yaml:\n%s", yaml)
	}
	for _, f := range []string{"cicord.yaml", "bots/cicord/bot.yaml", "servers/pixel-pals.yaml"} {
		if !slices.Contains(rep.Files, f) {
			t.Errorf("files %v miss %s", rep.Files, f)
		}
	}
	if _, err := Inspect(t.Context(), InspectOptions{Client: sess, ServerID: srv.ID, BotUserID: bot.ID, Dir: dir}); err == nil ||
		!strings.Contains(err.Error(), "already contains a cicord.yaml") {
		t.Errorf("inspect into a project: %v", err)
	}

	// With Administrator (the invite authorized again): everything is visible and read.
	if err := env.SetBotPermissions(tok, srv.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	rep, err = Inspect(t.Context(), InspectOptions{Client: sess, ServerID: srv.ID, BotUserID: bot.ID, BotName: bot.Username,
		BotKey: "cicord", ApplicationID: appID, SecretRef: "DISCORD_TOKEN", Dir: t.TempDir(), Download: w.Download})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Administrator || len(rep.Hidden()) != 0 || len(rep.Unreadable) != 0 || len(rep.Needs) != 0 || !rep.Ready || !rep.Plan.Empty() {
		t.Fatalf("with Administrator: %+v", rep)
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

// TestWizardInspectFirst: the "Inspect first" path invites with View Channels, inspects
// without changing Discord or storing anything, keeps the Repository step closed until
// the permissions are upgraded, then continues on the normal path.
func TestWizardInspectFirst(t *testing.T) {
	ctx := t.Context()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sec, err := secrets.Open(ctx, store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	w := sim.NewWorld()
	env, err := NewSimEnv(w)
	if err != nil {
		t.Fatal(err)
	}
	wz := New(Options{Mode: "dev", Store: store, Secrets: sec, Connector: w, Probe: env.Probe(), Sim: env, DataDir: t.TempDir()})
	if _, err := wz.SetIntent(ctx, "existing"); err != nil {
		t.Fatal(err)
	}
	tok, _ := wz.SimCreateBot("Cicord")
	v, err := wz.SubmitToken(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if v.Access != AccessAdmin || !strings.Contains(v.InviteURL, "permissions=8&") || !strings.Contains(v.InspectInviteURL, "permissions=1024&") {
		t.Fatalf("view = access %s, invite %s, inspect %s", v.Access, v.InviteURL, v.InspectInviteURL)
	}
	if _, err := wz.SetAccess(ctx, "everything"); err == nil {
		t.Fatal("bad access accepted")
	}
	if v, err = wz.SetAccess(ctx, AccessInspect); err != nil || v.Access != AccessInspect {
		t.Fatalf("set access = %v %v", v.Access, err)
	}
	if err := wz.SimInvite(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	srv := env.Servers()[0]
	if _, err := wz.SelectServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	botID := v.Bot.UserID
	w.ResetCalls()

	rep, err := wz.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hidden()) != 2 || len(rep.Unreadable) != 3 || rep.Ready || !rep.Plan.Empty() || rep.Files != nil {
		t.Fatalf("report = %+v", rep)
	}
	// The wizard's defaults for the official plugins are part of what to grant.
	found := false
	for _, n := range rep.Needs {
		if n.Permission == "ModerateMembers" && slices.Contains(n.Sources, baselineSource) && strings.Contains(n.Why, "official plugins") {
			found = true
		}
	}
	if !found {
		t.Errorf("needs = %+v", rep.Needs)
	}
	if calls := w.Calls(botID); len(calls) != 0 {
		t.Fatalf("the inspection changed Discord: %+v", calls)
	}
	s, _, _ := LoadState(ctx, store)
	if s.Activated || s.RepoInitialized || s.Repo != nil || s.Step != StepServer {
		t.Fatalf("state after inspection = %+v", s)
	}
	if rs, _ := store.Resources().List(ctx, "pixel-pals"); len(rs) != 0 {
		t.Fatalf("the inspection stored a resource map: %+v", rs)
	}
	// Stop here, or upgrade: the Repository step, the repository choice and the import
	// (activation) stay closed until then, even when called directly.
	var se *Error
	if _, err := wz.Goto(ctx, StepRepository); !errors.As(err, &se) || se.Status != http.StatusConflict {
		t.Fatalf("repository before the upgrade: %v", err)
	}
	if _, err := wz.ChooseRepo(ctx, RepoRequest{Provider: ProviderEmbedded}); !errors.As(err, &se) || se.Status != http.StatusConflict {
		t.Fatalf("repository choice before the upgrade: %v", err)
	}
	if _, err := wz.Import(ctx, true); err == nil {
		t.Fatal("import before the upgrade succeeded")
	}
	// A repository chosen before switching to Inspect first does not open the import either.
	if _, err := wz.update(ctx, func(s *State) error { s.Repo = &RepoConfig{Provider: ProviderEmbedded, Branch: "main"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := wz.Import(ctx, true); !errors.As(err, &se) || se.Status != http.StatusConflict {
		t.Fatalf("import before the upgrade: %v", err)
	}
	if s, _, _ := LoadState(ctx, store); s.RepoInitialized || s.Activated {
		t.Fatalf("state = %+v", s)
	}
	if _, err := wz.update(ctx, func(s *State) error { s.Repo = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := wz.SimGrantAdministrator(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := wz.CheckServer(ctx)
	if err != nil || !res.OK {
		t.Fatalf("re-check after the upgrade = %+v %v", res.Checks, err)
	}
	if rep, err = wz.Inspect(ctx); err != nil || !rep.Ready {
		t.Fatalf("inspection after the upgrade = %+v %v", rep, err)
	}
	if _, err := wz.Goto(ctx, StepRepository); err != nil {
		t.Fatal(err)
	}
	if calls := w.Calls(botID); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

// TestInspectUnlistedChannels: like Discord, the channel list omits what the bot cannot
// view (sim.WithHiddenChannels). The hidden channels are counted from what refers to them
// (a category of a visible channel, the system channel), the plan stays empty.
func TestInspectUnlistedChannels(t *testing.T) {
	w := sim.NewWorld(sim.WithHiddenChannels())
	env, err := NewSimEnv(w)
	if err != nil {
		t.Fatal(err)
	}
	srv := env.Servers()[0]
	tok, appID := env.CreateBot("Cicord")
	if err := env.InviteWith(tok, srv.ID, InspectPermissions, false); err != nil {
		t.Fatal(err)
	}
	bot := w.Bots()[0]
	var staff, modLog string
	for _, c := range w.Channels(srv.ID) {
		switch c.Name {
		case "Staff":
			staff = c.ID
		case "mod-log":
			modLog = c.ID
		}
	}
	// A channel of the private category opened to the bot, and the hidden one as system channel.
	if _, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("staff-news"), Type: discord.ChannelText, ParentID: &staff,
		Overwrites: &[]discord.Overwrite{{ID: bot.ID, Type: discord.OverwriteMember, Allow: discord.MustPermissions("ViewChannel")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AdminUpdateServer(srv.ID, discord.ServerParams{SystemChannelID: &modLog}); err != nil {
		t.Fatal(err)
	}
	w.ResetCalls()
	sess, err := w.Connect(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(context.Background())
	rep, err := Inspect(t.Context(), InspectOptions{Client: sess, ServerID: srv.ID, BotUserID: bot.ID, BotName: bot.Username,
		BotKey: "cicord", ApplicationID: appID, SecretRef: "DISCORD_TOKEN", Dir: t.TempDir(), Download: w.Download})
	if !errors.Is(err, ErrIncompleteView) || rep == nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range rep.Plan.Changes {
		t.Logf("change: %s", c.Summary)
	}
	if len(rep.Hidden()) != 0 {
		t.Errorf("hidden (listed) = %+v", rep.Hidden())
	}
	got := map[string][]string{}
	for _, u := range rep.Unlisted {
		got[u.ID] = u.ReferencedBy
	}
	if len(got) != 2 || !slices.Equal(got[staff], []string{"category of #staff-news"}) || !slices.Equal(got[modLog], []string{"systemChannel"}) {
		t.Errorf("unlisted = %+v", rep.Unlisted)
	}
	if rep.Ready || rep.Plan.Empty() {
		t.Errorf("ready %v, plan %+v", rep.Ready, rep.Plan)
	}
	if calls := w.Calls(bot.ID); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}
