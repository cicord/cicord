package sim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/npjonath/codi/internal/discord"
)

var ctx = context.Background()

// recorder collects events delivered to a session and optionally forwards them.
type recorder struct {
	mu     sync.Mutex
	events []discord.Event
	next   func(discord.Event)
}

func (r *recorder) handle(ev discord.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	next := r.next
	r.mu.Unlock()
	if next != nil {
		next(ev)
	}
}

func (r *recorder) all() []discord.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]discord.Event{}, r.events...)
}

// resetEvents waits for queued events, then forgets every recorded event.
func (f *fixture) resetEvents() {
	f.w.WaitIdle()
	f.rec.reset()
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

func eventsOf[T discord.Event](r *recorder) []T {
	var out []T
	for _, ev := range r.all() {
		if e, ok := ev.(T); ok {
			out = append(out, e)
		}
	}
	return out
}

type fixture struct {
	w       *World
	owner   discord.User
	alice   discord.User
	bot     discord.User
	token   string
	appID   string
	srv     discord.Server
	general discord.Channel
	botRole discord.Role
	sess    *Session
	rec     *recorder
}

type fixtureOpts struct {
	perms   discord.Permissions
	intents discord.Intents
	handler func(f *fixture, ev discord.Event)
	world   []Option
	noOpen  bool
}

func newFixture(t *testing.T, o fixtureOpts) *fixture {
	t.Helper()
	w := NewWorld(o.world...)
	f := &fixture{w: w, rec: &recorder{}}
	f.owner = w.CreateUser("owner")
	f.alice = w.CreateUser("alice")
	f.token, f.bot, f.appID = w.CreateBot("codi")
	f.srv = w.CreateServer("Test", f.owner.ID, WithGeneralChannel())
	if err := w.AddMember(f.srv.ID, f.alice.ID); err != nil {
		t.Fatal(err)
	}
	perms := o.perms
	if perms == 0 {
		perms = discord.MustPermissions("Administrator")
	}
	role, err := w.AddBotToServer(f.srv.ID, f.bot.ID, perms)
	if err != nil {
		t.Fatal(err)
	}
	f.botRole = role
	f.general, _ = w.ChannelByName(f.srv.ID, "general")
	f.sess = w.MustConnect(f.token)
	if o.handler != nil {
		f.rec.next = func(ev discord.Event) { o.handler(f, ev) }
	}
	if !o.noOpen {
		intents := o.intents
		if intents == 0 {
			intents = discord.MustIntents("Servers", "ServerMembers", "ServerMessages", "ServerMessageReactions", "DirectMessages", "DirectMessageReactions", "MessageContent")
		}
		if err := f.sess.Open(ctx, intents, f.rec.handle); err != nil {
			t.Fatal(err)
		}
		w.WaitIdle()
	}
	t.Cleanup(func() { _ = f.sess.Close(ctx) })
	return f
}

func isErr(t *testing.T, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// expectErr is isErr for use inside session handlers: it must not call t.Fatal,
// which would stop the dispatcher goroutine instead of the test.
func expectErr(t *testing.T, err, want error) {
	t.Helper()
	if want == nil && err != nil || want != nil && !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

func ptr[T any](v T) *T { return &v }

// ───────────────────────────────────────────────────────────── connector & gateway

func TestConnectInvalidToken(t *testing.T) {
	w := NewWorld()
	_, err := w.Connect(ctx, "nope")
	isErr(t, err, discord.ErrInvalidToken)
}

func TestReadyEventFirst(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	evs := f.rec.all()
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	ready, ok := evs[0].(discord.ReadyEvent)
	if !ok {
		t.Fatalf("first event = %T, want ReadyEvent", evs[0])
	}
	if ready.User.ID != f.bot.ID || len(ready.ServerIDs) != 1 || ready.ServerIDs[0] != f.srv.ID {
		t.Fatalf("ready = %+v", ready)
	}
	if f.sess.Self().ID != f.bot.ID || f.sess.ApplicationID() != f.appID {
		t.Fatal("Self/ApplicationID mismatch")
	}
	isErr(t, f.sess.Open(ctx, 0, f.rec.handle), discord.ErrBadRequest) // already open
}

func TestPrivilegedIntentsMustBeEnabled(t *testing.T) {
	f := newFixture(t, fixtureOpts{noOpen: true})
	if err := f.w.SetPrivilegedIntents(f.appID, 0); err != nil {
		t.Fatal(err)
	}
	err := f.sess.Open(ctx, discord.MustIntents("Servers", "MessageContent"), f.rec.handle)
	isErr(t, err, discord.ErrForbidden)
	isErr(t, f.sess.Open(ctx, discord.MustIntents("Servers"), f.rec.handle), nil)
}

func TestIntentFiltering(t *testing.T) {
	type action func(f *fixture)
	sendMsg := func(f *fixture) { _, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "hi") }
	cases := []struct {
		name    string
		intents []string
		act     action
		want    func(r *recorder) int
		expect  int
	}{
		{"member join needs ServerMembers", []string{"Servers"}, func(f *fixture) {
			_ = f.w.AddMember(f.srv.ID, f.w.CreateUser("bob").ID)
		}, func(r *recorder) int { return len(eventsOf[discord.MemberJoinEvent](r)) }, 0},
		{"member join with ServerMembers", []string{"ServerMembers"}, func(f *fixture) {
			_ = f.w.AddMember(f.srv.ID, f.w.CreateUser("bob").ID)
		}, func(r *recorder) int { return len(eventsOf[discord.MemberJoinEvent](r)) }, 1},
		{"member leave with ServerMembers", []string{"ServerMembers"}, func(f *fixture) {
			_ = f.w.RemoveMember(f.srv.ID, f.alice.ID)
		}, func(r *recorder) int { return len(eventsOf[discord.MemberLeaveEvent](r)) }, 1},
		{"message needs ServerMessages", []string{"Servers"}, sendMsg,
			func(r *recorder) int { return len(eventsOf[discord.MessageCreateEvent](r)) }, 0},
		{"message with ServerMessages", []string{"ServerMessages"}, sendMsg,
			func(r *recorder) int { return len(eventsOf[discord.MessageCreateEvent](r)) }, 1},
		{"DM message needs DirectMessages", []string{"ServerMessages"}, func(f *fixture) {
			dm, _ := f.w.DMChannel(f.alice.ID, f.bot.ID)
			_, _ = f.w.SendMessage(f.alice.ID, dm.ID, "psst")
		}, func(r *recorder) int { return len(eventsOf[discord.MessageCreateEvent](r)) }, 0},
		{"DM message with DirectMessages", []string{"DirectMessages"}, func(f *fixture) {
			dm, _ := f.w.DMChannel(f.alice.ID, f.bot.ID)
			_, _ = f.w.SendMessage(f.alice.ID, dm.ID, "psst")
		}, func(r *recorder) int { return len(eventsOf[discord.MessageCreateEvent](r)) }, 1},
		{"reaction needs ServerMessageReactions", []string{"ServerMessages"}, func(f *fixture) {
			m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "hi")
			_ = f.w.AddReaction(f.alice.ID, f.general.ID, m.ID, "👍")
		}, func(r *recorder) int { return len(eventsOf[discord.ReactionAddEvent](r)) }, 0},
		{"reaction with ServerMessageReactions", []string{"ServerMessageReactions"}, func(f *fixture) {
			m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "hi")
			_ = f.w.AddReaction(f.alice.ID, f.general.ID, m.ID, "👍")
		}, func(r *recorder) int { return len(eventsOf[discord.ReactionAddEvent](r)) }, 1},
		{"role events need Servers", []string{"ServerMembers"}, func(f *fixture) {
			_, _ = f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("x")})
		}, func(r *recorder) int { return len(eventsOf[discord.RoleCreateEvent](r)) }, 0},
		{"role events with Servers", []string{"Servers"}, func(f *fixture) {
			_, _ = f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("x")})
		}, func(r *recorder) int { return len(eventsOf[discord.RoleCreateEvent](r)) }, 1},
		{"channel events with Servers", []string{"Servers"}, func(f *fixture) {
			_, _ = f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Topic: ptr("t")})
		}, func(r *recorder) int { return len(eventsOf[discord.ChannelUpdateEvent](r)) }, 1},
		{"server update with Servers", []string{"Servers"}, func(f *fixture) {
			_, _ = f.w.AdminUpdateServer(f.srv.ID, discord.ServerParams{Name: ptr("New")})
		}, func(r *recorder) int { return len(eventsOf[discord.ServerUpdateEvent](r)) }, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, fixtureOpts{intents: discord.MustIntents(tc.intents...)})
			tc.act(f)
			f.w.WaitIdle()
			if got := tc.want(f.rec); got != tc.expect {
				t.Fatalf("got %d events, want %d", got, tc.expect)
			}
		})
	}
}

func TestEventsOnlyForServersTheBotIsIn(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	other := f.w.CreateServer("Other", f.owner.ID, WithGeneralChannel())
	gen, _ := f.w.ChannelByName(other.ID, "general")
	_, _ = f.w.SendMessage(f.owner.ID, gen.ID, "elsewhere")
	_, _ = f.w.AdminCreateRole(other.ID, discord.RoleParams{})
	f.w.WaitIdle()
	for _, ev := range f.rec.all() {
		if discord.ServerIDOf(ev) == other.ID {
			t.Fatalf("received %T for a server the bot is not in", ev)
		}
	}
	_, err := f.sess.GetServer(ctx, other.ID)
	isErr(t, err, discord.ErrForbidden)
}

func TestMessageContentGating(t *testing.T) {
	cases := []struct {
		name    string
		intents []string
		content func(f *fixture) string
		dm      bool
		visible bool
	}{
		{"no intent", []string{"ServerMessages"}, func(*fixture) string { return "hello" }, false, false},
		{"MessageContent", []string{"ServerMessages", "MessageContent"}, func(*fixture) string { return "hello" }, false, true},
		{"mention", []string{"ServerMessages"}, func(f *fixture) string { return "hey <@" + f.bot.ID + ">" }, false, true},
		{"nick mention", []string{"ServerMessages"}, func(f *fixture) string { return "hey <@!" + f.bot.ID + ">" }, false, true},
		{"DM", []string{"DirectMessages"}, func(*fixture) string { return "hello" }, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, fixtureOpts{intents: discord.MustIntents(tc.intents...)})
			ch := f.general.ID
			if tc.dm {
				dm, _ := f.w.DMChannel(f.alice.ID, f.bot.ID)
				ch = dm.ID
			}
			want := tc.content(f)
			if _, err := f.w.SendMessage(f.alice.ID, ch, want); err != nil {
				t.Fatal(err)
			}
			f.w.WaitIdle()
			evs := eventsOf[discord.MessageCreateEvent](f.rec)
			if len(evs) != 1 {
				t.Fatalf("got %d message events", len(evs))
			}
			got := evs[0].Message.Content
			if tc.visible && got != want || !tc.visible && got != "" {
				t.Fatalf("content = %q (visible=%v)", got, tc.visible)
			}
		})
	}
}

func TestOwnMessagesAreDeliveredWithContent(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("ServerMessages")})
	if _, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "mine"}); err != nil {
		t.Fatal(err)
	}
	f.w.WaitIdle()
	evs := eventsOf[discord.MessageCreateEvent](f.rec)
	if len(evs) != 1 || evs[0].Message.Content != "mine" || evs[0].Message.Author.ID != f.bot.ID {
		t.Fatalf("events = %+v", evs)
	}
}

func TestRESTContentGatingFollowsPortalSetting(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "secret")
	msgs, err := f.sess.ListMessages(ctx, f.general.ID, discord.ListMessagesParams{})
	isErr(t, err, nil)
	if msgs[0].Content != "secret" {
		t.Fatalf("content = %q", msgs[0].Content)
	}
	_ = f.w.SetPrivilegedIntents(f.appID, 0)
	msgs, _ = f.sess.ListMessages(ctx, f.general.ID, discord.ListMessagesParams{})
	if msgs[0].Content != "" {
		t.Fatalf("content should be redacted, got %q", msgs[0].Content)
	}
}

func TestHiddenChannelGetsNoMessageEvents(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ViewChannel", "SendMessages")})
	hidden, err := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{
		Name: ptr("staff"), Type: discord.ChannelText,
		Overwrites: &[]discord.Overwrite{{ID: f.botRole.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")}},
	})
	isErr(t, err, nil)
	_, _ = f.w.SendMessage(f.owner.ID, hidden.ID, "staff only")
	_, _ = f.w.SendMessage(f.owner.ID, f.general.ID, "public")
	f.w.WaitIdle()
	evs := eventsOf[discord.MessageCreateEvent](f.rec)
	if len(evs) != 1 || evs[0].Message.ChannelID != f.general.ID {
		t.Fatalf("events = %+v", evs)
	}
	_, err = f.sess.SendMessage(ctx, hidden.ID, discord.MessageCreate{Content: "x"})
	isErr(t, err, discord.ErrForbidden)
	_, err = f.sess.GetChannel(ctx, hidden.ID)
	isErr(t, err, discord.ErrForbidden)
	chans, _ := f.sess.ListChannels(ctx, f.srv.ID)
	if len(chans) != 2 {
		t.Fatalf("ListChannels should list hidden channels too, got %d", len(chans))
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	isErr(t, f.sess.Close(ctx), nil)
	f.resetEvents()
	_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "hi")
	f.w.WaitIdle()
	if n := len(f.rec.all()); n != 0 {
		t.Fatalf("got %d events after Close", n)
	}
	if f.w.OpenSessions() != 0 {
		t.Fatal("session still registered")
	}
}

func TestWaitIdleWaitsForCascades(t *testing.T) {
	// The handler echoes every human message; the echo produces another event.
	f := newFixture(t, fixtureOpts{handler: func(f *fixture, ev discord.Event) {
		if m, ok := ev.(discord.MessageCreateEvent); ok && !m.Message.Author.Bot {
			time.Sleep(5 * time.Millisecond)
			_, _ = f.sess.SendMessage(ctx, m.Message.ChannelID, discord.MessageCreate{Content: "echo: " + m.Message.Content})
		}
	}})
	for i := 0; i < 5; i++ {
		_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, fmt.Sprint(i))
	}
	f.w.WaitIdle()
	msgs := f.w.Messages(f.general.ID)
	if len(msgs) != 10 {
		t.Fatalf("got %d messages, want 10", len(msgs))
	}
	if got := len(eventsOf[discord.MessageCreateEvent](f.rec)); got != 10 {
		t.Fatalf("bot saw %d message events (own included), want 10", got)
	}
	if msgs[9].Content != "echo: 4" {
		t.Fatalf("last = %q", msgs[9].Content)
	}
}

func TestConcurrentUse(t *testing.T) {
	f := newFixture(t, fixtureOpts{handler: func(f *fixture, ev discord.Event) {
		if m, ok := ev.(discord.MessageCreateEvent); ok && !m.Message.Author.Bot {
			_ = f.sess.AddReaction(ctx, m.Message.ChannelID, m.Message.ID, "✅")
		}
	}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, fmt.Sprint(i, j))
				_, _ = f.sess.ListRoles(ctx, f.srv.ID)
				_ = f.w.Channels(f.srv.ID)
			}
		}(i)
	}
	wg.Wait()
	f.w.WaitIdle()
	for _, m := range f.w.Messages(f.general.ID) {
		if len(f.w.Reactions(m.ID)["✅"]) != 1 {
			t.Fatalf("message %s not reacted", m.ID)
		}
	}
}

// ───────────────────────────────────────────────────────────── permissions & hierarchy

func TestPermissionEnforcement(t *testing.T) {
	type call func(f *fixture) error
	cases := []struct {
		name  string
		perms []string
		call  call
		want  error
	}{
		{"CreateRole without ManageRoles", []string{"ViewChannel"}, func(f *fixture) error {
			_, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{}, "")
			return err
		}, discord.ErrForbidden},
		{"CreateRole with ManageRoles", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Permissions: ptr(discord.Permissions(0))}, "")
			return err
		}, nil},
		{"CreateRole granting missing permission", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Permissions: ptr(discord.MustPermissions("BanMembers"))}, "")
			return err
		}, discord.ErrForbidden},
		{"CreateChannel without ManageChannels", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x")}, "")
			return err
		}, discord.ErrForbidden},
		{"CreateChannel with ManageChannels", []string{"ManageChannels"}, func(f *fixture) error {
			_, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x")}, "")
			return err
		}, nil},
		{"UpdateChannel without ManageChannels", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{Topic: ptr("t")}, "")
			return err
		}, discord.ErrForbidden},
		{"DeleteChannel without ManageChannels", []string{"ManageRoles"}, func(f *fixture) error {
			return f.sess.DeleteChannel(ctx, f.general.ID, "")
		}, discord.ErrForbidden},
		{"UpdateServer without ManageServer", []string{"ManageChannels"}, func(f *fixture) error {
			_, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Name: ptr("x y")})
			return err
		}, discord.ErrForbidden},
		{"UpdateServer with ManageServer", []string{"ManageServer"}, func(f *fixture) error {
			_, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Name: ptr("x y")})
			return err
		}, nil},
		{"Kick without KickMembers", []string{"BanMembers"}, func(f *fixture) error {
			return f.sess.KickMember(ctx, f.srv.ID, f.alice.ID, "")
		}, discord.ErrForbidden},
		{"Kick with KickMembers", []string{"KickMembers"}, func(f *fixture) error {
			return f.sess.KickMember(ctx, f.srv.ID, f.alice.ID, "")
		}, nil},
		{"Ban without BanMembers", []string{"KickMembers"}, func(f *fixture) error {
			return f.sess.BanMember(ctx, f.srv.ID, f.alice.ID, 0, "")
		}, discord.ErrForbidden},
		{"Ban with BanMembers", []string{"BanMembers"}, func(f *fixture) error {
			return f.sess.BanMember(ctx, f.srv.ID, f.alice.ID, 0, "")
		}, nil},
		{"Timeout without ModerateMembers", []string{"KickMembers"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: ptr(time.Now().Add(time.Hour))}, "")
			return err
		}, discord.ErrForbidden},
		{"Timeout with ModerateMembers", []string{"ModerateMembers"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: ptr(time.Now().Add(time.Hour))}, "")
			return err
		}, nil},
		{"Timeout over 28 days", []string{"ModerateMembers"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: ptr(time.Now().Add(29 * 24 * time.Hour))}, "")
			return err
		}, discord.ErrBadRequest},
		{"Nick without ManageNicknames", []string{"KickMembers"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{Nick: ptr("al")}, "")
			return err
		}, discord.ErrForbidden},
		{"Nick with ManageNicknames", []string{"ManageNicknames"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{Nick: ptr("al")}, "")
			return err
		}, nil},
		{"Own nick with ChangeNickname (from @everyone)", []string{"KickMembers"}, func(f *fixture) error {
			_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.bot.ID, discord.MemberParams{Nick: ptr("me")}, "")
			return err
		}, nil},
		{"SendMessage denied by @everyone overwrite", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Overwrites: &[]discord.Overwrite{
				{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")},
			}})
			if err != nil {
				return err
			}
			_, err = f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "x"})
			return err
		}, discord.ErrForbidden},
		{"Member overwrite allow beats role deny", []string{"ManageRoles"}, func(f *fixture) error {
			_, err := f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Overwrites: &[]discord.Overwrite{
				{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")},
				{ID: f.bot.ID, Type: discord.OverwriteMember, Allow: discord.MustPermissions("SendMessages")},
			}})
			if err != nil {
				return err
			}
			_, err = f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "x"})
			return err
		}, nil},
		{"Administrator bypasses overwrites", []string{"Administrator"}, func(f *fixture) error {
			_, err := f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Overwrites: &[]discord.Overwrite{
				{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel", "SendMessages")},
			}})
			if err != nil {
				return err
			}
			_, err = f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "x"})
			return err
		}, nil},
		{"DeleteMessage of others needs ManageMessages", []string{"ViewChannel"}, func(f *fixture) error {
			m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "x")
			return f.sess.DeleteMessage(ctx, f.general.ID, m.ID, "")
		}, discord.ErrForbidden},
		{"EditMessage of others is forbidden", []string{"Administrator"}, func(f *fixture) error {
			m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "x")
			_, err := f.sess.EditMessage(ctx, f.general.ID, m.ID, discord.MessageCreate{Content: "y"})
			return err
		}, discord.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, fixtureOpts{perms: discord.MustPermissions(tc.perms...)})
			isErr(t, tc.call(f), tc.want)
		})
	}
}

func TestRoleHierarchy(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ManageRoles", "KickMembers")})
	// The owner creates "top" and moves it above the bot's role.
	top, err := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("top")})
	isErr(t, err, nil)
	isErr(t, f.w.AdminSetRolePositions(f.srv.ID, []discord.RolePosition{{ID: top.ID, Position: 2}}), nil)
	low, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("low"), Permissions: ptr(discord.Permissions(0))}, "")
	isErr(t, err, nil)

	botRole, _ := f.w.Role(f.srv.ID, f.botRole.ID)
	topNow, _ := f.w.Role(f.srv.ID, top.ID)
	if topNow.Position <= botRole.Position {
		t.Fatalf("top=%d bot=%d", topNow.Position, botRole.Position)
	}

	_, err = f.sess.UpdateRole(ctx, f.srv.ID, top.ID, discord.RoleParams{Name: ptr("x")}, "")
	isErr(t, err, discord.ErrForbidden)
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, top.ID, ""), discord.ErrForbidden)
	isErr(t, f.sess.AddMemberRole(ctx, f.srv.ID, f.alice.ID, top.ID, ""), discord.ErrForbidden)
	// Its own (managed) role: at its own position, and managed.
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, f.botRole.ID, ""), discord.ErrForbidden)
	isErr(t, f.w.AdminAddMemberRole(f.srv.ID, f.alice.ID, f.botRole.ID), discord.ErrForbidden)
	isErr(t, f.w.AdminDeleteRole(f.srv.ID, f.botRole.ID), discord.ErrForbidden)
	// @everyone.
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, f.srv.ID, ""), discord.ErrBadRequest)
	isErr(t, f.sess.AddMemberRole(ctx, f.srv.ID, f.alice.ID, f.srv.ID, ""), discord.ErrBadRequest)
	// Lower roles are fine.
	isErr(t, f.sess.AddMemberRole(ctx, f.srv.ID, f.alice.ID, low.ID, ""), nil)
	_, err = f.sess.UpdateRole(ctx, f.srv.ID, low.ID, discord.RoleParams{Color: ptr(0xff0000)}, "")
	isErr(t, err, nil)
	// Cannot grant a permission the bot lacks.
	_, err = f.sess.UpdateRole(ctx, f.srv.ID, low.ID, discord.RoleParams{Permissions: ptr(discord.MustPermissions("BanMembers"))}, "")
	isErr(t, err, discord.ErrForbidden)
	// Members: alice with "top" outranks the bot; the owner is untouchable.
	isErr(t, f.w.AdminAddMemberRole(f.srv.ID, f.alice.ID, top.ID), nil)
	isErr(t, f.sess.KickMember(ctx, f.srv.ID, f.alice.ID, ""), discord.ErrForbidden)
	isErr(t, f.sess.KickMember(ctx, f.srv.ID, f.owner.ID, ""), discord.ErrForbidden)
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, low.ID, ""), nil)
	if m, _ := f.w.Member(f.srv.ID, f.alice.ID); m.HasRole(low.ID) {
		t.Fatal("deleted role still assigned")
	}
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, "123", ""), discord.ErrNotFound)
}

func TestRolePositions(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ManageRoles")})
	names := func() []string {
		var out []string
		for _, r := range f.w.Roles(f.srv.ID) {
			out = append(out, fmt.Sprintf("%s:%d", r.Name, r.Position))
		}
		return out
	}
	a, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a")}, "")
	b, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("b")}, "")
	c, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("c")}, "")
	// New roles land at position 1, pushing the others up; the bot role stays on top.
	if got, want := fmt.Sprint(names()), "[@everyone:0 c:1 b:2 a:3 codi:4]"; got != want {
		t.Fatalf("roles = %s, want %s", got, want)
	}
	f.resetEvents()
	isErr(t, f.sess.SetRolePositions(ctx, f.srv.ID, []discord.RolePosition{{ID: c.ID, Position: 3}}), nil)
	if got, want := fmt.Sprint(names()), "[@everyone:0 b:1 a:2 c:3 codi:4]"; got != want {
		t.Fatalf("roles = %s, want %s", got, want)
	}
	f.w.WaitIdle()
	if n := len(eventsOf[discord.RoleUpdateEvent](f.rec)); n != 3 {
		t.Fatalf("got %d RoleUpdate events, want 3", n)
	}
	// Out-of-range positions are clamped and normalized.
	isErr(t, f.sess.SetRolePositions(ctx, f.srv.ID, []discord.RolePosition{{ID: a.ID, Position: -5}}), nil)
	if got, want := fmt.Sprint(names()), "[@everyone:0 a:1 b:2 c:3 codi:4]"; got != want {
		t.Fatalf("roles = %s, want %s", got, want)
	}
	// Cannot move a role to or above the bot's own role, nor move @everyone.
	isErr(t, f.sess.SetRolePositions(ctx, f.srv.ID, []discord.RolePosition{{ID: b.ID, Position: 4}}), discord.ErrForbidden)
	isErr(t, f.sess.SetRolePositions(ctx, f.srv.ID, []discord.RolePosition{{ID: f.srv.ID, Position: 2}}), discord.ErrBadRequest)
	isErr(t, f.sess.SetRolePositions(ctx, f.srv.ID, []discord.RolePosition{{ID: b.ID, Position: 1}, {ID: b.ID, Position: 2}}), discord.ErrBadRequest)
}

func TestManagedRoleLifecycle(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ManageRoles")})
	r, ok := f.w.Role(f.srv.ID, f.botRole.ID)
	if !ok || !r.Managed || r.Name != "codi" || r.Position != len(f.w.Roles(f.srv.ID))-1 {
		t.Fatalf("managed role = %+v", r)
	}
	if m, _ := f.w.Member(f.srv.ID, f.bot.ID); !m.HasRole(r.ID) {
		t.Fatal("bot lacks its managed role")
	}
	isErr(t, f.w.RemoveMember(f.srv.ID, f.bot.ID), nil)
	if _, ok := f.w.Role(f.srv.ID, r.ID); ok {
		t.Fatal("managed role should be deleted when the bot leaves")
	}
	_, err := f.sess.ListRoles(ctx, f.srv.ID)
	isErr(t, err, discord.ErrForbidden)
}

// ───────────────────────────────────────────────────────────── channels

func TestChannelCreateNormalizesNamesAndDefaults(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ch, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("My  Cool Channel"), Type: discord.ChannelText}, "")
	isErr(t, err, nil)
	if ch.Name != "my-cool-channel" {
		t.Fatalf("name = %q", ch.Name)
	}
	v, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("Voice Room"), Type: discord.ChannelVoice}, "")
	if v.Name != "Voice Room" || v.Bitrate != 64000 {
		t.Fatalf("voice = %+v", v)
	}
	for _, p := range []discord.ChannelParams{
		{Type: discord.ChannelText},
		{Name: ptr(""), Type: discord.ChannelText},
		{Name: ptr("x"), Type: discord.ChannelDM},
		{Name: ptr("x"), Type: discord.ChannelPublicThread},
		{Name: ptr("x"), Type: discord.ChannelText, ParentID: ptr(f.general.ID)},
		{Name: ptr("x"), Type: discord.ChannelText, RateLimitPerUser: ptr(99999)},
		{Name: ptr("x"), Type: discord.ChannelVoice, UserLimit: ptr(100)},
	} {
		_, err := f.sess.CreateChannel(ctx, f.srv.ID, p, "")
		isErr(t, err, discord.ErrBadRequest)
	}
}

func TestChannelPositionsAndCategories(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	cat, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("Info"), Type: discord.ChannelCategory}, "")
	a, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("a"), ParentID: &cat.ID}, "")
	b, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("b"), ParentID: &cat.ID}, "")
	c, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("c"), ParentID: &cat.ID}, "")
	order := func() string {
		var out []string
		for _, ch := range f.w.Channels(f.srv.ID) {
			out = append(out, fmt.Sprintf("%s:%d", ch.Name, ch.Position))
		}
		return fmt.Sprint(out)
	}
	if got, want := order(), "[general:0 Info:0 a:0 b:1 c:2]"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	isErr(t, f.sess.SetChannelPositions(ctx, f.srv.ID, []discord.ChannelPosition{{ID: c.ID, Position: 0}}), nil)
	if got, want := order(), "[general:0 Info:0 c:0 a:1 b:2]"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	// Move general into the category at position 1.
	isErr(t, f.sess.SetChannelPositions(ctx, f.srv.ID, []discord.ChannelPosition{{ID: f.general.ID, Position: 1, ParentID: &cat.ID}}), nil)
	if got, want := order(), "[Info:0 c:0 general:1 a:2 b:3]"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	// Create at a requested position.
	d, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("d"), ParentID: &cat.ID, Position: ptr(0)}, "")
	if got, want := order(), "[Info:0 d:0 c:1 general:2 a:3 b:4]"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	// Deleting the category uncategorizes its children.
	f.resetEvents()
	isErr(t, f.sess.DeleteChannel(ctx, cat.ID, ""), nil)
	for _, ch := range f.w.Channels(f.srv.ID) {
		if ch.ParentID != "" {
			t.Fatalf("%s still has parent", ch.Name)
		}
	}
	f.w.WaitIdle()
	if n := len(eventsOf[discord.ChannelUpdateEvent](f.rec)); n != 5 {
		t.Fatalf("got %d ChannelUpdate events, want 5", n)
	}
	_ = a
	_ = b
	_ = d
	// Invalid parent / nesting categories.
	cat2, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x"), Type: discord.ChannelCategory}, "")
	isErr(t, f.sess.SetChannelPositions(ctx, f.srv.ID, []discord.ChannelPosition{{ID: cat2.ID, Position: 0, ParentID: &cat2.ID}}), discord.ErrBadRequest)
}

func TestOverwritePermissionRules(t *testing.T) {
	deny := func(f *fixture, perms ...string) *[]discord.Overwrite {
		return &[]discord.Overwrite{{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions(perms...)}}
	}
	cases := []struct {
		name  string
		perms []string
		ows   func(f *fixture) *[]discord.Overwrite
		want  error
	}{
		{"needs ManageRoles", []string{"ManageChannels", "ViewChannel", "SendMessages"}, func(f *fixture) *[]discord.Overwrite { return deny(f, "SendMessages") }, discord.ErrForbidden},
		{"with ManageRoles", []string{"ManageChannels", "ManageRoles", "ViewChannel", "SendMessages"}, func(f *fixture) *[]discord.Overwrite { return deny(f, "SendMessages") }, nil},
		{"cannot deny a permission it lacks", []string{"ManageChannels", "ManageRoles"}, func(f *fixture) *[]discord.Overwrite { return deny(f, "BanMembers") }, discord.ErrForbidden},
		{"ManageRoles in overwrite needs admin", []string{"ManageChannels", "ManageRoles"}, func(f *fixture) *[]discord.Overwrite { return deny(f, "ManageRoles") }, discord.ErrForbidden},
		{"admin can do anything", []string{"Administrator"}, func(f *fixture) *[]discord.Overwrite { return deny(f, "ManageRoles", "BanMembers") }, nil},
		{"unknown role", []string{"Administrator"}, func(f *fixture) *[]discord.Overwrite {
			return &[]discord.Overwrite{{ID: "42", Type: discord.OverwriteRole}}
		}, discord.ErrBadRequest},
		{"duplicate", []string{"Administrator"}, func(f *fixture) *[]discord.Overwrite {
			return &[]discord.Overwrite{{ID: f.srv.ID}, {ID: f.srv.ID}}
		}, discord.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/create", func(t *testing.T) {
			f := newFixture(t, fixtureOpts{perms: discord.MustPermissions(tc.perms...)})
			_, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x"), Overwrites: tc.ows(f)}, "")
			isErr(t, err, tc.want)
		})
		t.Run(tc.name+"/update", func(t *testing.T) {
			f := newFixture(t, fixtureOpts{perms: discord.MustPermissions(tc.perms...)})
			_, err := f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{Overwrites: tc.ows(f)}, "")
			isErr(t, err, tc.want)
		})
	}
}

func TestCategorySyncAndLockPermissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ows := []discord.Overwrite{{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")}}
	cat, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("staff"), Type: discord.ChannelCategory, Overwrites: &ows}, "")
	child, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("mods"), ParentID: &cat.ID}, "")
	if len(child.Overwrites) != 1 || child.Overwrites[0].Deny != ows[0].Deny {
		t.Fatalf("child not synced with category: %+v", child.Overwrites)
	}
	// Move general into the category and lock its permissions.
	ch, err := f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{ParentID: &cat.ID, LockPermissions: ptr(true)}, "")
	isErr(t, err, nil)
	if ch.ParentID != cat.ID || len(ch.Overwrites) != 1 {
		t.Fatalf("general = %+v", ch)
	}
	if f.w.Permissions(f.srv.ID, ch.ID, f.alice.ID).Has(discord.MustPermissions("ViewChannel")) {
		t.Fatal("alice should not see the locked channel")
	}
	_, err = f.w.SendMessage(f.alice.ID, ch.ID, "x")
	isErr(t, err, discord.ErrForbidden)
}

func TestThreads(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	msg, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "topic")
	th, err := f.sess.CreateThread(ctx, f.general.ID, discord.ThreadParams{Name: "Discussion", MessageID: msg.ID})
	isErr(t, err, nil)
	if th.Type != discord.ChannelPublicThread || th.ParentID != f.general.ID || th.ID != msg.ID {
		t.Fatalf("thread = %+v", th)
	}
	priv, err := f.sess.CreateThread(ctx, f.general.ID, discord.ThreadParams{Name: "secret", Private: true})
	isErr(t, err, nil)
	if priv.Type != discord.ChannelPrivateThread {
		t.Fatalf("private thread type = %v", priv.Type)
	}
	chans, _ := f.sess.ListChannels(ctx, f.srv.ID)
	for _, c := range chans {
		if c.ID == th.ID || c.ID == priv.ID {
			t.Fatal("ListChannels must not include threads")
		}
	}
	if len(f.w.Threads(f.srv.ID)) != 2 {
		t.Fatal("Threads() should list both")
	}
	_, err = f.w.SendMessage(f.alice.ID, th.ID, "in thread")
	isErr(t, err, nil)
	got, err := f.sess.GetChannel(ctx, th.ID)
	isErr(t, err, nil)
	if got.ID != th.ID {
		t.Fatal("GetChannel(thread)")
	}
	f.w.WaitIdle()
	if n := len(eventsOf[discord.ChannelCreateEvent](f.rec)); n != 2 {
		t.Fatalf("got %d ChannelCreate events for threads", n)
	}
	cat, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("c"), Type: discord.ChannelCategory}, "")
	_, err = f.sess.CreateThread(ctx, cat.ID, discord.ThreadParams{Name: "x"})
	isErr(t, err, discord.ErrBadRequest)
	isErr(t, f.sess.DeleteChannel(ctx, f.general.ID, ""), nil)
	if len(f.w.Threads(f.srv.ID)) != 0 {
		t.Fatal("threads should be deleted with their parent")
	}
}

// ───────────────────────────────────────────────────────────── messages & DMs

func TestDMs(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	dm, err := f.sess.CreateDM(ctx, f.alice.ID)
	isErr(t, err, nil)
	if dm.Type != discord.ChannelDM || dm.ServerID != "" {
		t.Fatalf("dm = %+v", dm)
	}
	again, _ := f.sess.CreateDM(ctx, f.alice.ID)
	if again.ID != dm.ID {
		t.Fatal("CreateDM should return the existing channel")
	}
	_, err = f.sess.SendMessage(ctx, dm.ID, discord.MessageCreate{Content: "welcome!"})
	isErr(t, err, nil)
	dms := f.w.DMs(f.alice.ID, f.bot.ID)
	if len(dms) != 1 || dms[0].Content != "welcome!" || dms[0].ServerID != "" {
		t.Fatalf("dms = %+v", dms)
	}
	// The user replies.
	_, err = f.w.SendMessage(f.alice.ID, dm.ID, "thanks")
	isErr(t, err, nil)
	f.w.WaitIdle()
	var fromAlice int
	for _, e := range eventsOf[discord.MessageCreateEvent](f.rec) {
		if e.Message.Author.ID == f.alice.ID && e.Message.Content == "thanks" {
			fromAlice++
		}
	}
	if fromAlice != 1 {
		t.Fatal("bot did not receive the DM")
	}
	// Closed DMs.
	_ = f.w.SetDMsEnabled(f.alice.ID, false)
	_, err = f.sess.SendMessage(ctx, dm.ID, discord.MessageCreate{Content: "again"})
	isErr(t, err, discord.ErrForbidden)
	// Users sharing no server with the bot.
	stranger := f.w.CreateUser("stranger")
	sdm, _ := f.sess.CreateDM(ctx, stranger.ID)
	_, err = f.sess.SendMessage(ctx, sdm.ID, discord.MessageCreate{Content: "hi"})
	isErr(t, err, discord.ErrForbidden)
	// Bots cannot DM bots.
	_, other, _ := f.w.CreateBot("other")
	_, err = f.sess.CreateDM(ctx, other.ID)
	isErr(t, err, discord.ErrForbidden)
	_, err = f.sess.CreateDM(ctx, "999")
	isErr(t, err, discord.ErrNotFound)
}

func TestListMessagesPaging(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	var ids []string
	for i := 0; i < 10; i++ {
		m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, fmt.Sprint(i))
		ids = append(ids, m.ID)
	}
	contents := func(ms []discord.Message) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Content)
		}
		return fmt.Sprint(out)
	}
	cases := []struct {
		name string
		p    discord.ListMessagesParams
		want string
		err  error
	}{
		{"default newest first", discord.ListMessagesParams{}, "[9 8 7 6 5 4 3 2 1 0]", nil},
		{"limit", discord.ListMessagesParams{Limit: 3}, "[9 8 7]", nil},
		{"before", discord.ListMessagesParams{Before: ids[5], Limit: 2}, "[4 3]", nil},
		{"after", discord.ListMessagesParams{After: ids[5], Limit: 2}, "[7 6]", nil},
		{"after all", discord.ListMessagesParams{After: ids[7]}, "[9 8]", nil},
		{"limit clamped", discord.ListMessagesParams{Limit: 101}, "[9 8 7 6 5 4 3 2 1 0]", nil},
		{"before and after", discord.ListMessagesParams{Before: ids[5], After: ids[1]}, "", discord.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms, err := f.sess.ListMessages(ctx, f.general.ID, tc.p)
			isErr(t, err, tc.err)
			if tc.err == nil && contents(ms) != tc.want {
				t.Fatalf("got %s, want %s", contents(ms), tc.want)
			}
		})
	}
}

func TestMessageValidation(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	long := make([]byte, 2001)
	for i := range long {
		long[i] = 'a'
	}
	btn := func(id string) discord.Component {
		return discord.Component{Type: discord.ComponentButton, CustomID: id, Label: "b", Style: discord.ButtonPrimary}
	}
	row := func(cs ...discord.Component) discord.Component {
		return discord.Component{Type: discord.ComponentActionRow, Components: cs}
	}
	cases := []struct {
		name string
		m    discord.MessageCreate
		err  error
	}{
		{"ok", discord.MessageCreate{Content: "hi"}, nil},
		{"empty", discord.MessageCreate{}, discord.ErrBadRequest},
		{"too long", discord.MessageCreate{Content: string(long)}, discord.ErrBadRequest},
		{"embeds only", discord.MessageCreate{Embeds: []discord.Embed{{Title: "t"}}}, nil},
		{"11 embeds", discord.MessageCreate{Embeds: make([]discord.Embed, 11)}, discord.ErrBadRequest},
		{"components only", discord.MessageCreate{Components: []discord.Component{row(btn("a"))}}, nil},
		{"button outside row", discord.MessageCreate{Components: []discord.Component{btn("a")}}, discord.ErrBadRequest},
		{"6 buttons", discord.MessageCreate{Components: []discord.Component{row(btn("1"), btn("2"), btn("3"), btn("4"), btn("5"), btn("6"))}}, discord.ErrBadRequest},
		{"duplicate custom ID", discord.MessageCreate{Components: []discord.Component{row(btn("a"), btn("a"))}}, discord.ErrBadRequest},
		{"button without custom ID", discord.MessageCreate{Components: []discord.Component{row(btn(""))}}, discord.ErrBadRequest},
		{"link button", discord.MessageCreate{Components: []discord.Component{row(discord.Component{Type: discord.ComponentButton, Style: discord.ButtonLink, Label: "x", URL: "https://x"})}}, nil},
		{"reply to unknown", discord.MessageCreate{Content: "x", ReplyTo: "1"}, discord.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.sess.SendMessage(ctx, f.general.ID, tc.m)
			isErr(t, err, tc.err)
		})
	}
	cat, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("c"), Type: discord.ChannelCategory}, "")
	_, err := f.sess.SendMessage(ctx, cat.ID, discord.MessageCreate{Content: "x"})
	isErr(t, err, discord.ErrBadRequest)
}

func TestEditAndDeleteMessages(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	m, _ := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "v1", Embeds: []discord.Embed{{Title: "e"}}})
	f.resetEvents()
	e, err := f.sess.EditMessage(ctx, f.general.ID, m.ID, discord.MessageCreate{Content: "v2"})
	isErr(t, err, nil)
	if e.Content != "v2" || len(e.Embeds) != 0 || e.EditedAt == nil {
		t.Fatalf("edited = %+v (edits fully replace content, embeds and components)", e)
	}
	isErr(t, f.sess.DeleteMessage(ctx, f.general.ID, m.ID, ""), nil)
	isErr(t, f.sess.DeleteMessage(ctx, f.general.ID, m.ID, ""), discord.ErrNotFound)
	f.w.WaitIdle()
	if len(eventsOf[discord.MessageUpdateEvent](f.rec)) != 1 || len(eventsOf[discord.MessageDeleteEvent](f.rec)) != 1 {
		t.Fatalf("events = %+v", f.rec.all())
	}
	if len(f.w.Messages(f.general.ID)) != 0 {
		t.Fatal("message not deleted")
	}
}

func TestReactions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	m, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "vote")
	isErr(t, f.sess.AddReaction(ctx, f.general.ID, m.ID, "👍"), nil)
	isErr(t, f.sess.AddReaction(ctx, f.general.ID, m.ID, "👍"), nil) // idempotent
	isErr(t, f.w.AddReaction(f.alice.ID, f.general.ID, m.ID, "👍"), nil)
	if got := f.w.Reactions(m.ID)["👍"]; len(got) != 2 || got[0] != f.bot.ID {
		t.Fatalf("reactions = %v", got)
	}
	isErr(t, f.sess.RemoveOwnReaction(ctx, f.general.ID, m.ID, "👍"), nil)
	isErr(t, f.sess.AddReaction(ctx, f.general.ID, m.ID, ""), discord.ErrBadRequest)
	f.w.WaitIdle()
	adds := eventsOf[discord.ReactionAddEvent](f.rec)
	if len(adds) != 2 || adds[1].UserID != f.alice.ID || adds[1].Member == nil {
		t.Fatalf("adds = %+v", adds)
	}
	if len(eventsOf[discord.ReactionRemoveEvent](f.rec)) != 1 {
		t.Fatal("missing ReactionRemove")
	}
	// Without AddReactions a bot can only add existing reactions.
	f2 := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ViewChannel")})
	_, _ = f2.w.AdminUpdateRole(f2.srv.ID, f2.srv.ID, discord.RoleParams{Permissions: ptr(discord.MustPermissions("ViewChannel", "ReadMessageHistory", "SendMessages"))})
	m2, _ := f2.w.SendMessage(f2.alice.ID, f2.general.ID, "x")
	isErr(t, f2.sess.AddReaction(ctx, f2.general.ID, m2.ID, "🎉"), discord.ErrForbidden)
}

func TestTimedOutMemberCannotSend(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, fixtureOpts{world: []Option{WithClock(func() time.Time { return now })}})
	_, err := f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: ptr(now.Add(time.Hour))}, "spam")
	isErr(t, err, nil)
	_, err = f.w.SendMessage(f.alice.ID, f.general.ID, "x")
	isErr(t, err, discord.ErrForbidden)
	_, err = f.sess.UpdateMember(ctx, f.srv.ID, f.alice.ID, discord.MemberParams{TimeoutUntil: &time.Time{}}, "")
	isErr(t, err, nil)
	_, err = f.w.SendMessage(f.alice.ID, f.general.ID, "x")
	isErr(t, err, nil)
	f.w.WaitIdle()
	ups := eventsOf[discord.MemberUpdateEvent](f.rec)
	if len(ups) != 2 || ups[0].Previous == nil || ups[0].Member.TimeoutUntil == nil {
		t.Fatalf("member updates = %+v", ups)
	}
	// Timing out the owner or an administrator is forbidden.
	_, err = f.sess.UpdateMember(ctx, f.srv.ID, f.owner.ID, discord.MemberParams{TimeoutUntil: ptr(now.Add(time.Hour))}, "")
	isErr(t, err, discord.ErrForbidden)
}

func TestBanRemovesMemberAndMessages(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "spam")
	_, _ = f.w.SendMessage(f.owner.ID, f.general.ID, "keep")
	isErr(t, f.sess.BanMember(ctx, f.srv.ID, f.alice.ID, 3600, "spam"), nil)
	if _, ok := f.w.Member(f.srv.ID, f.alice.ID); ok {
		t.Fatal("still a member")
	}
	if ms := f.w.Messages(f.general.ID); len(ms) != 1 || ms[0].Content != "keep" {
		t.Fatalf("messages = %+v", ms)
	}
	isErr(t, f.w.AddMember(f.srv.ID, f.alice.ID), discord.ErrForbidden)
	if bans := f.w.Bans(f.srv.ID); len(bans) != 1 || bans[0] != f.alice.ID {
		t.Fatalf("bans = %v", bans)
	}
	isErr(t, f.w.UnbanMember(f.srv.ID, f.alice.ID), nil)
	isErr(t, f.w.AddMember(f.srv.ID, f.alice.ID), nil)
}

// ───────────────────────────────────────────────────────────── server, self, presence, call log

func TestUpdateServerAndSelf(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	srv, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{
		Name: ptr("Renamed"), VerificationLevel: ptr("high"), Icon: ptr([]byte("png")), SystemChannelID: ptr(""),
	})
	isErr(t, err, nil)
	if srv.Name != "Renamed" || srv.VerificationLevel != "high" || srv.IconURL == "" || srv.SystemChannelID != "" {
		t.Fatalf("server = %+v", srv)
	}
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{VerificationLevel: ptr("extreme")})
	isErr(t, err, discord.ErrBadRequest)
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{RulesChannelID: ptr("1")})
	isErr(t, err, discord.ErrBadRequest)

	u, err := f.sess.UpdateSelf(ctx, discord.SelfParams{Username: ptr("codi-bot"), Avatar: ptr([]byte("img"))})
	isErr(t, err, nil)
	if u.Username != "codi-bot" || u.AvatarURL == "" || f.sess.Self().Username != "codi-bot" {
		t.Fatalf("self = %+v", u)
	}
	_, err = f.sess.UpdateSelf(ctx, discord.SelfParams{Username: ptr("x")})
	isErr(t, err, discord.ErrBadRequest)

	isErr(t, f.sess.SetPresence(ctx, discord.Presence{Status: "dnd", ActivityType: "watching", ActivityName: "the repo"}), nil)
	if p, ok := f.w.Presence(f.bot.ID); !ok || p.Status != "dnd" || p.ActivityName != "the repo" {
		t.Fatalf("presence = %+v", p)
	}
	isErr(t, f.sess.SetPresence(ctx, discord.Presence{Status: "busy"}), discord.ErrBadRequest)
}

func TestCallLog(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	_, _ = f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a")}, "plan")
	_, _ = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("b")}, "plan")
	_, _ = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{}, "plan") // fails
	_, _ = f.sess.ListRoles(ctx, f.srv.ID)                                      // read: not logged
	_, _ = f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{})
	if n := f.w.CountCalls(f.bot.ID); n != 2 {
		t.Fatalf("CountCalls = %d, want 2", n)
	}
	if n := f.w.CountCalls(f.bot.ID, "CreateChannel"); n != 1 {
		t.Fatalf("CountCalls(CreateChannel) = %d", n)
	}
	calls := f.w.Calls(f.bot.ID)
	if len(calls) != 3 || calls[2].Err == nil || calls[0].Reason != "plan" || calls[0].TargetID == "" {
		t.Fatalf("calls = %+v", calls)
	}
	if n := f.w.CountCalls(f.owner.ID); n != 1 {
		t.Fatalf("owner calls = %d", n)
	}
	f.w.ResetCalls()
	if len(f.w.Calls("")) != 0 {
		t.Fatal("ResetCalls")
	}
}

func TestAdminChangesEmitDriftEvents(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers")})
	_, err := f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Name: ptr("renamed")})
	isErr(t, err, nil)
	r, _ := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("x")})
	isErr(t, f.w.AdminDeleteRole(f.srv.ID, r.ID), nil)
	ch, _ := f.w.AdminCreateChannel(f.srv.ID, discord.ChannelParams{Name: ptr("new")})
	isErr(t, f.w.AdminDeleteChannel(ch.ID), nil)
	f.w.WaitIdle()
	ups := eventsOf[discord.ChannelUpdateEvent](f.rec)
	if len(ups) != 1 || ups[0].Channel.Name != "renamed" {
		t.Fatalf("channel updates = %+v", ups)
	}
	if len(eventsOf[discord.RoleCreateEvent](f.rec)) != 1 || len(eventsOf[discord.RoleDeleteEvent](f.rec)) != 1 ||
		len(eventsOf[discord.ChannelCreateEvent](f.rec)) != 1 || len(eventsOf[discord.ChannelDeleteEvent](f.rec)) != 1 {
		t.Fatalf("events = %+v", f.rec.all())
	}
}

func TestBotReceivesItsOwnRESTChanges(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers")})
	_, _ = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x")}, "")
	f.w.WaitIdle()
	if len(eventsOf[discord.ChannelCreateEvent](f.rec)) != 1 {
		t.Fatal("bot should receive ChannelCreate for its own change")
	}
}

func TestSnowflakes(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := NewWorld(WithClock(func() time.Time { return fixed }))
	prev := ""
	for i := 0; i < 100; i++ {
		id := w.CreateUser("u").ID
		if len(id) < 18 || len(id) > 19 {
			t.Fatalf("id %s has %d digits", id, len(id))
		}
		if prev != "" && !snowLess(prev, id) {
			t.Fatalf("ids not monotonic: %s then %s", prev, id)
		}
		prev = id
	}
	u, _ := w.User(prev)
	_ = u
	if w.Now() != fixed {
		t.Fatal("clock")
	}
}

func TestReturnedValuesAreCopies(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ows := []discord.Overwrite{{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: 1}}
	ch, _ := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("x"), Overwrites: &ows}, "")
	ows[0].Deny = 2
	ch.Overwrites[0].Deny = 4
	got, _ := f.w.Channel(ch.ID)
	if got.Overwrites[0].Deny != 1 {
		t.Fatal("state aliased caller slices")
	}
}
