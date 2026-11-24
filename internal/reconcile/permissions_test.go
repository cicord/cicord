package reconcile

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// corePerms is a least-privilege bot role: enough to manage roles, channels and the
// server, nothing else.
var corePerms = discord.MustPermissions("ViewChannel", "ManageRoles", "ManageChannels", "ManageServer")

func mustPermissionsError(t *testing.T, err error) *PermissionsError {
	t.Helper()
	var pe *PermissionsError
	if !errors.As(err, &pe) {
		t.Fatalf("want a *PermissionsError, got %v", err)
	}
	return pe
}

// noWrites fails when the bot made any mutating call (failed ones included).
func (e *env) noWrites() {
	e.t.Helper()
	if calls := e.w.Calls(e.bot.ID); len(calls) > 0 {
		e.t.Fatalf("the preflight should stop before any write, got %d call(s): %+v", len(calls), calls[0])
	}
}

func (e *env) botRole() discord.Role {
	e.t.Helper()
	r, ok := e.w.RoleByName(e.srv.ID, "cicord")
	if !ok || !r.Managed {
		e.t.Fatalf("bot managed role not found")
	}
	return r
}

func TestPreflightMissingRolePermissions(t *testing.T) {
	desired, root := parityRepo(t, `
  roles:
    moderator: {name: Moderator, permissions: [BanMembers, KickMembers, ManageMessages]}
  channels:
    general-chat: {name: general-chat, type: text}
`)
	e := newEnvWith(t, desired, root, corePerms|discord.MustPermissions("ManageMessages"))
	r := e.reconciler(e.session)
	ctx := context.Background()

	p, res, err := r.Sync(ctx)
	pe := mustPermissionsError(t, err)
	if got := pe.Missing.Names(); !slices.Equal(got, []string{"BanMembers", "KickMembers"}) {
		t.Fatalf("missing = %v", got)
	}
	if src := pe.Missing.Sources["BanMembers"]; !slices.Equal(src, []string{"roles.moderator.permissions"}) {
		t.Fatalf("sources = %v", src)
	}
	if len(res.Applied) != 0 || len(p.Changes) == 0 {
		t.Fatalf("plan should be returned, nothing applied: %+v\n%s", res, p)
	}
	e.noWrites()
	// Plan reports the same error (cicord plan, the admin Plan page, the wizard check).
	if _, err := r.Plan(ctx); !errors.As(err, &pe) {
		t.Fatalf("plan: %v", err)
	}
	h := HumanizeErr(err)
	if h.Code != ErrCodeMissingPermissions || !strings.Contains(h.Message, "BanMembers, KickMembers") || !strings.Contains(h.Hint, "Nothing was applied") {
		t.Fatalf("humanized = %+v", h)
	}
	if !strings.Contains(err.Error(), "needs BanMembers (roles.moderator.permissions)") {
		t.Fatalf("error text = %s", err)
	}

	// Granting the permissions to the bot's role fixes it.
	if _, err := e.w.AdminUpdateRole(e.srv.ID, e.botRole().ID, discord.RoleParams{Permissions: new(corePerms | discord.MustPermissions("ManageMessages", "BanMembers", "KickMembers"))}); err != nil {
		t.Fatal(err)
	}
	if p, res, err := r.Sync(ctx); err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v %+v\n%s", err, res.Failed, p)
	}
	e.mustEmptyPlan(r)
}

// Only what the plan sends is checked: a live role already holding a permission the
// bot lacks does not block an unrelated change.
func TestPreflightIgnoresUnchangedPermissions(t *testing.T) {
	desired, root := parityRepo(t, `
  roles:
    moderator: {name: Moderator, permissions: [BanMembers]}
  channels:
    general-chat: {name: general-chat, type: text, topic: Hello}
`)
	e := newEnvWith(t, desired, root, corePerms)
	if _, err := e.w.AdminCreateRole(e.srv.ID, discord.RoleParams{Name: new("Moderator"), Permissions: new(discord.MustPermissions("BanMembers"))}); err != nil {
		t.Fatal(err)
	}
	r := e.reconciler(e.session)
	if p, res, err := r.Sync(context.Background()); err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v %+v\n%s", err, res.Failed, p)
	}
	e.mustEmptyPlan(r)

	// The static requirements still list it: that is what to give the bot's role.
	req, err := ServerRequirements(&e.desired.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Permissions.Has(discord.MustPermissions("BanMembers", "ManageRoles", "ManageChannels", "ViewChannel")) {
		t.Fatalf("requirements = %v", req.Names())
	}
	if miss := req.Missing(corePerms); !slices.Equal(miss.Names(), []string{"BanMembers"}) {
		t.Fatalf("missing = %v", miss.Names())
	}
}

func TestPreflightManageRolesOverwriteNeedsAdministrator(t *testing.T) {
	desired, root := parityRepo(t, `
  roles:
    moderator: {name: Moderator}
  channels:
    mod-log:
      name: mod-log
      type: text
      overwrites:
        moderator: {allow: [ManageRoles, ViewChannel]}
`)
	e := newEnvWith(t, desired, root, corePerms)
	r := e.reconciler(e.session)
	_, _, err := r.Sync(context.Background())
	pe := mustPermissionsError(t, err)
	if !slices.Equal(pe.Missing.Names(), []string{"Administrator"}) ||
		!slices.Equal(pe.Missing.Sources["Administrator"], []string{"channels.mod-log.overwrites"}) {
		t.Fatalf("missing = %+v", pe.Missing)
	}
	e.noWrites()

	// The same configuration syncs with an Administrator bot (no preflight).
	admin := newEnv(t, desired, root)
	if p, res, err := admin.reconciler(admin.session).Sync(context.Background()); err != nil || len(res.Failed) > 0 {
		t.Fatalf("admin sync: %v %+v\n%s", err, res.Failed, p)
	}
}

const privateStaff = `
  roles:
    staff: {name: Staff}
  categories:
    staff:
      name: Staff
      overwrites:
        "@everyone": {deny: [ViewChannel]}
        staff: {allow: [ViewChannel]}%s
  channels:
    mod-log: {name: mod-log, type: text, category: staff, topic: %s}
    tickets: {name: tickets, type: text, category: staff}
`

func privateStaffSpec(botOverwrite bool, topic string) string {
	bot := ""
	if botOverwrite {
		bot = "\n        \"@bot\": {allow: [ViewChannel]}"
	}
	return strings.Replace(strings.Replace(privateStaff, "%s", bot, 1), "%s", topic, 1)
}

// A private category without "@bot" hides itself from a bot that is not Administrator:
// the first sync would create it, every later sync would fail with Missing Access.
func TestPreflightPrivateChannelNeedsBotOverwrite(t *testing.T) {
	desired, root := parityRepo(t, privateStaffSpec(false, "logs"))
	e := newEnvWith(t, desired, root, corePerms)
	r := e.reconciler(e.session)
	ctx := context.Background()
	_, _, err := r.Sync(ctx)
	pe := mustPermissionsError(t, err)
	var paths []string
	for _, l := range pe.Lockouts {
		paths = append(paths, l.Path)
		if l.Now || !slices.Contains(l.Lost, "ViewChannel") {
			t.Fatalf("lockout = %+v", l)
		}
	}
	if !slices.Equal(paths, []string{"categories.staff", "channels.mod-log", "channels.tickets"}) {
		t.Fatalf("lockouts = %v", paths)
	}
	e.noWrites()
	if h := HumanizeErr(err); h.Code != ErrCodeMissingAccess || !strings.Contains(h.Hint, `"@bot"`) {
		t.Fatalf("humanized = %+v", h)
	}
	if !strings.Contains(err.Error(), `(allow them to "@bot")`) {
		t.Fatalf("error text = %s", err)
	}
}

func TestBotOverwriteKeepsAccessToPrivateChannels(t *testing.T) {
	desired, root := parityRepo(t, privateStaffSpec(true, "logs"))
	e := newEnvWith(t, desired, root, corePerms)
	r := e.reconciler(e.session)
	ctx := context.Background()
	if p, res, err := r.Sync(ctx); err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v %+v\n%s", err, res.Failed, p)
	}
	e.mustEmptyPlan(r)
	modLog := e.channel("mod-log")
	botRole := e.botRole()
	if !slices.ContainsFunc(modLog.Overwrites, func(o discord.Overwrite) bool {
		return o.Type == discord.OverwriteRole && o.ID == botRole.ID && o.Allow == discord.MustPermissions("ViewChannel")
	}) {
		t.Fatalf("mod-log overwrites = %+v (bot role %s)", modLog.Overwrites, botRole.ID)
	}
	if !e.w.Permissions(e.srv.ID, modLog.ID, e.bot.ID).Has(corePerms) {
		t.Fatal("the bot should see and manage #mod-log")
	}

	// A later change to the private channel still applies (the bot can see it).
	next, root := parityRepo(t, privateStaffSpec(true, "moderation-logs"))
	next.Spec.DiscordID = e.srv.ID
	r.SetDesired(next, root)
	e.desired = next
	if p, res, err := r.Sync(ctx); err != nil || len(res.Failed) > 0 {
		t.Fatalf("second sync: %v %+v\n%s", err, res.Failed, p)
	}
	if got := e.channel("mod-log").Topic; got != "moderation-logs" {
		t.Fatalf("topic = %q", got)
	}
	e.mustEmptyPlan(r)
}

// A bot invited without permissions has no managed role: "@bot" is a member overwrite.
func TestBotOverwriteWithoutManagedRole(t *testing.T) {
	desired, root := parityRepo(t, privateStaffSpec(true, "logs"))
	e := newEnvWith(t, desired, root, 0)
	role, err := e.w.AdminCreateRole(e.srv.ID, discord.RoleParams{Name: new("Cicord bot"), Permissions: new(corePerms)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.w.AdminSetRolePositions(e.srv.ID, []discord.RolePosition{{ID: role.ID, Position: 5}}); err != nil {
		t.Fatal(err)
	}
	if err := e.w.AdminAddMemberRole(e.srv.ID, e.bot.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	r := e.reconciler(e.session)
	if p, res, err := r.Sync(context.Background()); err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v %+v\n%s", err, res.Failed, p)
	}
	modLog := e.channel("mod-log")
	if !slices.ContainsFunc(modLog.Overwrites, func(o discord.Overwrite) bool {
		return o.Type == discord.OverwriteMember && o.ID == e.bot.ID
	}) {
		t.Fatalf("mod-log overwrites = %+v", modLog.Overwrites)
	}
	e.mustEmptyPlan(r)
}

// A channel the bot already cannot see cannot be fixed by Cicord: reported as such.
func TestPreflightChannelHiddenNow(t *testing.T) {
	desired, root := parityRepo(t, `
  channels:
    secret: {name: secret, type: text, topic: new topic}
`)
	e := newEnvWith(t, desired, root, corePerms)
	if _, err := e.w.AdminCreateChannel(e.srv.ID, discord.ChannelParams{Name: new("secret"), Type: discord.ChannelText,
		Overwrites: &[]discord.Overwrite{{ID: e.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")}}}); err != nil {
		t.Fatal(err)
	}
	r := e.reconciler(e.session)
	_, _, err := r.Sync(context.Background())
	pe := mustPermissionsError(t, err)
	if len(pe.Lockouts) != 1 || pe.Lockouts[0].Path != "channels.secret" || !pe.Lockouts[0].Now {
		t.Fatalf("lockouts = %+v", pe.Lockouts)
	}
	e.noWrites()
	if !strings.Contains(err.Error(), "channels.secret: the bot lacks") {
		t.Fatalf("error text = %s", err)
	}
}

func TestServerRequirements(t *testing.T) {
	desired, _ := parityRepo(t, `
  everyone: {permissions: [ViewChannel, UseApplicationCommands]}
  roles:
    mod: {name: Mod, permissions: [ModerateMembers]}
  channels:
    hooks:
      name: hooks
      type: text
      overwrites:
        mod: {allow: [ManageWebhooks], deny: [ManageRoles]}
  automod:
    links: {name: Links, trigger: keyword, keywords: ["http"], actions: {block: {}}}
`)
	req, err := ServerRequirements(&desired.Spec)
	if err != nil {
		t.Fatal(err)
	}
	want := discord.MustPermissions("ViewChannel", "UseApplicationCommands", "ManageRoles", "ModerateMembers",
		"ManageChannels", "ManageWebhooks", "ManageServer", "Administrator")
	if req.Permissions != want {
		t.Fatalf("requirements = %v, want %v", req.Names(), want.Names())
	}
	for perm, src := range map[string]string{
		"ManageWebhooks":  "channels.hooks.overwrites.mod",
		"Administrator":   "channels.hooks.overwrites.mod",
		"ManageServer":    "automod.links",
		"ModerateMembers": "roles.mod.permissions",
	} {
		if !slices.Contains(req.Sources[perm], src) {
			t.Errorf("%s sources = %v, want %s", perm, req.Sources[perm], src)
		}
	}
	if miss := req.Missing(discord.MustPermissions("Administrator")); len(miss.Sources) != 0 {
		t.Fatalf("an Administrator misses nothing: %v", miss.Names())
	}
}
