package runtime

// End-to-end tests of examples/showcase (tickets, polls, levels, moderation) on the Discord
// simulator, through the real runtime: reconciler, bot host, plugin host, scheduler, store.
// Each test maps to rows of https://cicord.dev/docs/plugins/coverage/.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

const (
	showcaseServer = "showcase"
	showcaseBot    = "cicord"
)

type showcaseOpts struct {
	limits     plugin.Limits
	simOpts    []sim.Option
	httpClient *http.Client
	// prepare edits the copied repository before the runtime starts.
	prepare func(dir string)
	// probe adds the testdata/probe plugin (error paths) to the bot.
	probe bool
}

type showcase struct {
	*env
	store *sqlite.Store
	owner discord.User
	logs  *syncBuffer
}

// syncBuffer collects the runtime logs (plugin console output included).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startShowcase deploys examples/showcase on a simulated Discord.
func startShowcase(t *testing.T, o showcaseOpts) *showcase {
	t.Helper()
	w := sim.NewWorld(o.simOpts...)
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Cicord Showcase", owner.ID)
	token, bot, appID := w.CreateBot("Cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	copyTree(t, "../../examples/showcase", dir)
	replaceIn(t, filepath.Join(dir, "servers/showcase.yaml"), `discordId: "223344556677889900"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/cicord/bot.yaml"), `applicationId: "887766554433221100"`, `applicationId: "`+appID+`"`)
	if o.probe {
		copyTree(t, "testdata/probe", filepath.Join(dir, "plugins/probe"))
		appendTo(t, filepath.Join(dir, "bots/cicord/bot.yaml"), `
    - name: probe
      source: ../../plugins/probe
      grants: [storage.kv, portal.routes, discord.messages.send]
`)
	}
	if o.prepare != nil {
		o.prepare(dir)
	}

	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	var out io.Writer = logs
	if testing.Verbose() {
		out = io.MultiWriter(logs, os.Stderr)
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rt := New(Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { return token, n == "DISCORD_TOKEN_CICORD" },
		PortalURL: "http://localhost:7499", Logger: logger, ResyncInterval: -1,
		PluginLimits: o.limits, AllowPrivateNetworks: o.httpClient != nil, PluginHTTPClient: o.httpClient,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	e := &env{t: t, world: w, rt: rt, dir: dir, serverID: srv.ID, appID: appID, bot: bot, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		<-done
		src.Close()
		store.Close()
	})
	if err := build.PrepareCompiler(); err != nil {
		t.Fatal(err)
	}
	e.waitFor("first revision", func() bool { _, ok := rt.Revision(); return ok })
	s := &showcase{env: e, store: store, owner: owner, logs: logs}
	for _, p := range rt.Status().Bots[0].Plugins {
		if p.Health != HealthHealthy {
			t.Fatalf("plugin %s: %s %s", p.Instance, p.Health, p.Message)
		}
	}
	return s
}

// ───────────────────────────────────────────────────────────── helpers

func appendTo(t *testing.T, file, text string) {
	t.Helper()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// tasksOf lists the pending tasks of a plugin instance due before t (other instances, such as
// the audit-log batch flushes, schedule tasks too).
func (s *showcase) tasksOf(ctx context.Context, instance string, t time.Time) []state.Task {
	s.t.Helper()
	all, err := s.store.Tasks().Due(ctx, t, 100)
	if err != nil {
		s.t.Fatal(err)
	}
	var out []state.Task
	for _, task := range all {
		if task.Scope == "plugin:"+showcaseBot+"/"+instance+"/"+showcaseServer {
			out = append(out, task)
		}
	}
	return out
}

// kv is the KV store of a plugin instance of the showcase bot.
func (s *showcase) kv(instance string) state.KV {
	return s.store.KV("plugin:" + showcaseBot + "/" + instance + "/" + showcaseServer)
}

func (s *showcase) channel(name string) discord.Channel {
	s.t.Helper()
	c, ok := s.world.ChannelByName(s.serverID, name)
	if !ok {
		s.t.Fatalf("no channel %q", name)
	}
	return c
}

func (s *showcase) role(name string) discord.Role {
	s.t.Helper()
	r, ok := s.world.RoleByName(s.serverID, name)
	if !ok {
		s.t.Fatalf("no role %q", name)
	}
	return r
}

// member creates a user, adds them to the server with the given roles (display names).
func (s *showcase) member(username string, roles ...string) discord.User {
	s.t.Helper()
	u := s.world.CreateUser(username)
	if err := s.world.AddMember(s.serverID, u.ID); err != nil {
		s.t.Fatal(err)
	}
	for _, r := range roles {
		if err := s.world.AdminAddMemberRole(s.serverID, u.ID, s.role(r).ID); err != nil {
			s.t.Fatal(err)
		}
	}
	return u
}

// cid is the custom id the SDK puts on the wire for a plugin-local component id.
func cid(t *testing.T, instance, local string) string {
	t.Helper()
	id, err := plugin.EncodeCustomID(instance, local)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// waitMessage waits for a message of channelID matching pred.
func (s *showcase) waitMessage(what, channelID string, pred func(discord.Message) bool) discord.Message {
	s.t.Helper()
	var found discord.Message
	s.waitFor(what, func() bool {
		for _, m := range s.world.Messages(channelID) {
			if pred(m) {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func hasComponent(m discord.Message, customID string) bool {
	for _, row := range m.Components {
		for _, c := range row.Components {
			if c.CustomID == customID {
				return true
			}
		}
	}
	return false
}

func textOf(m discord.Message) string {
	var b strings.Builder
	b.WriteString(m.Content)
	for _, e := range m.Embeds {
		b.WriteString("\n" + e.Title + "\n" + e.Description)
		for _, f := range e.Fields {
			b.WriteString("\n" + f.Name + ": " + f.Value)
		}
	}
	return b.String()
}

// wait waits for an interaction's initial response and fails the test on errors.
func wait(t *testing.T, h *sim.InteractionHandle) sim.InteractionState {
	t.Helper()
	if err := h.Err(); err != nil {
		t.Fatal(err)
	}
	st := h.Wait()
	if st.Failed {
		t.Fatalf("interaction failed (not acknowledged): %+v", st)
	}
	return st
}

// settled waits until the response of a (possibly deferred) interaction is final.
func settled(t *testing.T, h *sim.InteractionHandle, cond func(sim.InteractionState) bool) sim.InteractionState {
	t.Helper()
	wait(t, h)
	st, ok := h.WaitUntil(10*time.Second, cond)
	if !ok {
		t.Fatalf("interaction did not settle: %+v", st)
	}
	return st
}

func replyText(st sim.InteractionState) string {
	if st.Message != nil {
		return textOf(*st.Message)
	}
	if len(st.Followups) > 0 {
		return textOf(st.Followups[0])
	}
	return ""
}

func (s *showcase) route(instance, method, path string, viewer *discord.User, query map[string]string) (plugin.RouteResponse, plugin.RouteDef) {
	s.t.Helper()
	req := plugin.RouteRequest{Query: query}
	if viewer != nil {
		rv := &plugin.RouteViewer{User: *viewer}
		if m, ok := s.world.Member(s.serverID, viewer.ID); ok {
			rv.Member = &m
		}
		req.Viewer = rv
	}
	resp, def, err := s.rt.HandleRoute(context.Background(), showcaseServer, showcaseBot, instance, method, path, req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp, def
}

func decodeBody[T any](t *testing.T, resp plugin.RouteResponse) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		t.Fatalf("body %s: %v", resp.Body, err)
	}
	return v
}

// ───────────────────────────────────────────────────────────── tickets

func TestShowcaseTickets(t *testing.T) {
	s := startShowcase(t, showcaseOpts{})
	w := s.world
	alice := s.member("alice") // regular member, no role
	sam := s.member("sam", "Support")
	eve := s.member("eve", "Member")
	desk := s.channel("help-desk")
	ticketLog := s.channel("ticket-log")
	support, _ := w.ChannelByName(s.serverID, "Support")

	// /ticket-panel (channel option omitted → configured channel), ephemeral by default.
	if h := w.InvokeCommand(alice.ID, s.serverID, desk.ID, s.appID, "ticket-panel", nil); h.Err() == nil {
		t.Fatal("/ticket-panel requires ManageChannels: a member must not be able to use it")
	}
	st := wait(t, w.InvokeCommand(s.owner.ID, s.serverID, desk.ID, s.appID, "ticket-panel", nil))
	if !st.Ephemeral || !strings.Contains(replyText(st), desk.ID) {
		t.Fatalf("/ticket-panel reply = %+v", st)
	}
	openID := cid(t, "tickets", "open")
	panel := s.waitMessage("panel", desk.ID, func(m discord.Message) bool { return hasComponent(m, openID) })
	// channel option: post a second panel elsewhere.
	general := s.channel("general")
	st = wait(t, w.InvokeCommand(s.owner.ID, s.serverID, desk.ID, s.appID, "ticket-panel", map[string]any{"channel": general.ID}))
	if !strings.Contains(replyText(st), general.ID) {
		t.Fatalf("/ticket-panel channel: = %s", replyText(st))
	}
	s.waitMessage("panel in general", general.ID, func(m discord.Message) bool { return hasComponent(m, openID) })

	// Button → modal → plugin-owned private channel.
	click := w.ClickButton(alice.ID, panel, openID)
	st = wait(t, click)
	if st.Modal == nil || st.Modal.CustomID != cid(t, "tickets", "open") || len(st.Modal.Components) == 0 {
		t.Fatalf("open button: %+v", st)
	}
	modal := click.SubmitModal(map[string]string{"subject": "Cannot join voice", "description": "Since yesterday"})
	st = settled(t, modal, func(st sim.InteractionState) bool { return st.Edited || len(st.Followups) > 0 })
	if !st.Deferred || !st.Ephemeral || !strings.Contains(replyText(st), "Your ticket is open") {
		t.Fatalf("modal submit: %+v (%q)", st, replyText(st))
	}
	ticket, ok := w.ChannelByName(s.serverID, "ticket-1")
	if !ok {
		t.Fatalf("ticket channel not created: %+v", w.Channels(s.serverID))
	}
	if ticket.ParentID != support.ID {
		t.Errorf("ticket parent = %s, want the Support category %s", ticket.ParentID, support.ID)
	}
	perms := func(u discord.User) bool {
		return w.Permissions(s.serverID, ticket.ID, u.ID).Has(discord.MustPermissions("ViewChannel"))
	}
	if !perms(alice) || !perms(sam) || perms(eve) {
		t.Errorf("ticket visibility: alice=%v sam=%v eve=%v", perms(alice), perms(sam), perms(eve))
	}
	closeID := cid(t, "tickets", "close:1")
	welcome := s.waitMessage("welcome", ticket.ID, func(m discord.Message) bool { return hasComponent(m, closeID) })
	if !strings.Contains(welcome.Content, "<@"+alice.ID+">") || !strings.Contains(welcome.Content, "<@&"+s.role("Support").ID+">") {
		t.Errorf("welcome = %q", welcome.Content)
	}

	// The reconciler (prune: true) leaves the plugin-owned channel alone.
	if _, _, err := s.rt.SyncServer(context.Background(), showcaseServer); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Channel(ticket.ID); !ok {
		t.Fatal("the reconciler pruned a plugin-owned channel")
	}
	res, err := s.store.Resources().GetByID(context.Background(), showcaseServer, ticket.ID)
	if err != nil || res.Owner != "plugin:cicord/tickets" || res.Kind != state.KindChannel {
		t.Fatalf("ownership = %+v, %v", res, err)
	}
	if !perms(alice) || perms(eve) {
		t.Error("the reconciler changed the ticket overwrites")
	}

	// Activity (messageCreate without MessageContent).
	if _, err := w.SendMessage(alice.ID, ticket.ID, "Here are the details"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SendMessage(sam.ID, ticket.ID, "On it"); err != nil {
		t.Fatal(err)
	}
	w.WaitIdle()

	// GET /tickets: a staff route. Tickets 0.4 declares its routes at member access and
	// checks the staff roles itself (per ticket type).
	var listed struct {
		Items []struct {
			ID, Subject, Status string
			Author              struct{ ID, DisplayName string }
			Messages            int
		}
	}
	s.waitFor("ticket activity", func() bool {
		resp, def := s.route("tickets", "GET", "/tickets", &sam, nil)
		if def.Access != "member" {
			t.Fatalf("access = %q", def.Access)
		}
		listed = decodeBody[struct {
			Items []struct {
				ID, Subject, Status string
				Author              struct{ ID, DisplayName string }
				Messages            int
			}
		}](t, resp)
		return len(listed.Items) == 1 && listed.Items[0].Messages == 2
	})
	if it := listed.Items[0]; it.ID != "1" || it.Status != "open" || it.Author.ID != alice.ID || it.Subject != "Cannot join voice" {
		t.Fatalf("GET /tickets = %+v", listed)
	}

	// Close: only the opener or staff; confirmation (ephemeral) → summary → channel deleted.
	if h := w.ClickButton(eve.ID, welcome, closeID); h.Err() == nil {
		t.Fatal("eve cannot see the ticket channel, so she cannot click its buttons")
	}
	st = wait(t, w.ClickButton(sam.ID, welcome, closeID))
	if !st.Ephemeral || st.Message == nil || !hasComponent(*st.Message, cid(t, "tickets", "close-confirm:1")) {
		t.Fatalf("close confirmation = %+v", st)
	}
	confirm := wait(t, w.ClickButton(sam.ID, *st.Message, cid(t, "tickets", "close-confirm:1")))
	if confirm.ResponseType != discord.ResponseUpdateMessage {
		t.Fatalf("confirm response = %+v", confirm)
	}
	summary := s.waitMessage("summary", ticketLog.ID, func(m discord.Message) bool { return strings.Contains(textOf(m), "Ticket #1 closed") })
	// Tickets 0.4 posts a content-free summary with the portal link (no transcript without
	// the managedMessages feature, no participants).
	if !strings.Contains(textOf(summary), "/tickets?server=showcase&ticket=1") || strings.Contains(textOf(summary), "Cannot join voice") {
		t.Errorf("summary = %s", textOf(summary))
	}
	s.waitFor("ticket channel deleted", func() bool { _, ok := w.Channel(ticket.ID); return !ok })
	if _, err := s.store.Resources().GetByID(context.Background(), showcaseServer, ticket.ID); err == nil {
		t.Error("ownership record kept after deleteChannel")
	}
	// The close is committed by a durable task after the summary: wait for the record.
	s.waitFor("closed ticket listed", func() bool {
		resp, _ := s.route("tickets", "GET", "/tickets", &sam, map[string]string{"status": "closed"})
		return strings.Contains(string(resp.Body), `"status":"closed"`)
	})
}

// ───────────────────────────────────────────────────────────── SDK edge cases (probe)

func TestShowcaseProbeErrorsAndGating(t *testing.T) {
	s := startShowcase(t, showcaseOpts{probe: true})
	w := s.world
	alice := s.member("alice", "Member")
	general := s.channel("general")

	// CapabilityError: thrown in JS, instanceof works, names the capability.
	st := wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-cap", nil))
	if got := replyText(st); got != `{"name":"CapabilityError","capability":"discord.members.read","isCapabilityError":true}` {
		t.Errorf("probe-cap = %s", got)
	}

	// A throwing handler: the user gets the generic ephemeral error.
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-throw", nil))
	if replyText(st) != plugin.GenericErrorMessage || !st.Ephemeral {
		t.Errorf("probe-throw = %+v", st)
	}

	// A runaway loop hits the sync budget: aborted, generic error, the VM keeps serving.
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-spin", nil))
	if replyText(st) != plugin.GenericErrorMessage {
		t.Errorf("probe-spin = %+v", st)
	}
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-ping", nil))
	if replyText(st) != "pong" {
		t.Errorf("probe-ping after a runaway loop = %+v", st)
	}
	if !strings.Contains(s.logs.String(), "budget") {
		t.Errorf("the sync budget interruption is not logged")
	}

	// MessageContent gating per plugin: levels (same bot) declares MessageContent, the probe
	// does not. The probe sees no content, except when the bot is mentioned. (The sim cannot
	// post user attachments; their blanking is covered by internal/plugin tests.)
	msg, err := w.SendMessage(alice.ID, general.ID, "secret plans")
	if err != nil {
		t.Fatal(err)
	}
	mention, err := w.SendMessage(alice.ID, general.ID, "hey <@"+s.bot.ID+"> look")
	if err != nil {
		t.Fatal(err)
	}
	w.WaitIdle()
	seen := func(id string) string {
		var out string
		s.waitFor("probe saw "+id, func() bool {
			resp, _ := s.route("probe", "GET", "/seen/"+id, nil, nil)
			out = string(resp.Body)
			return out != "null"
		})
		return out
	}
	if got := seen(msg.ID); !strings.Contains(got, `"content":""`) {
		t.Errorf("probe (no MessageContent) saw %s", got)
	}
	if got := seen(mention.ID); !strings.Contains(got, "look") {
		t.Errorf("probe did not see a message mentioning the bot: %s", got)
	}
	// message.reply() on a message mentioning the bot (the sim validates the reply
	// reference but does not keep it on the message).
	s.waitMessage("probe reply", general.ID, func(m discord.Message) bool { return m.Content == "you called?" })
}

func TestShowcaseAutoDefer(t *testing.T) {
	s := startShowcase(t, showcaseOpts{probe: true, limits: plugin.Limits{AutoDeferAfter: 100 * time.Millisecond}})
	w := s.world
	alice := s.member("alice", "Member")
	general := s.channel("general")

	// A slow command is deferred automatically; the later reply() edits the deferred response.
	h := w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-slow", nil)
	st := wait(t, h)
	if st.ResponseType != discord.ResponseDeferredMessage || st.Ephemeral {
		t.Fatalf("slow command initial response = %+v", st)
	}
	st = settled(t, h, func(st sim.InteractionState) bool { return st.Edited })
	if st.Message == nil || st.Message.Content != "slow done" {
		t.Fatalf("slow command final = %+v", st)
	}
	// Timers work and console.* goes to the instance logger.
	if !strings.Contains(s.logs.String(), "probe slow start") {
		t.Error("console.log output not in the runtime log")
	}

	// A slow component gets a silent deferred update; update() then edits the message.
	h = w.ClickButton(alice.ID, *st.Message, cid(t, "probe", "slow"))
	st = wait(t, h)
	if st.ResponseType != discord.ResponseDeferredUpdate {
		t.Fatalf("slow component initial response = %+v", st)
	}
	s.waitFor("component update", func() bool {
		m, ok := w.Message(st.Message.ID)
		return ok && m.Content == "slow click done"
	})

	// Ephemeral default of an `ephemeral: true` command also applies to the auto-defer:
	// /rank (levels) is answered quickly, so it stays an ephemeral reply.
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "rank", nil))
	if !st.Ephemeral {
		t.Errorf("/rank (ephemeral: true) = %+v", st)
	}
}

// ───────────────────────────────────────────────────────────── polls (+ levels bonus)

func pollMessage(s *showcase, channelID, pollID string) discord.Message {
	s.t.Helper()
	return s.waitMessage("poll #"+pollID, channelID, func(m discord.Message) bool {
		return hasComponent(m, cid(s.t, "polls", "end:"+pollID))
	})
}

func xpOf(t *testing.T, s *showcase, userID string) float64 {
	t.Helper()
	v, err := s.kv("levels").Get(context.Background(), "xp:"+userID)
	if err != nil {
		return 0
	}
	var n float64
	_ = json.Unmarshal(v, &n)
	return n
}

func TestShowcasePolls(t *testing.T) {
	s := startShowcase(t, showcaseOpts{})
	w := s.world
	ctx := context.Background()
	alice := s.member("alice", "Member")
	bob := s.member("bob") // regular member, no role
	mia := s.member("mia", "Moderator")
	pollsCh := s.channel("polls")
	member := s.role("Member")

	// Only poll managers may ping a role that is not mentionable (Member is not): the
	// bot could, so the plugin refuses it for anyone else, before posting anything.
	st := wait(t, w.InvokeCommand(alice.ID, s.serverID, pollsCh.ID, s.appID, "poll", map[string]any{
		"question": "Game night?", "choices": "Friday | Saturday", "ping": member.ID,
	}))
	if !st.Ephemeral || !strings.Contains(replyText(st), "only poll managers may ping a role that is not mentionable") {
		t.Fatalf("/poll pinging a non-mentionable role = %+v (%s)", st, replyText(st))
	}

	// /poll with string, number, boolean, string choices and mentionable (a member) options.
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, pollsCh.ID, s.appID, "poll", map[string]any{
		"question": "Game night?", "choices": "Friday | Saturday | Sunday", "hours": 2.5,
		"multiple": false, "ping": bob.ID, "thread": true,
	}))
	if !st.Ephemeral || !strings.Contains(replyText(st), "Poll #1 posted. It closes in 2.5 h.") {
		t.Fatalf("/poll = %+v (%s)", st, replyText(st))
	}
	poll := pollMessage(s, pollsCh.ID, "1")
	if !strings.HasPrefix(poll.Content, "<@"+bob.ID+"> — new poll by <@"+alice.ID+">") {
		t.Errorf("ping = %q", poll.Content)
	}
	// allowedMentions: only the pinged member is notified, not the author named in the text.
	if m, _ := w.MessageMentions(poll.ID); !reflect.DeepEqual(m, sim.Mentions{UserIDs: []string{bob.ID}}) {
		t.Errorf("poll #1 notified %+v", m)
	}
	threads := w.Threads(s.serverID)
	if len(threads) != 1 || threads[0].Name != "Poll: Game night?" {
		t.Errorf("threads = %+v", threads)
	}
	tasks := s.tasksOf(ctx, "polls", time.Now().Add(3*time.Hour))
	if len(tasks) != 1 || tasks[0].Name != "close" {
		t.Fatalf("scheduled tasks = %+v", tasks)
	}

	// Buttons with data ids: live update of the poll message + ephemeral follow-up.
	st = wait(t, w.ClickButton(bob.ID, poll, cid(t, "polls", "vote:1:2")))
	if st.ResponseType != discord.ResponseUpdateMessage || !strings.Contains(textOf(*st.Message), "**3. Sunday**\n`████████████` 1 vote (100%)") {
		t.Fatalf("vote = %+v", st)
	}
	st, _ = w.ClickButton(bob.ID, poll, cid(t, "polls", "vote:1:2")).WaitUntil(5*time.Second, func(st sim.InteractionState) bool { return len(st.Followups) > 0 })
	if len(st.Followups) == 0 || st.Followups[0].Content != "Your vote was removed." || st.Followups[0].Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatalf("toggle follow-up = %+v", st.Followups)
	}
	wait(t, w.ClickButton(bob.ID, poll, cid(t, "polls", "vote:1:0")))
	wait(t, w.ClickButton(mia.ID, poll, cid(t, "polls", "vote:1:0")))

	// Only the author (or a manager) closes it; closing cancels the task and publishes
	// polls.closed, which levels turns into bonus XP for every voter.
	st = wait(t, w.ClickButton(bob.ID, poll, cid(t, "polls", "end:1")))
	if !st.Ephemeral || !strings.Contains(replyText(st), "Only the author") {
		t.Fatalf("bob closing = %+v", st)
	}
	st = wait(t, w.ClickButton(alice.ID, poll, cid(t, "polls", "end:1")))
	if st.Message == nil || len(st.Message.Components) != 0 || !strings.Contains(textOf(*st.Message), "Friday** 🏆") {
		t.Fatalf("closed poll = %+v", st.Message)
	}
	if tasks := s.tasksOf(ctx, "polls", time.Now().Add(3*time.Hour)); len(tasks) != 0 {
		t.Errorf("schedule.cancel did not remove the task: %+v", tasks)
	}
	s.waitFor("poll bonus XP", func() bool { return xpOf(t, s, bob.ID) == 10 && xpOf(t, s, mia.ID) == 10 })
	if xpOf(t, s, alice.ID) != 0 {
		t.Errorf("alice did not vote but got %v XP", xpOf(t, s, alice.ID))
	}

	// A select-menu poll, closed by its task after a hot reload: KV data and the task
	// scheduled before the reload survive it.
	wait(t, w.InvokeCommand(alice.ID, s.serverID, pollsCh.ID, s.appID, "poll", map[string]any{
		"question": "Snacks?", "choices": "Chips | Fruit | Cake", "style": "select", "multiple": true,
	}))
	poll2 := pollMessage(s, pollsCh.ID, "2")
	st = wait(t, w.Select(bob.ID, poll2, cid(t, "polls", "pick:2"), []string{"0", "2"}))
	if !strings.Contains(textOf(*st.Message), "**1. Chips**\n`██████░░░░░░` 1 vote (50%)") {
		t.Fatalf("select vote = %s", textOf(*st.Message))
	}

	rev, _ := s.rt.Revision()
	replaceIn(t, filepath.Join(s.dir, "bots/cicord/bot.yaml"), "defaultHours: 24", "defaultHours: 12")
	s.waitFor("new revision", func() bool { r, _ := s.rt.Revision(); return r.SHA != rev.SHA })
	if n := w.OpenSessions(); n != 1 {
		t.Errorf("open sessions after reload = %d", n)
	}
	s.rt.sched.runTasks(ctx, time.Now().Add(25*time.Hour)) // the default 24 h of poll #2 elapsed
	s.waitFor("poll closed by its task", func() bool {
		m, _ := w.Message(poll2.ID)
		return len(m.Components) == 0 && strings.Contains(textOf(m), "Closed")
	})
	s.waitFor("bonus XP after the task", func() bool { return xpOf(t, s, bob.ID) == 20 })

	// Public results route.
	resp, def := s.route("polls", "GET", "/polls/2", nil, nil)
	if def.Access != "public" || !strings.Contains(string(resp.Body), `"status":"closed"`) {
		t.Errorf("GET /polls/2 = %s (%s)", resp.Body, def.Access)
	}

	// /polls close: a subcommand whose poll option autocompletes the author's open polls
	// (an autocomplete interaction answered by the plugin, never deferred).
	wait(t, w.InvokeCommand(alice.ID, s.serverID, pollsCh.ID, s.appID, "poll", map[string]any{
		"question": "Board games?", "choices": "Yes | No",
	}))
	poll3 := pollMessage(s, pollsCh.ID, "3")
	st = wait(t, w.Autocomplete(alice.ID, s.serverID, pollsCh.ID, s.appID, "polls close", "poll", "board", nil))
	if st.ResponseType != discord.ResponseAutocomplete || !reflect.DeepEqual(st.Choices, []discord.CommandChoice{{Name: "#3 Board games?", Value: "3"}}) {
		t.Fatalf("autocomplete = %+v", st)
	}
	if st = wait(t, w.Autocomplete(bob.ID, s.serverID, pollsCh.ID, s.appID, "polls close", "poll", "", nil)); len(st.Choices) != 0 {
		t.Errorf("bob's suggestions = %+v", st.Choices)
	}
	st = settled(t, w.InvokeCommand(alice.ID, s.serverID, pollsCh.ID, s.appID, "polls close", map[string]any{"poll": "3"}),
		func(st sim.InteractionState) bool { return replyText(st) != "" })
	if !st.Ephemeral || replyText(st) != "Poll #3 is closed." {
		t.Fatalf("/polls close = %+v (%s)", st, replyText(st))
	}
	s.waitFor("poll #3 message closed", func() bool {
		m, _ := w.Message(poll3.ID)
		return len(m.Components) == 0 && strings.Contains(textOf(m), "Closed")
	})
	st = settled(t, w.InvokeCommand(bob.ID, s.serverID, pollsCh.ID, s.appID, "polls results", map[string]any{"poll": "1"}),
		func(st sim.InteractionState) bool { return replyText(st) != "" })
	if !st.Ephemeral || !strings.Contains(replyText(st), "Friday** 🏆") {
		t.Errorf("/polls results = %+v", st.Message)
	}

	// A poll manager (managerRoles: [mod]) may ping the role: exactly that role is notified.
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, pollsCh.ID, s.appID, "poll", map[string]any{
		"question": "Movie night?", "choices": "Yes | No", "ping": member.ID,
	}))
	if !strings.Contains(replyText(st), "Poll #4 posted") {
		t.Fatalf("/poll by a manager = %+v (%s)", st, replyText(st))
	}
	poll4 := pollMessage(s, pollsCh.ID, "4")
	if !strings.HasPrefix(poll4.Content, "<@&"+member.ID+">") {
		t.Errorf("manager ping = %q", poll4.Content)
	}
	if m, _ := w.MessageMentions(poll4.ID); !reflect.DeepEqual(m, sim.Mentions{RoleIDs: []string{member.ID}}) {
		t.Errorf("poll #4 notified %+v", m)
	}
}

// ───────────────────────────────────────────────────────────── levels

func TestShowcaseLevels(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		bot := filepath.Join(dir, "bots/cicord/bot.yaml")
		replaceIn(t, bot, "cooldownSeconds: 60", "cooldownSeconds: 0\n        xpPerMessage: 100")
		replaceIn(t, bot, "{ level: 5, role: regular }", "{ level: 1, role: regular, keep: false }")
		replaceIn(t, bot, "{ level: 10, role: veteran }", "{ level: 2, role: veteran }")
	}})
	w := s.world
	ctx := context.Background()
	alice := s.member("alice") // a regular member: no role at all
	bob := s.member("bob", "Member", "Moderator")
	general := s.channel("general")
	levelUps := s.channel("level-ups")
	regular, veteran := s.role("Regular"), s.role("Veteran")

	// messageCreate with MessageContent: too short messages earn nothing.
	if _, err := w.SendMessage(alice.ID, general.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	first, err := w.SendMessage(alice.ID, general.ID, "hello everyone!")
	if err != nil {
		t.Fatal(err)
	}
	s.waitMessage("level-up announcement", levelUps.ID, func(m discord.Message) bool {
		return m.Content == "GG <@"+alice.ID+">, you reached **level 1**! 🎉"
	})
	s.waitFor("🎉 reaction", func() bool { return len(w.Reactions(first.ID)["🎉"]) == 1 })
	s.waitFor("reward role", func() bool { m, _ := w.Member(s.serverID, alice.ID); return m.HasRole(regular.ID) })
	if xpOf(t, s, alice.ID) != 100 {
		t.Errorf("xp = %v", xpOf(t, s, alice.ID))
	}
	// Level 2: the keep:false reward is replaced by the next one.
	if _, err := w.SendMessage(alice.ID, general.ID, "second message"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SendMessage(alice.ID, general.ID, "third message!"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("level 2 rewards", func() bool {
		m, _ := w.Member(s.serverID, alice.ID)
		return m.HasRole(veteran.ID) && !m.HasRole(regular.ID)
	})

	// Reactions (partial message): the author gets reactionXp, removal takes it back.
	before := xpOf(t, s, alice.ID)
	if err := w.AddReaction(bob.ID, general.ID, first.ID, "👍"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("reaction XP", func() bool { return xpOf(t, s, alice.ID) == before+2 })
	if err := w.RemoveReaction(bob.ID, general.ID, first.ID, "👍"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("reaction XP removed", func() bool { return xpOf(t, s, alice.ID) == before })

	// /rank: user option, ephemeral by default, portal link button.
	st := wait(t, w.InvokeCommand(bob.ID, s.serverID, general.ID, s.appID, "rank", map[string]any{"member": alice.ID}))
	if !st.Ephemeral || !strings.Contains(replyText(st), "level **2**") {
		t.Fatalf("/rank = %+v (%s)", st, replyText(st))
	}
	link := st.Message.Components[0].Components[0]
	if link.Style != discord.ButtonLink || !strings.HasPrefix(link.URL, "http://localhost:7499/leaderboard?") || !strings.Contains(link.URL, "cicord_token=") {
		t.Errorf("leaderboard link = %+v", link)
	}

	// /top with integer and role options.
	if _, err := w.SendMessage(bob.ID, general.ID, "bob is here"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("bob XP", func() bool { return xpOf(t, s, bob.ID) == 100 })
	st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "top", map[string]any{"limit": 5, "role": s.role("Moderator").ID}))
	if got := replyText(st); st.Ephemeral || !strings.Contains(got, "Top members — Moderator") || strings.Contains(got, alice.ID) || !strings.Contains(got, bob.ID) {
		t.Errorf("/top role = %+v (%s)", st, got)
	}

	// Routes: public leaderboard (anonymous), member route with the viewer.
	resp, def := s.route("levels", "GET", "/leaderboard", nil, map[string]string{"limit": "10"})
	lb := decodeBody[struct {
		Items []struct {
			Rank        int
			UserID      string
			DisplayName string
			AvatarURL   *string
			Level, XP   int
		}
	}](t, resp)
	if def.Access != "public" || len(lb.Items) != 2 || lb.Items[0].UserID != alice.ID || lb.Items[0].Rank != 1 || lb.Items[0].DisplayName != "alice" || lb.Items[1].UserID != bob.ID {
		t.Fatalf("GET /leaderboard = %s", resp.Body)
	}
	resp, def = s.route("levels", "GET", "/me", &bob, nil)
	if def.Access != "member" || !strings.Contains(string(resp.Body), `"rank":2`) {
		t.Errorf("GET /me = %s (%s)", resp.Body, def.Access)
	}

	// Daily cron recap. The plugin reads yesterday's counters (real clock); seed them and run
	// the scheduler two days ahead so 09:00 UTC is due.
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
	if err := s.kv("levels").Set(ctx, "day:"+yesterday+":"+bob.ID, json.RawMessage("42"), 0); err != nil {
		t.Fatal(err)
	}
	s.rt.sched.runCron(ctx, time.Now().UTC().Add(48*time.Hour))
	recap := s.waitMessage("recap", levelUps.ID, func(m discord.Message) bool {
		return strings.Contains(textOf(m), "Yesterday's most active members")
	})
	if !strings.Contains(textOf(recap), "<@"+bob.ID+"> — +42 XP") {
		t.Errorf("recap = %s", textOf(recap))
	}
}

// ───────────────────────────────────────────────────────────── moderation (+ http.fetch)

func TestShowcaseModeration(t *testing.T) {
	// The webhook host is allow-listed in plugin.yaml (hooks.slack.com); the test client
	// sends every request to a local TLS server instead.
	var hookMu sync.Mutex
	var hooks []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hookMu.Lock()
		hooks = append(hooks, r.Host+r.URL.Path+" "+string(body))
		hookMu.Unlock()
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := srv.Client()
	tr := client.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	client = &http.Client{Transport: tr}

	s := startShowcase(t, showcaseOpts{httpClient: client, prepare: func(dir string) {
		bot := filepath.Join(dir, "bots/cicord/bot.yaml")
		replaceIn(t, bot, "warningDays: 30", "warningDays: 0.00002") // ≈ 2 s
		replaceIn(t, bot, "{ warnings: 3, action: timeout, durationMinutes: 60 }", "{ warnings: 2, action: timeout }")
		replaceIn(t, bot, "flood:\n          exemptRoles: [mod, admin]", "flood:\n          messages: 4\n          exemptRoles: [mod, admin]\n        webhook:\n          url: https://hooks.slack.com/services/T0/B0/x")
	}})
	w := s.world
	mia := s.member("mia", "Moderator")
	troll := s.member("troll") // regular member, no role
	general := s.channel("general")
	modLog := s.channel("mod-log")
	logged := func(what string) discord.Message {
		return s.waitMessage(what, modLog.ID, func(m discord.Message) bool { return strings.Contains(textOf(m), what) })
	}
	// Member events: moderation 0.3 leaves them to audit-log when it is installed (its
	// legacyMemberLog feature), as in the showcase.
	auditLog := s.channel("audit-log")
	audited := func(what string) {
		s.waitMessage(what, auditLog.ID, func(m discord.Message) bool { return strings.Contains(textOf(m), what) })
	}
	audited("**troll** joined") // memberJoin

	// /warn requires ModerateMembers.
	if h := w.InvokeCommand(troll.ID, s.serverID, general.ID, s.appID, "warn", map[string]any{"member": mia.ID, "reason": "x"}); h.Err() == nil {
		t.Fatal("a member could use /warn")
	}
	// Discord only checks the bot's role hierarchy: the plugin refuses a moderator acting on
	// staff (spec.access admins/moderators) or on a member ranked at or above them.
	boss := s.member("boss", "Admin")
	st := wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "kick", map[string]any{"member": boss.ID}))
	if !st.Ephemeral || !strings.Contains(replyText(st), "is staff (admin access): only an admin can moderate them.") {
		t.Fatalf("/kick of an admin by a moderator = %+v (%s)", st, replyText(st))
	}
	if _, ok := w.Member(s.serverID, boss.ID); !ok {
		t.Fatal("the admin was kicked by a moderator")
	}
	// /warn with an attachment option: case in the audit log, DM to the member, webhook.
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "warn", map[string]any{
		"member": troll.ID, "reason": "spam", "evidence": "proof.png",
	}))
	if !st.Ephemeral || !strings.Contains(replyText(st), "was warned (case #1). Active warnings: 1.") {
		t.Fatalf("/warn = %+v (%s)", st, replyText(st))
	}
	warnCase := logged("Case #1 · Warning")
	if len(warnCase.Embeds) == 0 || !strings.HasSuffix(warnCase.Embeds[0].ImageURL, "/proof.png") {
		t.Errorf("evidence not in the case: %+v", warnCase.Embeds)
	}
	s.waitFor("warning DM", func() bool {
		for _, m := range w.DMs(troll.ID, s.bot.ID) {
			if strings.Contains(m.Content, "You received a warning on **Cicord Showcase**: spam") {
				return true
			}
		}
		return false
	})
	s.waitFor("webhook", func() bool {
		hookMu.Lock()
		defer hookMu.Unlock()
		// Moderation 0.3 sends only the case number and action to external webhooks.
		return len(hooks) > 0 && strings.Contains(hooks[0], "hooks.slack.com/services/T0/B0/x") && strings.Contains(hooks[0], `{"text":"Case #1 · Warning"}`)
	})

	// Second warning: automatic timeout (escalation rule with the durationMinutes default).
	wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "warn", map[string]any{"member": troll.ID, "reason": "again"}))
	logged("Case #3 · Timeout")
	m, _ := w.Member(s.serverID, troll.ID)
	if m.TimeoutUntil == nil || time.Until(*m.TimeoutUntil) < 50*time.Minute {
		t.Errorf("escalation timeout = %v", m.TimeoutUntil)
	}
	resp, def := s.route("moderation", "GET", "/warnings/"+troll.ID, &mia, nil)
	if def.Access != "moderator" || strings.Count(string(resp.Body), `"case"`) != 2 {
		t.Errorf("GET /warnings = %s", resp.Body)
	}
	// KV TTL: the warnings expire.
	s.waitFor("warnings expired", func() bool {
		st := wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "warnings", map[string]any{"member": troll.ID}))
		return strings.Contains(replyText(st), "has no active warnings")
	})
	_, def = s.route("moderation", "DELETE", "/warnings/"+troll.ID, &mia, nil)
	if def.Access != "admin" {
		t.Errorf("DELETE /warnings access = %s", def.Access)
	}

	// /timeout with integer choices (0 = remove).
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "timeout", map[string]any{"member": troll.ID, "duration": 0}))
	if !strings.Contains(replyText(st), "was removed") {
		t.Errorf("/timeout 0 = %s", replyText(st))
	}
	s.waitFor("timeout removed", func() bool {
		m, _ := w.Member(s.serverID, troll.ID)
		return m.TimeoutUntil == nil || m.TimeoutUntil.Before(time.Now())
	})

	// memberUpdate: nickname change logged.
	nick := "Nice Guy"
	if _, err := w.AdminUpdateMember(s.serverID, troll.ID, discord.MemberParams{Nick: &nick}); err != nil {
		t.Fatal(err)
	}
	audited("→ Nice Guy")

	// Flood protection (messageCreate without MessageContent): timeout + message deleted.
	var last discord.Message
	for i := 0; i < 4; i++ {
		msg, err := w.SendMessage(troll.ID, general.ID, "spam spam")
		if err != nil {
			t.Fatal(err)
		}
		last = msg
	}
	logged("4 messages in 5 s") // automatic timeout case
	s.waitFor("flood message deleted", func() bool { _, ok := w.Message(last.ID); return !ok })

	// /kick: the DM is sent before the kick; memberLeave is logged.
	wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "timeout", map[string]any{"member": troll.ID, "duration": 0}))
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "kick", map[string]any{"member": troll.ID, "reason": "bye"}))
	if !strings.Contains(replyText(st), "was kicked") {
		t.Fatalf("/kick = %s", replyText(st))
	}
	s.waitFor("kicked", func() bool { _, ok := w.Member(s.serverID, troll.ID); return !ok })
	audited("**troll** left the server.")
	found := false
	for _, m := range w.DMs(troll.ID, s.bot.ID) {
		found = found || strings.Contains(m.Content, "You were kicked from **Cicord Showcase**: bye")
	}
	if !found {
		t.Error("no DM before the kick")
	}

	// /ban with a boolean option.
	villain := s.member("villain", "Member")
	wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "ban", map[string]any{"member": villain.ID, "reason": "raid", "delete_messages": true}))
	s.waitFor("banned", func() bool {
		for _, id := range w.Bans(s.serverID) {
			if id == villain.ID {
				return true
			}
		}
		return false
	})
}

// A plugin that exhausts its memory limit, or ignores its CPU budget until the hard stop,
// crashes its instance only: the instance is reported Degraded, restarted with backoff, and
// serves again; the process and the other plugins are unaffected.
func TestShowcaseProbeCrashRestarts(t *testing.T) {
	oldMin := restartMin
	restartMin = 50 * time.Millisecond // copied by New
	t.Cleanup(func() { restartMin = oldMin })
	s := startShowcase(t, showcaseOpts{probe: true, limits: plugin.Limits{Memory: 16 << 20}})
	w := s.world
	alice := s.member("alice", "Member")
	general := s.channel("general")

	probe := func() InstanceInfo {
		for _, in := range s.rt.Instances("") {
			if in.Config.Name == "probe" {
				return in
			}
		}
		t.Fatal("no probe instance")
		return InstanceInfo{}
	}
	for cmd, reason := range map[string]string{"probe-oom": "memory limit", "probe-kill": "killed"} {
		st := wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, cmd, nil))
		if replyText(st) != plugin.GenericErrorMessage {
			t.Errorf("%s = %+v", cmd, st)
		}
		s.waitFor(cmd+": probe degraded", func() bool {
			in := probe()
			return in.Health == HealthDegraded && strings.Contains(in.Message, reason)
		})
		s.waitFor(cmd+": probe restarted", func() bool { return probe().Health == HealthHealthy })
		st = wait(t, w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "probe-ping", nil))
		if replyText(st) != "pong" {
			t.Errorf("probe-ping after %s = %+v", cmd, st)
		}
	}
	if !strings.Contains(s.logs.String(), "plugin instance restarted") {
		t.Error("the restart is not logged")
	}
	m := scrapeMetrics(t, s.rt.opts.Metrics)
	var probeLabels string
	for series := range m {
		if rest, ok := strings.CutPrefix(series, "cicord_plugin_instance_running{"); ok && strings.Contains(rest, `plugin_instance="probe"`) {
			probeLabels = strings.TrimSuffix(rest, "}")
		}
	}
	if probeLabels == "" {
		t.Fatal("no cicord_plugin_instance_running series for the probe")
	}
	for series, want := range map[string]float64{
		"cicord_plugin_instance_crashes_total{" + probeLabels + "}":                     2,
		"cicord_plugin_instance_restarts_total{" + probeLabels + `,result="succeeded"}`: 2,
		"cicord_plugin_instance_running{" + probeLabels + "}":                           1,
	} {
		if m[series] != want {
			t.Errorf("%s = %v, want %v", series, m[series], want)
		}
	}
	if m["cicord_plugin_handler_errors_total{"+probeLabels+`,kind="command"}`] < 2 {
		t.Error("the crashing commands are not counted as handler errors")
	}
}
