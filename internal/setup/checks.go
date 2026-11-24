package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/writeback"
)

// Check statuses.
const (
	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusFail    = "fail"
	StatusPending = "pending"
	StatusSkip    = "skip"
)

// Check IDs (the admin UI titles them from its i18n catalog: setup.checks.<id>).
const (
	CheckToken       = "token"
	CheckIntents     = "intents"
	CheckMember      = "member"
	CheckPermissions = "permissions"
	CheckHierarchy   = "hierarchy"
	CheckCommands    = "commands"
	CheckEvents      = "events"
	CheckPlan        = "plan"
	CheckWriteBack   = "writeback"
)

// Check is one verification with a human explanation.
type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	// Hint says exactly what to do when the check is not ok.
	Hint string `json:"hint,omitempty"`
	// Link is a deep link where the fix happens (developer portal page…).
	Link string         `json:"link,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

// Blocking reports whether a check prevents moving on.
func (c Check) Blocking() bool { return c.Status == StatusFail }

// PortalBotURL is the developer portal page with the token and the privileged intents.
func PortalBotURL(appID string) string { return discord.PortalBotURL(appID) }

// PortalOAuthURL is the developer portal page with the client secret and the redirects.
func PortalOAuthURL(appID string) string {
	if appID == "" {
		return "https://discord.com/developers/applications"
	}
	return "https://discord.com/developers/applications/" + appID + "/oauth2"
}

// RecommendedIntents are the privileged intents the official plugins use.
var RecommendedIntents = discord.MustIntents("ServerMembers", "MessageContent")

// intentToggle is the label of a privileged intent's toggle in the developer portal.
func intentToggle(name string) string {
	switch name {
	case "ServerMembers":
		return "Server Members Intent"
	case "MessageContent":
		return "Message Content Intent"
	case "ServerPresences":
		return "Presence Intent"
	}
	return name
}

// IntentsCheck compares the enabled privileged intents (application flags) with the
// required ones (a fail when missing) and the recommended ones (a warning).
func IntentsCheck(appID string, flags int, required, recommended discord.Intents) Check {
	enabled := IntentsFromFlags(flags)
	missingReq := (required &^ enabled) & discord.PrivilegedIntents
	missingRec := (recommended &^ enabled &^ missingReq) & discord.PrivilegedIntents
	c := Check{ID: CheckIntents, Link: PortalBotURL(appID), Data: map[string]any{
		"enabled": nonNilNames(enabled.Names()), "missing": nonNilNames((missingReq | missingRec).Names()),
	}}
	toggles := func(in discord.Intents) string {
		var out []string
		for _, n := range in.Names() {
			out = append(out, "“"+intentToggle(n)+"”")
		}
		return strings.Join(out, " and ")
	}
	switch {
	case missingReq != 0:
		c.Status = StatusFail
		c.Message = fmt.Sprintf("Your plugins need %s, which is turned off for this application.", toggles(missingReq))
		if len(missingReq.Names()) > 1 {
			c.Message = fmt.Sprintf("Your plugins need %s, which are turned off for this application.", toggles(missingReq))
		}
		c.Hint = fmt.Sprintf("In the Discord Developer Portal, open your application → Bot → Privileged Gateway Intents, turn on %s, click Save Changes, then re-check.", toggles(missingReq))
	case missingRec != 0:
		c.Status = StatusWarn
		verb := "is"
		if len(missingRec.Names()) > 1 {
			verb = "are"
		}
		c.Message = fmt.Sprintf("%s %s turned off. Some official plugins need it (welcome messages need Server Members, levels need Message Content).", toggles(missingRec), verb)
		c.Hint = fmt.Sprintf("In the Discord Developer Portal, open your application → Bot → Privileged Gateway Intents, turn on %s, click Save Changes, then re-check.", toggles(missingRec))
	default:
		c.Status = StatusOK
		c.Message = "The privileged intents Cicord uses are enabled."
	}
	return c
}

func nonNilNames(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// RequiredPermissions are what Cicord asks for when the bot is not Administrator and the
// configuration is not known yet (the wizard's server step): server management plus
// what the official plugins use. Once the configuration exists, the checks use
// Requirements instead (the exact needs of the server and its plugins).
var RequiredPermissions = discord.MustPermissions("ViewChannel", "ManageRoles", "ManageChannels", "ManageServer",
	"SendMessages", "EmbedLinks", "ReadMessageHistory", "AddReactions", "ManageMessages", "ModerateMembers")

const baselineSource = "Cicord defaults"

// BaselineRequirements is RequiredPermissions as Requirements.
func BaselineRequirements() reconcile.Requirements {
	var r reconcile.Requirements
	r.Add(RequiredPermissions, baselineSource)
	return r
}

// corePermissions are needed whatever the configuration: see the channels (gateway
// events, the plan) and answer slash commands.
var corePermissions = discord.MustPermissions("ViewChannel")

// Requirements returns the permissions a bot that is not Administrator needs for one
// server: View Channel, what the server's configuration manages
// (reconcile.ServerRequirements) and what the capabilities granted to the bot's plugin
// instances on that server use (plugin.CapabilityPermissions).
func Requirements(repo *config.Repo, serverKey, botKey string) (reconcile.Requirements, error) {
	var r reconcile.Requirements
	r.Add(corePermissions, "Cicord")
	if repo == nil {
		return r, nil
	}
	if srv := repo.Servers[serverKey]; srv != nil {
		sr, err := reconcile.ServerRequirements(&srv.Spec)
		if err != nil {
			return r, err
		}
		r.Merge(sr)
	}
	for _, in := range repo.Instances {
		if in.Server != serverKey || in.Bot != botKey {
			continue
		}
		for _, g := range in.Grants {
			r.Add(plugin.CapabilityPermissions(g), "plugin "+in.Name+": "+g)
		}
	}
	return r, nil
}

// onlyBaseline reports whether every source is the default list (nothing to explain).
func onlyBaseline(r reconcile.Requirements) bool {
	for _, srcs := range r.Sources {
		for _, s := range srcs {
			if s != baselineSource {
				return false
			}
		}
	}
	return true
}

// basePermissions computes a member's server-level permissions.
func basePermissions(serverID string, roles []discord.Role, m discord.Member, ownerID string) discord.Permissions {
	return reconcile.BasePermissions(serverID, roles, m.RoleIDs, m.User.ID != "" && m.User.ID == ownerID)
}

// ServerChecks verifies that the bot is in the server, has the permissions req lists
// (BaselineRequirements when the configuration is not known) and that its role is above
// every role Cicord will manage.
func ServerChecks(ctx context.Context, c discord.Client, serverID, botUserID, botName string, req reconcile.Requirements) []Check {
	member := Check{ID: CheckMember}
	srv, err := c.GetServer(ctx, serverID)
	if err == nil {
		_, err = c.GetMember(ctx, serverID, botUserID)
	}
	if err != nil {
		member.Status = StatusFail
		member.Message = "The bot is not a member of this server (or cannot see it)."
		member.Hint = "Open the invite link, pick this server and click Authorize, then re-check."
		if !errors.Is(err, discord.ErrNotFound) && !errors.Is(err, discord.ErrForbidden) {
			member.Message = "Discord did not answer: " + err.Error()
			member.Hint = "Wait a moment and re-check."
		}
		skip := func(id string) Check {
			return Check{ID: id, Status: StatusSkip, Message: "Checked once the bot is in the server."}
		}
		return []Check{member, skip(CheckPermissions), skip(CheckHierarchy)}
	}
	member.Status, member.Message = StatusOK, fmt.Sprintf("%s is a member of %s.", botName, srv.Name)
	m, _ := c.GetMember(ctx, serverID, botUserID)
	roles, err := c.ListRoles(ctx, serverID)
	if err != nil {
		e := Check{Status: StatusFail, Message: "Could not read the server roles: " + err.Error(), Hint: "Re-check in a moment."}
		p, h := e, e
		p.ID, h.ID = CheckPermissions, CheckHierarchy
		return []Check{member, p, h}
	}
	return []Check{member, PermissionsCheck(serverID, roles, m, srv.OwnerID, req), HierarchyCheck(serverID, roles, m, botName)}
}

// PermissionsCheck verifies the bot's server permissions against req.
func PermissionsCheck(serverID string, roles []discord.Role, bot discord.Member, ownerID string, req reconcile.Requirements) Check {
	c := Check{ID: CheckPermissions}
	perms := basePermissions(serverID, roles, bot, ownerID)
	admin := false
	for _, r := range roles {
		if bot.HasRole(r.ID) && r.Permissions.Has(discord.MustPermissions("Administrator")) {
			admin = true
		}
	}
	missing := req.Missing(perms)
	switch {
	case admin:
		c.Status, c.Message = StatusOK, "The bot has the Administrator permission."
		// what a least-privilege bot role would need instead (docs: self-hosting/bot-permissions)
		switch adminSrc := req.Sources["Administrator"]; {
		case len(adminSrc) > 0:
			c.Message += " It stays required: " + strings.Join(adminSrc, ", ") + " puts ManageRoles in a channel overwrite."
		case !onlyBaseline(req):
			if need := req.Permissions; need != 0 {
				c.Message += " Without it, it would need: " + strings.Join(need.Names(), ", ") + "."
			}
		}
		c.Data = map[string]any{"required": nonNilNames(req.Names()), "sources": req.Sources}
	case len(missing.Sources) == 0:
		c.Status, c.Message = StatusOK, "The bot has every permission Cicord needs."
		c.Data = map[string]any{"required": nonNilNames(req.Names()), "sources": req.Sources}
	default:
		c.Status = StatusFail
		c.Data = map[string]any{"missing": missing.Names(), "sources": missing.Sources}
		c.Message = "The bot is missing permissions: " + strings.Join(missing.Names(), ", ") + "."
		if !onlyBaseline(missing) {
			c.Message += " Needed by: " + strings.Join(missing.Describe(), "; ") + "."
		}
		c.Hint = "In Server Settings → Roles, give the bot's role these permissions (or invite the bot again with the link above, which asks for Administrator), then re-check."
		if missing.Permissions.Has(discord.MustPermissions("Administrator")) {
			c.Hint = "A channel overwrite allows or denies Manage Roles, which only an Administrator bot can set: remove ManageRoles from the overwrites, or give the bot Administrator. " +
				"Then, in Server Settings → Roles, give the bot's role the other permissions, and re-check."
		}
	}
	return c
}

// HierarchyCheck verifies that the bot's highest role is above every role Cicord manages
// (all the roles that are not managed by an integration).
func HierarchyCheck(serverID string, roles []discord.Role, bot discord.Member, botName string) Check {
	c := Check{ID: CheckHierarchy}
	top, topName := -1, ""
	for _, r := range roles {
		if bot.HasRole(r.ID) && r.Position > top {
			top, topName = r.Position, r.Name
		}
	}
	var above []discord.Role
	for _, r := range roles {
		if r.IsEveryone(serverID) || r.Managed || bot.HasRole(r.ID) {
			continue
		}
		if r.Position >= top {
			above = append(above, r)
		}
	}
	sort.Slice(above, func(i, j int) bool { return above[i].Position > above[j].Position })
	if len(above) == 0 {
		c.Status = StatusOK
		if topName != "" {
			c.Message = fmt.Sprintf("The role “%s” is above every role Cicord manages.", topName)
		} else {
			c.Message = "No role needs to be managed above the bot."
		}
		return c
	}
	names := make([]string, len(above))
	for i, r := range above {
		names[i] = "“" + r.Name + "”"
	}
	if topName == "" {
		topName = botName
	}
	c.Status = StatusFail
	c.Data = map[string]any{"botRole": topName, "above": names}
	c.Message = fmt.Sprintf("The role “%s” is below %d role(s) Cicord must manage: %s. A bot can only edit roles below its own.",
		topName, len(above), strings.Join(names, ", "))
	c.Hint = fmt.Sprintf("In Discord, open Server Settings → Roles and drag the role “%s” above %s (to the top of the list), then re-check.",
		topName, names[0])
	return c
}

// ───────────────────────────────────────────────────────────── diagnostics

// Checker runs the verification checklist against the running runtime. It is used by the
// wizard's Verify step and by the admin Diagnostics page.
type Checker struct {
	Runtime *runtime.Runtime
	Probe   Probe
	// Secrets resolves token secretRefs (env first, then the secret store).
	Secrets func(string) (string, bool)
	// Writer is the write-back writer (nil: write-back not configured).
	Writer writeback.Writer
	// CacheTTL keeps the expensive results (token / application lookup, plan, write-back
	// probe) between runs, so that a UI polling every few seconds does not hammer Discord
	// and git (default 15s; negative disables). Live checks (members, permissions,
	// hierarchy, commands, events) always run.
	CacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]slowResults
}

type slowResults struct {
	at       time.Time
	token    string // the token the identity belongs to (a new token invalidates)
	ident    Identity
	identErr error
	plan     reconcile.Plan
	planErr  error
	wbErr    error
}

func (c *Checker) ttl() time.Duration {
	if c.CacheTTL == 0 {
		return 15 * time.Second
	}
	return c.CacheTTL
}

// slow returns the cached slow results of a server, computing them when stale.
func (c *Checker) slow(ctx context.Context, serverKey, token string) slowResults {
	c.mu.Lock()
	r, ok := c.cache[serverKey]
	c.mu.Unlock()
	if ok && r.token == token && c.ttl() > 0 && time.Since(r.at) < c.ttl() {
		return r
	}
	r = slowResults{at: time.Now(), token: token}
	if token != "" {
		r.ident, r.identErr = c.Probe.Identify(ctx, token)
	}
	r.plan, r.planErr = c.Runtime.PlanServer(ctx, serverKey)
	if c.Writer != nil {
		r.wbErr = probeWriteBack(ctx, c.Writer)
	}
	if ctx.Err() == nil {
		c.mu.Lock()
		if c.cache == nil {
			c.cache = map[string]slowResults{}
		}
		c.cache[serverKey] = r
		c.mu.Unlock()
	}
	return r
}

// Invalidate drops the cached results (after a change the user made).
func (c *Checker) Invalidate() {
	c.mu.Lock()
	c.cache = nil
	c.mu.Unlock()
}

// Report is the result of a checklist run.
type Report struct {
	Server string          `json:"server"`
	Bot    string          `json:"bot"`
	Checks []Check         `json:"checks"`
	Plan   *reconcile.Plan `json:"plan,omitempty"`
	RanAt  time.Time       `json:"ranAt"`
	// OK is true when no check failed and none is pending.
	OK bool `json:"ok"`
}

// Run checks one server. since bounds the "gateway events" check: a member's message
// must have been received after it (zero: any message since the runtime started).
func (c *Checker) Run(ctx context.Context, serverKey string, since time.Time) Report {
	rep := Report{Server: serverKey, RanAt: time.Now().UTC()}
	rt := c.Runtime
	srv, ok := rt.ServerConfig(serverKey)
	if !ok {
		rep.Checks = []Check{{ID: CheckMember, Status: StatusFail, Message: fmt.Sprintf("Unknown server %q.", serverKey)}}
		return rep
	}
	serverID := srv.Spec.DiscordID
	// The reconciling bot, else the first bot listing the server.
	botKey := ""
	for _, ss := range rt.Status().Servers {
		if ss.Key == serverKey {
			botKey = ss.Bot
		}
	}
	repo := rt.Repo()
	if botKey == "" && repo != nil {
		keys := make([]string, 0, len(repo.Bots))
		for k := range repo.Bots {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if slices.Contains(repo.Bots[k].Spec.Servers, serverKey) {
				botKey = k
				break
			}
		}
	}
	rep.Bot = botKey
	add := func(ch Check) { rep.Checks = append(rep.Checks, ch) }
	if botKey == "" || repo == nil || repo.Bots[botKey] == nil {
		add(Check{ID: CheckToken, Status: StatusFail, Message: "No bot lists this server in spec.servers.",
			Hint: "Add the server key to a bot's spec.servers in bots/<bot>/bot.yaml."})
		return finish(rep)
	}
	bot := repo.Bots[botKey]
	appID := bot.Spec.ApplicationID

	// 1. token
	token, has := c.Secrets(bot.Spec.Token.SecretRef)
	if !has {
		token = ""
	}
	slow := c.slow(ctx, serverKey, token)
	var ident Identity
	tok := Check{ID: CheckToken, Link: PortalBotURL(appID)}
	switch {
	case !has:
		tok.Status = StatusFail
		tok.Message = fmt.Sprintf("The bot token (%s) is not set.", bot.Spec.Token.SecretRef)
		tok.Hint = "Set the environment variable " + bot.Spec.Token.SecretRef + " (or store it with `cicord secret set " + bot.Spec.Token.SecretRef + "`), then restart Cicord."
	default:
		ident = slow.ident
		err := slow.identErr
		switch {
		case errors.Is(err, discord.ErrInvalidToken):
			tok.Status = StatusFail
			tok.Message = "Discord rejected the bot token (it was reset or revoked)."
			tok.Hint = "In the Developer Portal → Bot, click Reset Token and copy it; store it with `cicord secret set " + bot.Spec.Token.SecretRef + "` (or the environment variable of that name), then restart Cicord."
		case err != nil:
			tok.Status = StatusWarn
			tok.Message = "Could not reach Discord to validate the token: " + err.Error()
			tok.Hint = "Check the runtime's network connection and re-run the checks."
		case ident.ApplicationID != appID:
			tok.Status = StatusFail
			tok.Message = fmt.Sprintf("The token belongs to application %s, but bot.yaml says %s.", ident.ApplicationID, appID)
			tok.Hint = "Paste the token of the right application, or fix spec.applicationId."
		default:
			if e := rt.BotError(botKey); e != "" {
				tok.Status = StatusFail
				tok.Message = fmt.Sprintf("The token is valid but the bot is not connected: %s.", e)
				tok.Hint = "Check the privileged intents below, then restart Cicord if it persists."
			} else {
				tok.Status = StatusOK
				tok.Message = fmt.Sprintf("Connected as %s.", ident.BotUsername)
			}
		}
	}
	add(tok)

	// 2. intents
	if tok.Status == StatusOK || tok.Status == StatusFail && ident.ApplicationID != "" {
		required := botIntentsOf(repo, botKey)
		add(IntentsCheck(appID, ident.Flags, required, RecommendedIntents))
	} else {
		add(Check{ID: CheckIntents, Status: StatusSkip, Message: "Checked once the token is valid."})
	}

	sess, _, connected := rt.BotSession(botKey)
	if !connected {
		for _, id := range []string{CheckMember, CheckPermissions, CheckHierarchy, CheckCommands, CheckEvents, CheckPlan} {
			add(Check{ID: id, Status: StatusSkip, Message: "Checked once the bot is connected."})
		}
	} else {
		req, err := Requirements(repo, serverKey, botKey)
		if err != nil {
			req = BaselineRequirements()
		}
		for _, ch := range ServerChecks(ctx, sess, serverID, sess.Self().ID, sess.Self().Username, req) {
			add(ch)
		}
		// commands
		cmd := Check{ID: CheckCommands}
		cs, ok := rt.CommandSyncStatus(botKey, serverID)
		switch {
		case !ok:
			cmd.Status, cmd.Message = StatusPending, "Slash commands have not been registered yet."
		case cs.Err != "":
			cmd.Status = StatusFail
			cmd.Message = "Discord refused the slash commands: " + cs.Err
			cmd.Hint = "Invite the bot again with the link from the Server step (it includes the applications.commands scope)."
		case cs.Commands == 0:
			cmd.Status, cmd.Message = StatusOK, "Slash commands are registered (none yet: install a plugin to add some)."
		default:
			cmd.Status, cmd.Message = StatusOK, fmt.Sprintf("%d slash command(s) registered.", cs.Commands)
		}
		cmd.Data = map[string]any{}
		add(cmd)
		// events
		ev := Check{ID: CheckEvents}
		if at, ok := rt.LastMessageAt(serverID); ok && !at.Before(since) {
			ev.Status, ev.Message = StatusOK, "A message from the server reached Cicord through the gateway."
			ev.Data = map[string]any{"at": at.UTC()}
		} else {
			ev.Status = StatusPending
			ev.Message = "Waiting for a message: send any message (for example “hello Cicord”) in a text channel the bot can see. This proves Cicord receives live events."
		}
		add(ev)
		// plan
		pl := Check{ID: CheckPlan}
		var pe *reconcile.PermissionsError
		if p, err := slow.plan, slow.planErr; errors.As(err, &pe) {
			// the preflight: the plan is fine, the bot cannot apply it
			h := reconcile.HumanizeErr(err)
			pl.Status, pl.Message, pl.Hint = StatusFail, h.Message+". Nothing will be applied until this is fixed.", h.Hint
			pl.Data = map[string]any{"changes": len(p.Changes), "blocked": len(p.Blocked), "missing": nonNilNames(pe.Missing.Names()), "lockouts": pe.Lockouts}
			rep.Plan = &p
		} else if err != nil {
			pl.Status, pl.Message = StatusFail, "Could not compute the plan: "+err.Error()
			pl.Hint = reconcileHint(err)
		} else {
			rep.Plan = &p
			switch {
			case len(p.Changes) > 0:
				pl.Status = StatusWarn
				pl.Message = fmt.Sprintf("Discord differs from the configuration: %d change(s) pending.", len(p.Changes))
				pl.Hint = "Open Sync → Plan in the admin to review them; Cicord applies them on the next sync."
			case len(p.Blocked) > 0:
				pl.Status = StatusWarn
				pl.Message = fmt.Sprintf("%d change(s) are blocked.", len(p.Blocked))
				pl.Hint = "Open Sync → Plan in the admin for the explanation."
			default:
				pl.Status, pl.Message = StatusOK, "Discord matches the configuration: nothing to change."
			}
			pl.Data = map[string]any{"changes": len(p.Changes), "blocked": len(p.Blocked)}
		}
		add(pl)
	}

	// write-back
	wb := Check{ID: CheckWriteBack}
	if c.Writer == nil {
		wb.Status, wb.Message = StatusWarn, "Write-back is not configured: changes made in the admin cannot be saved."
	} else if err := slow.wbErr; err != nil {
		wb.Status = StatusFail
		wb.Message = "Cicord cannot write to the configuration repository: " + err.Error()
		wb.Hint = "Check the repository URL and the token (it needs read and write access to the repository contents)."
	} else {
		wb.Status, wb.Message = StatusOK, "Cicord can read and update the configuration repository."
	}
	add(wb)
	return finish(rep)
}

func finish(rep Report) Report {
	rep.OK = true
	for _, c := range rep.Checks {
		if c.Status == StatusFail || c.Status == StatusPending {
			rep.OK = false
		}
	}
	return rep
}

// probeWriteBack runs a no-op write (cicord.yaml rewritten with its own content): it
// clones/fetches the repository with the configured credentials and leaves no commit. A
// working directory (cicord dev) is only checked for write access: rewriting the file
// would race with the person editing it.
func probeWriteBack(ctx context.Context, w writeback.Writer) error {
	if dw, ok := w.(*writeback.DirWriter); ok {
		f, err := os.OpenFile(filepath.Join(dw.Root, "cicord.yaml"), os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	}
	_, err := w.Write(ctx, writeback.Request{
		Edits: map[string]writeback.EditFunc{"cicord.yaml": func(cur []byte) ([]byte, error) {
			if cur == nil {
				return nil, errors.New("cicord.yaml not found")
			}
			return cur, nil
		}},
		Message: "check write-back", Actor: "cicord", Source: "setup", Mode: writeback.ModeCommit,
	})
	return err
}

func reconcileHint(err error) string {
	if h := reconcile.HumanizeErr(err); h != nil {
		return h.Hint
	}
	return ""
}

// botIntentsOf returns the gateway intents the runtime requests for a bot (same rule as
// the runtime: config.Repo.GatewayIntents).
func botIntentsOf(repo *config.Repo, bot string) discord.Intents {
	in, _ := discord.ParseIntents(repo.GatewayIntents(bot))
	return in
}
