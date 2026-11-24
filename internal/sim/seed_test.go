package sim

import (
	"strconv"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func TestCreateServerWithID(t *testing.T) {
	w := NewWorld()
	owner := w.CreateUser("owner")
	const id = "1200000000000000000"
	srv, err := w.CreateServerWithID(id, "Fixed", owner.ID, WithGeneralChannel())
	isErr(t, err, nil)
	if srv.ID != id || srv.OwnerID != owner.ID || srv.SystemChannelID == "" {
		t.Fatalf("server = %+v", srv)
	}
	if roles := w.Roles(id); len(roles) != 1 || roles[0].ID != id || !roles[0].IsEveryone(id) {
		t.Fatalf("roles = %+v", roles)
	}
	cases := []struct {
		name, id, owner string
		want            error
	}{
		{"taken by server", id, owner.ID, discord.ErrBadRequest},
		{"taken by user", owner.ID, owner.ID, discord.ErrBadRequest},
		{"taken by channel", srv.SystemChannelID, owner.ID, discord.ErrBadRequest},
		{"not a number", "abc", owner.ID, discord.ErrBadRequest},
		{"empty", "", owner.ID, discord.ErrBadRequest},
		{"zero", "0", owner.ID, discord.ErrBadRequest},
		{"leading zero", "0123", owner.ID, discord.ErrBadRequest},
		{"negative", "-5", owner.ID, discord.ErrBadRequest},
		{"overflow", "99999999999999999999", owner.ID, discord.ErrBadRequest},
		{"unknown owner", "1200000000000000001", "42", discord.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.CreateServerWithID(tc.id, "x", tc.owner)
			isErr(t, err, tc.want)
		})
	}
}

func TestInjectedIDsNeverCollide(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := NewWorld(WithClock(func() time.Time { return now })) // frozen clock: IDs only advance by +1
	owner := w.CreateUser("owner")
	next, _ := strconv.ParseUint(owner.ID, 10, 64)
	// Inject the IDs the generator would hand out next.
	for i := uint64(1); i <= 3; i++ {
		_, err := w.CreateServerWithID(strconv.FormatUint(next+i, 10), "s", owner.ID)
		isErr(t, err, nil)
	}
	_, _, err := w.CreateBotWithApplicationID("bot", strconv.FormatUint(next+4, 10))
	isErr(t, err, nil)
	seen := map[string]bool{}
	for _, s := range []uint64{1, 2, 3, 4} {
		seen[strconv.FormatUint(next+s, 10)] = true
	}
	for i := 0; i < 50; i++ {
		id := w.CreateUser("u").ID
		if seen[id] {
			t.Fatalf("generated ID %s collides with an injected one", id)
		}
		seen[id] = true
	}
	// An injected ID below the generator is fine too, as long as it is unused.
	_, err = w.CreateServerWithID("1100000000000000000", "low", owner.ID)
	isErr(t, err, nil)
}

func TestCreateBotWithApplicationID(t *testing.T) {
	w := NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("S", owner.ID, WithGeneralChannel())
	const appID = "1300000000000000000"
	token, bot, err := w.CreateBotWithApplicationID("cicord", appID)
	isErr(t, err, nil)
	if !bot.Bot || bot.ID == appID {
		t.Fatalf("bot = %+v", bot)
	}
	if got, ok := w.ApplicationID(bot.ID); !ok || got != appID {
		t.Fatalf("ApplicationID = %q", got)
	}
	_, _ = w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator"))
	sess := w.MustConnect(token)
	if sess.ApplicationID() != appID {
		t.Fatal("session application ID")
	}
	isErr(t, sess.SetServerCommands(ctx, appID, srv.ID, []discord.Command{{Name: "ping", Description: "p"}}), nil)
	if len(w.Commands(appID, srv.ID)) != 1 {
		t.Fatal("commands not registered under the fixed application ID")
	}
	_, _, err = w.CreateBotWithApplicationID("dup", appID)
	isErr(t, err, discord.ErrBadRequest)
	_, _, err = w.CreateBotWithApplicationID("bad", "x1")
	isErr(t, err, discord.ErrBadRequest)
	_, _, err = w.CreateBotWithApplicationID("srv", srv.ID)
	isErr(t, err, discord.ErrBadRequest)
}

func TestUsersAndBots(t *testing.T) {
	w := NewWorld()
	a := w.CreateUser("a")
	_, b1, _ := w.CreateBot("b1")
	c := w.CreateUser("c")
	_, b2, _ := w.CreateBot("b2")
	users, bots := w.Users(), w.Bots()
	if len(users) != 2 || users[0].ID != a.ID || users[1].ID != c.ID {
		t.Fatalf("users = %+v", users)
	}
	if len(bots) != 2 || bots[0].ID != b1.ID || bots[1].ID != b2.ID {
		t.Fatalf("bots = %+v", bots)
	}
}

func TestSubscribe(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ch, cancel := f.w.Subscribe()
	drain := func() {
		for {
			select {
			case <-ch:
			default:
				return
			}
		}
	}
	expect := func(name string, act func()) {
		t.Helper()
		drain()
		act()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s: no notification", name)
		}
	}
	expect("message", func() { _, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "hi") })
	expect("member", func() { _ = f.w.AddMember(f.srv.ID, f.w.CreateUser("bob").ID) })
	expect("role", func() { _, _ = f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{}, "") })
	expect("channel", func() { _, _ = f.w.AdminUpdateChannel(f.general.ID, discord.ChannelParams{Topic: ptr("t")}) })
	expect("user", func() { f.w.CreateUser("carol") })
	expect("presence", func() { _ = f.sess.SetPresence(ctx, discord.Presence{Status: "idle"}) })
	expect("interaction", func() {
		_ = f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, []discord.Command{{Name: "ping", Description: "p"}})
		drain()
		f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	})

	// Coalesced and non-blocking: many changes with nobody reading never block.
	drain()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "spam")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("world blocked on a slow subscriber")
	}
	if len(ch) != 1 {
		t.Fatalf("pending notifications = %d, want 1 (coalesced)", len(ch))
	}

	cancel()
	cancel() // idempotent
	<-ch     // the pending notification, then the close
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	_, _ = f.w.SendMessage(f.alice.ID, f.general.ID, "after cancel") // must not panic
	f.w.WaitIdle()
}
