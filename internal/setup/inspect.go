package setup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/importer"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
	"github.com/cicord/cicord/internal/version"
)

// Read-only inspection of an existing server (https://cicord.dev/docs/getting-started/evaluate-a-server/):
// the bot is invited with View Channels only, the server is imported and planned with a
// ReadOnlyClient, and the report says what the bot can see, what it could not read and
// what it would need to apply changes. Nothing is changed on Discord.

// InspectPermissions is what the inspection invite asks for: View Channels, enough to
// read the server, its roles and the channels the bot can see (and to list the others).
var InspectPermissions = discord.MustPermissions("ViewChannel")

// InspectInviteURL is the OAuth2 URL that adds the bot with View Channels only.
func InspectInviteURL(appID string) string { return InviteURLWith(appID, InspectPermissions) }

// InviteURLWith is the OAuth2 URL that adds the bot (or updates its role, when it is
// already in the server) with exactly the permissions p.
func InviteURLWith(appID string, p discord.Permissions) string {
	q := url.Values{}
	q.Set("client_id", appID)
	q.Set("scope", "bot applications.commands")
	q.Set("permissions", strconv.FormatUint(uint64(p), 10))
	q.Set("integration_type", "0")
	return "https://discord.com/oauth2/authorize?" + q.Encode()
}

// ───────────────────────────────────────────────────────────── best-effort fetch

// Unreadable is a part of the server the bot could not read.
type Unreadable struct {
	// Part is the YAML section: "widget", "automod", "welcomeScreen"…
	Part string `json:"part"`
	// Name is the human name ("AutoMod rules").
	Name string `json:"name"`
	// Needs is the permission Discord asks for to read it ("" when the failure is not a
	// permission problem).
	Needs string `json:"needs,omitempty"`
	// Error is Discord's answer.
	Error string `json:"error"`
}

// FetchForImport reads everything the importer knows. The core (server, roles, channels,
// the bot member) must be readable; each optional part (widget, onboarding, welcome
// screen, AutoMod, emojis, stickers, soundboard) is read on its own, and the ones Discord
// refuses (missing permission) are returned as unreadable instead of failing the import.
// Other errors (network, Discord down) fail.
func FetchForImport(ctx context.Context, c discord.Client, serverID, botUserID string) (reconcile.Snapshot, []Unreadable, error) {
	snap, err := reconcile.Fetch(ctx, c, serverID, botUserID)
	if err != nil {
		return snap, nil, err
	}
	var out []Unreadable
	part := func(key, name, needs string, read func() error) error {
		err := read()
		switch {
		case err == nil:
			return nil
		case errors.Is(err, discord.ErrForbidden):
			out = append(out, Unreadable{Part: key, Name: name, Needs: needs, Error: err.Error()})
			return nil
		}
		return fmt.Errorf("read %s: %w", name, err)
	}
	notFound := func(err error) error {
		if errors.Is(err, discord.ErrNotFound) {
			return nil
		}
		return err
	}
	steps := []struct {
		key, name, needs string
		read             func() error
	}{
		{"widget", "Server widget", "ManageServer", func() error {
			w, err := c.GetServerWidget(ctx, serverID)
			if err == nil {
				snap.Widget = &w
			}
			return err
		}},
		{"onboarding", "Onboarding", "ManageServer", func() error {
			o, err := c.GetOnboarding(ctx, serverID)
			if err = notFound(err); err == nil {
				snap.Onboarding = &o
			}
			return err
		}},
		{"welcomeScreen", "Welcome screen", "ManageServer", func() error {
			ws, err := c.GetWelcomeScreen(ctx, serverID)
			if err = notFound(err); err == nil {
				snap.WelcomeScreen = &ws
			}
			return err
		}},
		{"automod", "AutoMod rules", "ManageServer", func() error {
			rs, err := c.ListAutoModRules(ctx, serverID)
			snap.AutoMod, snap.HasAutoMod = rs, err == nil
			return err
		}},
		{"emojis", "Emojis", "ManageExpressions", func() error {
			es, err := c.ListEmojis(ctx, serverID)
			snap.Emojis, snap.HasEmojis = es, err == nil
			return err
		}},
		{"stickers", "Stickers", "ManageExpressions", func() error {
			ss, err := c.ListStickers(ctx, serverID)
			snap.Stickers, snap.HasStickers = ss, err == nil
			return err
		}},
		{"soundboard", "Soundboard sounds", "ManageExpressions", func() error {
			ss, err := c.ListSoundboardSounds(ctx, serverID)
			snap.Sounds, snap.HasSounds = ss, err == nil
			return err
		}},
	}
	for _, s := range steps {
		if err := part(s.key, s.name, s.needs, s.read); err != nil {
			return snap, out, err
		}
	}
	return snap, out, nil
}

// UnreadableWarning is the import warning listing the parts that were not read.
func UnreadableWarning(us []Unreadable) string {
	if len(us) == 0 {
		return ""
	}
	parts := make([]string, len(us))
	for i, u := range us {
		parts[i] = u.Name
		if u.Needs != "" {
			parts[i] += " (needs " + u.Needs + ")"
		}
	}
	return "not imported, the bot cannot read them: " + strings.Join(parts, ", ")
}

// ───────────────────────────────────────────────────────────── report

// ChannelAccess is one category or channel as the bot sees it.
type ChannelAccess struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
	// CategoryID and Category are the parent category ("" at the top level).
	CategoryID string `json:"categoryId,omitempty"`
	Category   string `json:"category,omitempty"`
	// Private: @everyone cannot view it.
	Private bool `json:"private"`
	// Visible: the bot can view it (and read what Cicord manages in it).
	Visible bool `json:"visible"`
}

// UnlistedChannel is a channel ID referenced by the server but absent from the channel
// list (hidden from the bot), with what refers to it.
type UnlistedChannel struct {
	ID           string   `json:"id"`
	ReferencedBy []string `json:"referencedBy"`
}

// Need is a permission the bot lacks to apply changes, with the configuration paths
// that need it and why.
type Need struct {
	Permission string   `json:"permission"`
	Sources    []string `json:"sources"`
	Why        string   `json:"why"`
}

// InspectReport is the outcome of an inspection.
type InspectReport struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	// Key is the server key of the generated configuration.
	Key       string `json:"key"`
	BotUserID string `json:"botUserId"`
	BotName   string `json:"botName"`
	// Administrator: the bot is Administrator (or the owner) and sees everything.
	Administrator bool `json:"administrator"`
	// Permissions are the bot's server-level permissions (@everyone included);
	// RolePermissions what its own roles grant (the invite's permissions).
	Permissions     []string        `json:"permissions"`
	RolePermissions []string        `json:"rolePermissions"`
	Categories      []ChannelAccess `json:"categories"`
	Channels        []ChannelAccess `json:"channels"`
	// Unlisted are channels the server refers to (system or rules channel, parent of a
	// listed channel, AutoMod alert…) that Discord did not list: it omits the channels a
	// bot cannot view, so their names are unknown and there may be more.
	Unlisted   []UnlistedChannel `json:"unlisted"`
	Unreadable []Unreadable      `json:"unreadable"`
	// Needs is what the bot lacks to apply changes to the imported configuration.
	Needs     []Need `json:"needs"`
	Hierarchy Check  `json:"hierarchy"`
	// Ready: nothing is missing to manage the imported configuration (permissions, role
	// order, no hidden channel).
	Ready bool `json:"ready"`
	// UpgradeInviteURL re-invites the bot with exactly what Needs lists (Administrator
	// when a channel overwrite needs it); AdminInviteURL with Administrator.
	UpgradeInviteURL string `json:"upgradeInviteUrl,omitempty"`
	AdminInviteURL   string `json:"adminInviteUrl,omitempty"`
	// Plan is the plan of the imported configuration (empty: everything is adopted as is).
	Plan     reconcile.Plan `json:"-"`
	Files    []string       `json:"files"`
	Warnings []string       `json:"warnings"`
	// Reads counts the Discord calls made; Writes the mutating calls attempted, all refused
	// by the ReadOnlyClient (0: nothing tried to change Discord).
	Reads  int `json:"reads"`
	Writes int `json:"writes"`
}

// Hidden returns the categories and channels the bot cannot view.
func (r *InspectReport) Hidden() []ChannelAccess {
	var out []ChannelAccess
	for _, c := range append(slices.Clone(r.Categories), r.Channels...) {
		if !c.Visible {
			out = append(out, c)
		}
	}
	return out
}

// InspectOptions configures Inspect.
type InspectOptions struct {
	// Client is the bot's REST client; Inspect only uses it through ReadOnly.
	Client    discord.Client
	ServerID  string
	BotUserID string
	BotName   string
	// ServerKey of the generated server ("": derived from the server name).
	ServerKey string
	// BotKey, ApplicationID and SecretRef go into the generated bots/<key>/bot.yaml.
	BotKey        string
	ApplicationID string
	SecretRef     string
	// Dir receives the generated project; it must not contain a cicord.yaml.
	Dir string
	// Store receives the resource map (key → Discord ID) so that a later plan in Dir
	// adopts everything; nil keeps it in memory.
	Store    state.Store
	Download importer.Downloader
	// Extra are requirements added to what the configuration needs (the wizard's defaults
	// for the official plugins).
	Extra reconcile.Requirements
}

// ErrNotEmptyPlan is returned when the imported configuration does not plan empty (an
// import round-trip bug): the plan is in the report.
var ErrNotEmptyPlan = errors.New("the imported configuration does not match the server (please report this bug)")

// ErrIncompleteView is returned when the plan is not empty because the server refers to
// channels the bot cannot see (InspectReport.Unlisted): the imported configuration cannot
// reproduce them. The report and the plan are returned; the plan must not be applied.
var ErrIncompleteView = errors.New("the bot cannot see every channel the server refers to, so the imported configuration is incomplete and its plan is not empty: " +
	"do not apply it; let the bot view these channels (or give it Administrator) and inspect again")

// Inspect reads a server through a ReadOnlyClient, generates its configuration into
// Dir, plans it and reports what the bot can see and what applying changes would need.
// It never changes anything on Discord: every mutating call is refused before it
// reaches Discord and counted in Writes.
func Inspect(ctx context.Context, o InspectOptions) (*InspectReport, error) {
	if _, err := os.Stat(filepath.Join(o.Dir, "cicord.yaml")); err == nil {
		return nil, fmt.Errorf("%s already contains a cicord.yaml: inspect writes a new project (use an empty directory)", o.Dir)
	}
	ro := ReadOnly(o.Client)
	rep := &InspectReport{ServerID: o.ServerID, BotUserID: o.BotUserID, BotName: o.BotName}
	defer func() { rep.Reads, rep.Writes = ro.Reads(), ro.Blocked() }()

	snap, unreadable, err := FetchForImport(ctx, ro, o.ServerID, o.BotUserID)
	if err != nil {
		return nil, fmt.Errorf("read server %s (is the bot a member?): %w", o.ServerID, err)
	}
	rep.ServerName, rep.Unreadable = snap.Server.Name, orEmpty(unreadable)
	gen, err := importer.Generate(ctx, importer.Options{ServerKey: o.ServerKey, Snapshot: snap, Download: o.Download})
	if err != nil {
		return nil, err
	}
	rep.Key = gen.Key
	rep.Warnings = orEmpty(gen.Warnings)

	// The project, as the wizard would create it.
	files, err := RepoFiles{Project: gen.Key, ServerKey: gen.Key, ServerYAML: gen.YAML, BotKey: o.BotKey,
		ApplicationID: o.ApplicationID, SecretRef: o.SecretRef}.Generate()
	if err != nil {
		return nil, err
	}
	for p, data := range gen.FileMap() {
		files[p] = data
	}
	files = keepExisting(o.Dir, files)
	rep.Files = catalog.SortedPaths(files)
	if err := writeTree(o.Dir, files); err != nil {
		return nil, err
	}
	repo, ds := config.Load(o.Dir, config.LoadOptions{EngineVersion: version.Version})
	if ds.HasErrors() || repo == nil {
		return nil, fmt.Errorf("the imported configuration does not validate (please report this bug): %w", ds.Err())
	}

	// Access: server permissions, channels, role order, what applying would need.
	owner := o.BotUserID != "" && snap.Server.OwnerID == o.BotUserID
	have := reconcile.BasePermissions(o.ServerID, snap.Roles, snap.BotRoleIDs, owner)
	rep.Administrator = have.Has(discord.MustPermissions("Administrator"))
	rep.Permissions = orEmpty(have.Names())
	var own discord.Permissions
	for _, r := range snap.Roles {
		if slices.Contains(snap.BotRoleIDs, r.ID) {
			own |= r.Permissions
		}
	}
	rep.RolePermissions = orEmpty(own.Names())
	if rep.Administrator {
		rep.Permissions, rep.RolePermissions = []string{"Administrator"}, []string{"Administrator"}
	}
	keys := map[string]string{}
	for _, r := range gen.Resources {
		keys[r.ID] = r.Key
	}
	rep.Categories, rep.Channels = channelAccess(snap, o.BotUserID, have, keys)
	rep.Unlisted = unlisted(snap)
	bot := discord.Member{User: discord.User{ID: o.BotUserID}, RoleIDs: snap.BotRoleIDs}
	rep.Hierarchy = HierarchyCheck(o.ServerID, snap.Roles, bot, o.BotName)
	req, err := Requirements(repo, gen.Key, o.BotKey)
	if err != nil {
		return nil, err
	}
	req.Merge(o.Extra)
	missing := req.Missing(have)
	rep.Needs = needs(missing)
	if o.ApplicationID != "" {
		rep.AdminInviteURL = InviteURL(o.ApplicationID)
		rep.UpgradeInviteURL = InviteURLWith(o.ApplicationID, upgradePermissions(req))
	}
	rep.Ready = len(rep.Needs) == 0 && rep.Hierarchy.Status == StatusOK && len(rep.Hidden()) == 0 && len(rep.Unlisted) == 0

	// The plan, on the resource map of the import: it must be empty.
	st := o.Store
	if st == nil {
		mem, err := sqlite.Open(":memory:")
		if err != nil {
			return nil, err
		}
		defer mem.Close()
		st = mem
	}
	if err := replaceGitMappings(ctx, st, gen.Key, gen.Resources); err != nil {
		return nil, err
	}
	kv := st.KV("reconcile:" + gen.Key)
	if err := gen.RecordFiles(ctx, kv); err != nil {
		return nil, err
	}
	rec := reconcile.New(reconcile.Options{Server: repo.Servers[gen.Key], RepoRoot: o.Dir, Client: ro,
		Resources: readOnlyResources{st.Resources()}, KV: kv, BotUserID: o.BotUserID})
	plan, err := rec.Plan(ctx)
	rec.Close()
	// The preflight refuses a non-empty plan the bot could not apply: the plan itself is
	// what matters here (it is never applied).
	if pe := (*reconcile.PermissionsError)(nil); errors.As(err, &pe) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("plan the imported configuration: %w", err)
	}
	rep.Plan = plan
	switch {
	case plan.Empty():
		return rep, nil
	case len(rep.Unlisted) > 0:
		return rep, ErrIncompleteView
	}
	return rep, ErrNotEmptyPlan
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// channelAccess classifies the categories and channels by what the bot can view,
// following Discord's order: base permissions, @everyone overwrite, the bot's role
// overwrites, its member overwrite.
func channelAccess(snap reconcile.Snapshot, botUserID string, base discord.Permissions, keys map[string]string) (cats, chans []ChannelAccess) {
	view := discord.MustPermissions("ViewChannel")
	serverID := snap.Server.ID
	var everyone discord.Permissions
	for _, r := range snap.Roles {
		if r.IsEveryone(serverID) {
			everyone = r.Permissions
		}
	}
	names := map[string]string{}
	for _, c := range snap.Channels {
		if c.Type == discord.ChannelCategory {
			names[c.ID] = c.Name
		}
	}
	effective := func(p discord.Permissions, ch discord.Channel, roleIDs []string, userID string) discord.Permissions {
		if p.Has(discord.MustPermissions("Administrator")) {
			return p
		}
		var roleAllow, roleDeny, memAllow, memDeny discord.Permissions
		for _, o := range ch.Overwrites {
			switch {
			case o.Type == discord.OverwriteRole && o.ID == serverID:
				p = p&^o.Deny | o.Allow
			case o.Type == discord.OverwriteRole && slices.Contains(roleIDs, o.ID):
				roleAllow |= o.Allow
				roleDeny |= o.Deny
			case o.Type == discord.OverwriteMember && userID != "" && o.ID == userID:
				memAllow |= o.Allow
				memDeny |= o.Deny
			}
		}
		p = p&^roleDeny | roleAllow
		return p&^memDeny | memAllow
	}
	chs := slices.Clone(snap.Channels)
	// Discord's order: by position, voice channels after text channels.
	voice := func(c discord.Channel) bool { return c.Type == discord.ChannelVoice || c.Type == discord.ChannelStage }
	sort.SliceStable(chs, func(i, j int) bool {
		if vi, vj := voice(chs[i]), voice(chs[j]); vi != vj {
			return vj
		}
		return chs[i].Position < chs[j].Position
	})
	for _, ch := range chs {
		a := ChannelAccess{ID: ch.ID, Key: keys[ch.ID], Name: ch.Name, Type: ch.Type.String(),
			CategoryID: ch.ParentID, Category: names[ch.ParentID],
			Private: !effective(everyone, ch, nil, "").Has(view),
			Visible: effective(base, ch, snap.BotRoleIDs, botUserID).Has(view)}
		if ch.Type == discord.ChannelCategory {
			cats = append(cats, a)
		} else {
			chans = append(chans, a)
		}
	}
	return orEmpty(cats), orEmpty(chans)
}

// unlisted returns the channel IDs the snapshot refers to that the channel list does not
// contain, with what refers to them.
func unlisted(snap reconcile.Snapshot) []UnlistedChannel {
	listed := map[string]bool{}
	for _, c := range snap.Channels {
		listed[c.ID] = true
	}
	refs := map[string][]string{}
	var order []string
	add := func(id, by string) {
		if id == "" || listed[id] {
			return
		}
		if _, ok := refs[id]; !ok {
			order = append(order, id)
		}
		if !slices.Contains(refs[id], by) {
			refs[id] = append(refs[id], by)
		}
	}
	srv := snap.Server
	add(srv.SystemChannelID, "systemChannel")
	add(srv.RulesChannelID, "rulesChannel")
	add(srv.PublicUpdatesChannelID, "community.updatesChannel")
	add(srv.SafetyAlertsChannelID, "community.safetyAlertsChannel")
	add(srv.AFKChannelID, "afk.channel")
	for _, c := range snap.Channels {
		add(c.ParentID, "category of #"+c.Name)
	}
	if w := snap.Widget; w != nil {
		add(w.ChannelID, "widget.channel")
	}
	if ob := snap.Onboarding; ob != nil {
		for _, id := range ob.DefaultChannelIDs {
			add(id, "onboarding.defaultChannels")
		}
		for _, p := range ob.Prompts {
			for _, op := range p.Options {
				for _, id := range op.ChannelIDs {
					add(id, "onboarding prompt "+p.Title)
				}
			}
		}
	}
	if ws := snap.WelcomeScreen; ws != nil {
		for _, c := range ws.Channels {
			add(c.ChannelID, "welcomeScreen.channels")
		}
	}
	for _, r := range snap.AutoMod {
		for _, a := range r.Actions {
			add(a.ChannelID, "automod "+r.Name+" alert")
		}
		for _, id := range r.ExemptChannels {
			add(id, "automod "+r.Name+" exemptChannels")
		}
	}
	out := []UnlistedChannel{}
	for _, id := range order {
		out = append(out, UnlistedChannel{ID: id, ReferencedBy: refs[id]})
	}
	return out
}

// needs explains each missing permission.
func needs(missing reconcile.Requirements) []Need {
	out := []Need{}
	for _, n := range missing.Names() {
		srcs := missing.Sources[n]
		out = append(out, Need{Permission: n, Sources: srcs, Why: whyNeeded(n, srcs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission < out[j].Permission })
	return out
}

func overwriteSource(srcs []string) bool {
	for _, s := range srcs {
		if strings.Contains(s, ".overwrites") {
			return true
		}
	}
	return false
}

func whyNeeded(perm string, srcs []string) string {
	switch perm {
	case "Administrator":
		if overwriteSource(srcs) {
			return "Only an administrator can allow or deny ManageRoles in a channel overwrite."
		}
		return "These roles grant Administrator, and a bot can only give permissions it holds. It is needed only to change their permissions: without it, Cicord manages everything else."
	case "ManageRoles":
		return "Edit roles, @everyone, the role order and permission overwrites."
	case "ManageChannels":
		return "Edit categories and channels and their order."
	case "ManageServer":
		return "Edit the server settings, AutoMod, onboarding, the widget and the welcome screen."
	case "ManageExpressions", "CreateExpressions":
		return "Edit emojis, stickers and soundboard sounds."
	case "ViewChannel":
		return "See the channels Cicord manages."
	}
	baseline, other := false, false
	for _, s := range srcs {
		if s == baselineSource {
			baseline = true
		} else {
			other = true
		}
	}
	switch {
	case baseline && other:
		return "A role or overwrite grants it (a bot can only give permissions it holds), and the official plugins use it."
	case baseline:
		return "The official plugins use it."
	}
	return "A role or overwrite grants it, and a bot can only give permissions it holds."
}

// upgradePermissions is what the upgrade invite asks for: every requirement, without
// Administrator when only role permissions need it (the bot then manages everything but
// those roles' permissions). A ManageRoles overwrite needs Administrator itself.
func upgradePermissions(req reconcile.Requirements) discord.Permissions {
	admin := discord.MustPermissions("Administrator")
	if srcs := req.Sources["Administrator"]; len(srcs) > 0 && overwriteSource(srcs) {
		return admin
	}
	return req.Permissions &^ admin
}
