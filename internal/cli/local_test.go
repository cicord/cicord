package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/state/sqlite"
)

type simServer struct {
	w        *sim.World
	serverID string
	appID    string
	token    string
	botID    string
}

func ptr[T any](v T) *T { return &v }

// buildLiveServer creates a hand-made Discord server in the simulator, as if it had been
// configured by hand before adopting CODI.
func buildLiveServer(t *testing.T) *simServer {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Ma Commu", owner.ID)
	token, bot, appID := w.CreateBot("Gardien")
	botRole := must[discord.Role](t)(w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")))
	ok := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	perms := func(n ...string) *discord.Permissions { p := discord.MustPermissions(n...); return &p }
	ok(w.AdminUpdateRole(srv.ID, srv.ID, discord.RoleParams{Permissions: perms("ViewChannel", "SendMessages", "ReadMessageHistory")}))
	modo := must[discord.Role](t)(w.AdminCreateRole(srv.ID, discord.RoleParams{Name: ptr("Modo"), Color: ptr(0xe67e22), Hoist: ptr(true), Permissions: perms("KickMembers", "ManageMessages")}))
	member := must[discord.Role](t)(w.AdminCreateRole(srv.ID, discord.RoleParams{Name: ptr("Membre"), Color: ptr(0x3498db), Mentionable: ptr(true)}))
	ok(w.AdminCreateRole(srv.ID, discord.RoleParams{Name: ptr("Notifs événements")}))
	ok(w.AdminCreateRole(srv.ID, discord.RoleParams{Name: ptr("🎮 Gamers")}))
	_ = member

	info := must[discord.Channel](t)(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Infos"), Type: discord.ChannelCategory,
		Overwrites: &[]discord.Overwrite{{ID: srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}}))
	staff := must[discord.Channel](t)(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Staff"), Type: discord.ChannelCategory,
		Overwrites: &[]discord.Overwrite{
			{ID: srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")},
			{ID: modo.ID, Type: discord.OverwriteRole, Allow: discord.MustPermissions("ViewChannel")},
		}}))
	community := must[discord.Channel](t)(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Communauté"), Type: discord.ChannelCategory}))
	// synced with its category
	rules := must[discord.Channel](t)(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("règlement"), Type: discord.ChannelText, ParentID: &info.ID,
		Overwrites: &[]discord.Overwrite{{ID: srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}}))
	ok(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("annonces"), Type: discord.ChannelAnnouncement, ParentID: &info.ID,
		Overwrites: &[]discord.Overwrite{}})) // explicitly not synced with its category
	general := must[discord.Channel](t)(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("général"), Type: discord.ChannelText, ParentID: &community.ID,
		Topic: ptr("Discussions générales"), RateLimitPerUser: ptr(5)}))
	ok(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Vocal"), Type: discord.ChannelVoice, ParentID: &community.ID, UserLimit: ptr(10)}))
	// custom overwrites inside a category, including a member and the bot's managed role
	ok(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("staff"), Type: discord.ChannelText, ParentID: &staff.ID,
		Overwrites: &[]discord.Overwrite{
			{ID: srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")},
			{ID: modo.ID, Type: discord.OverwriteRole, Allow: discord.MustPermissions("ViewChannel", "ManageMessages")},
			{ID: owner.ID, Type: discord.OverwriteMember, Allow: discord.MustPermissions("ViewChannel")},
		}}))
	ok(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("hors-catégorie"), Type: discord.ChannelText}))
	ok(w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("🎉-fêtes"), Type: discord.ChannelText}))
	ok(w.AdminUpdateServer(srv.ID, discord.ServerParams{SystemChannelID: &general.ID, RulesChannelID: &rules.ID, VerificationLevel: ptr("medium")}))
	_ = botRole
	return &simServer{w: w, serverID: srv.ID, appID: appID, token: token, botID: bot.ID}
}

// newRepo writes a minimal repository with one bot managing server key.
func newRepo(t *testing.T, appID, serverKey string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "codi.yaml"), "apiVersion: codi.dev/v1alpha1\nkind: Project\nmetadata:\n  name: test\nspec:\n  engine: \""+engineRange()+"\"\n")
	writeFile(t, filepath.Join(dir, "bots", "gardien", "bot.yaml"), `apiVersion: codi.dev/v1alpha1
kind: Bot
metadata:
  name: gardien
spec:
  applicationId: "`+appID+`"
  token:
    secretRef: DISCORD_TOKEN_GARDIEN
  servers: [`+serverKey+`]
`)
	return dir
}

func TestImportValidatePlanRoundTrip(t *testing.T) {
	s := buildLiveServer(t)
	useSim(t, s.w, map[string]string{"DISCORD_TOKEN": s.token, "DISCORD_TOKEN_GARDIEN": s.token})
	dir := newRepo(t, s.appID, "ma-commu")

	code, out, errOut := run(t, "import", s.serverID, dir)
	if code != 0 {
		t.Fatalf("import = %d\n%s\n%s", code, out, errOut)
	}
	yamlText := readFile(t, filepath.Join(dir, "servers", "ma-commu.yaml"))
	for _, want := range []string{
		"name: ma-commu", `discordId: "` + s.serverID + `"`, "systemChannel: general", "rulesChannel: reglement",
		"notifs-evenements:", "name: Notifs événements", "hierarchy: [modo, membre, notifs-evenements, gamers]",
		`name: "🎮 Gamers"`, `name: "🎉-fêtes"`, "fetes:",
		"topic: Discussions générales", "slowmode: 5", "userLimit: 10", "member:", "type: announcement",
		"syncWithCategory: false",
	} {
		if !strings.Contains(yamlText, want) {
			t.Errorf("servers/ma-commu.yaml misses %q:\n%s", want, yamlText)
		}
	}
	if strings.Contains(yamlText, "Gardien") {
		t.Errorf("the bot's managed role was imported:\n%s", yamlText)
	}
	if !strings.Contains(errOut, "managed by an integration") {
		t.Errorf("no warning for the managed role: %s", errOut)
	}

	// the generated file validates
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Fatalf("validate = %d: %s", code, errOut)
	}
	// the resource map was written
	store, err := sqlite.Open(filepath.Join(dir, ".codi", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	rs, _ := store.Resources().List(context.Background(), "ma-commu")
	store.Close()
	if len(rs) != 14 { // 4 roles + 3 categories + 7 channels
		t.Errorf("resource map has %d entries: %+v", len(rs), rs)
	}

	// a plan against the live server shows nothing to do: everything is mapped
	code, out, errOut = run(t, "plan", dir, "--detailed-exitcode")
	if code != 0 || !strings.Contains(out, "No changes") {
		t.Fatalf("plan after import = %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "adopt") || strings.Contains(out, "create") {
		t.Errorf("plan after import adopts/creates:\n%s", out)
	}

	// JSON output
	code, out, _ = run(t, "plan", dir, "-o", "json")
	if code != 0 || !strings.Contains(out, `"valid": true`) || !strings.Contains(out, `"server": "ma-commu"`) {
		t.Errorf("plan -o json = %d\n%s", code, out)
	}

	// a change shows up (exit 2 with --detailed-exitcode) and is applied with --yes
	p := filepath.Join(dir, "servers", "ma-commu.yaml")
	writeFile(t, p, strings.Replace(readFile(t, p), "name: général", "name: discussions", 1))
	code, out, _ = run(t, "plan", dir, "--detailed-exitcode")
	if code != 2 || !strings.Contains(out, "~ rename channel #général → #discussions") || !strings.Contains(out, "Plan: 0 to create, 1 to update, 0 to delete.") {
		t.Fatalf("plan with a rename = %d\n%s", code, out)
	}
	withStdin(t, false, "")
	if code, _, errOut := run(t, "apply", dir); code != 1 || !strings.Contains(errOut, "--yes") {
		t.Errorf("apply without a terminal = %d: %s", code, errOut)
	}
	withStdin(t, true, "no\n")
	if code, _, errOut := run(t, "apply", dir); code != 1 || !strings.Contains(errOut, "cancelled") {
		t.Errorf("apply declined = %d: %s", code, errOut)
	}
	if _, ok := s.w.ChannelByName(s.serverID, "discussions"); ok {
		t.Fatal("declined apply changed Discord")
	}
	withStdin(t, true, "yes\n")
	code, out, errOut = run(t, "apply", dir)
	if code != 0 || !strings.Contains(out, "1 change(s) applied, 0 failed") {
		t.Fatalf("apply = %d\n%s\n%s", code, out, errOut)
	}
	if _, ok := s.w.ChannelByName(s.serverID, "discussions"); !ok {
		t.Fatal("rename not applied")
	}
	if code, out, _ := run(t, "plan", dir, "--detailed-exitcode"); code != 0 {
		t.Errorf("plan after apply = %d\n%s", code, out)
	}

	// adopt: lose the state, rebuild it by name
	os.Remove(filepath.Join(dir, ".codi", "state.db"))
	code, out, _ = run(t, "plan", dir)
	if code != 0 || !strings.Contains(out, "to adopt") {
		t.Errorf("plan without state = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".codi", "state.db")); err == nil {
		t.Error("plan created the state database")
	}
	code, out, errOut = run(t, "adopt", dir, "--yes")
	if code != 0 || !strings.Contains(out, "14 to adopt") || !strings.Contains(out, "= channel general → text channel #discussions") {
		t.Fatalf("adopt = %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = run(t, "plan", dir, "--detailed-exitcode")
	if code != 0 || strings.Contains(out, "adopt") {
		t.Errorf("plan after adopt = %d\n%s", code, out)
	}
	if code, out, _ := run(t, "adopt", dir, "--yes"); code != 0 || !strings.Contains(out, "Nothing to change") {
		t.Errorf("second adopt = %d\n%s", code, out)
	}

	// importing again refuses to overwrite
	if code, _, errOut := run(t, "import", s.serverID, dir); code != 1 || !strings.Contains(errOut, "already declared") {
		t.Errorf("re-import = %d: %s", code, errOut)
	}
	if code, _, errOut := run(t, "import", s.serverID, dir, "--force"); code != 0 {
		t.Errorf("re-import --force = %d: %s", code, errOut)
	}
}

func TestApplyCreatesFromScratch(t *testing.T) {
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Fresh", owner.ID)
	token, bot, appID := w.CreateBot("Gardien")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	useSim(t, w, map[string]string{"DISCORD_TOKEN_GARDIEN": token})
	dir := newRepo(t, appID, "fresh")
	writeFile(t, filepath.Join(dir, "servers", "fresh.yaml"), `apiVersion: codi.dev/v1alpha1
kind: Server
metadata:
  name: fresh
spec:
  discordId: "`+srv.ID+`"
  roles:
    member:
      name: Member
  categories:
    community:
      name: Community
  channels:
    general:
      name: general
      type: text
      category: community
`)
	code, out, errOut := run(t, "apply", dir, "--yes", "-o", "json")
	if code != 0 || !strings.Contains(out, `"server": "fresh"`) {
		t.Fatalf("apply = %d\n%s\n%s", code, out, errOut)
	}
	if _, ok := w.RoleByName(srv.ID, "Member"); !ok {
		t.Error("role not created")
	}
	store := must[*sqlite.Store](t)(sqlite.Open(filepath.Join(dir, ".codi", "state.db")))
	defer store.Close()
	r, err := store.Resources().Get(context.Background(), "fresh", state.KindChannel, "general")
	if ch, ok := w.ChannelByName(srv.ID, "general"); err != nil || !ok || r.ID != ch.ID {
		t.Errorf("mapping = %+v, %v", r, err)
	}

	// a missing token is an error of the server, not a crash
	useSim(t, w, map[string]string{})
	code, _, errOut = run(t, "plan", dir)
	if code != 1 || !strings.Contains(errOut, "DISCORD_TOKEN_GARDIEN") {
		t.Errorf("plan without token = %d: %s", code, errOut)
	}
}
