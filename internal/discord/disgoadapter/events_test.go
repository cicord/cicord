package disgoadapter

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/gorilla/websocket"

	"github.com/cicord/cicord/internal/discord"
)

func testSession() *session {
	return &session{logger: slog.Default(), members: newMemberCache()}
}

func mustTranslate(t *testing.T, s *session, typ, payload string) []discord.Event {
	t.Helper()
	evs, err := s.translate(typ, []byte(payload))
	if err != nil {
		t.Fatalf("translate %s: %v", typ, err)
	}
	return evs
}

// guildCreate translates a GUILD_CREATE and drops the ServerAvailableEvent every
// available GUILD_CREATE emits (checked in TestTranslateServerAvailable).
func guildCreate(t *testing.T, s *session, payload string) []discord.Event {
	t.Helper()
	var out []discord.Event
	for _, e := range mustTranslate(t, s, "GUILD_CREATE", payload) {
		if _, ok := e.(discord.ServerAvailableEvent); !ok {
			out = append(out, e)
		}
	}
	return out
}

func one(t *testing.T, evs []discord.Event) discord.Event {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1: %#v", len(evs), evs)
	}
	return evs[0]
}

func TestTranslateReady(t *testing.T) {
	s := testSession()
	ev := one(t, mustTranslate(t, s, "READY", `{"v":10,"user":{"id":"600","username":"cicord","bot":true},
	  "guilds":[{"id":"1","unavailable":true},{"id":"2","unavailable":true}],"session_id":"x","application":{"id":"600"}}`))
	r, ok := ev.(discord.ReadyEvent)
	if !ok || r.User.ID != "600" || !r.User.Bot || !reflect.DeepEqual(r.ServerIDs, []string{"1", "2"}) {
		t.Errorf("ready = %#v", ev)
	}
}

func TestTranslateMembersAndPrevious(t *testing.T) {
	s := testSession()
	// GUILD_CREATE seeds the cache but emits nothing.
	if evs := guildCreate(t, s, `{"id":"1","members":[{"user":{"id":"2","username":"a"},"roles":["5"]}]}`); len(evs) != 0 {
		t.Errorf("guild create emitted %v", evs)
	}
	ev := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"2","username":"a"},"roles":["5","6"],"nick":"x"}`))
	u := ev.(discord.MemberUpdateEvent)
	if u.Previous == nil || !reflect.DeepEqual(u.Previous.RoleIDs, []string{"5"}) || !reflect.DeepEqual(u.Member.RoleIDs, []string{"5", "6"}) || u.Member.Nick != "x" || u.Member.ServerID != "1" {
		t.Errorf("update = %#v", u)
	}
	// Unknown member: Previous nil, then cached.
	u2 := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"3","username":"b"},"roles":[]}`)).(discord.MemberUpdateEvent)
	if u2.Previous != nil {
		t.Errorf("previous should be nil, got %+v", u2.Previous)
	}
	u3 := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"3","username":"b"},"roles":["7"],"communication_disabled_until":"2030-01-01T00:00:00Z"}`)).(discord.MemberUpdateEvent)
	if u3.Previous == nil || len(u3.Previous.RoleIDs) != 0 || u3.Member.TimeoutUntil == nil {
		t.Errorf("third update = %#v", u3)
	}

	j := one(t, mustTranslate(t, s, "GUILD_MEMBER_ADD", `{"guild_id":"1","user":{"id":"4","username":"c"},"roles":[],"joined_at":"2024-01-01T00:00:00Z"}`)).(discord.MemberJoinEvent)
	if j.Member.ServerID != "1" || j.Member.User.ID != "4" || j.Member.JoinedAt.IsZero() {
		t.Errorf("join = %#v", j)
	}
	l := one(t, mustTranslate(t, s, "GUILD_MEMBER_REMOVE", `{"guild_id":"1","user":{"id":"4","username":"c"}}`)).(discord.MemberLeaveEvent)
	if l.ServerID != "1" || l.User.Username != "c" {
		t.Errorf("leave = %#v", l)
	}
	if u := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"4"},"roles":[]}`)).(discord.MemberUpdateEvent); u.Previous != nil {
		t.Error("removed member should not be cached")
	}

	// Chunks feed the cache too; GUILD_DELETE (not unavailable) drops the server.
	mustTranslate(t, s, "GUILD_MEMBERS_CHUNK", `{"guild_id":"8","members":[{"user":{"id":"9"},"roles":["1"]}]}`)
	if u := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"8","user":{"id":"9"},"roles":[]}`)).(discord.MemberUpdateEvent); u.Previous == nil {
		t.Error("chunked member should be cached")
	}
	mustTranslate(t, s, "GUILD_DELETE", `{"id":"8"}`)
	if u := one(t, mustTranslate(t, s, "GUILD_MEMBER_UPDATE", `{"guild_id":"8","user":{"id":"9"},"roles":[]}`)).(discord.MemberUpdateEvent); u.Previous != nil {
		t.Error("server should have been dropped from the cache")
	}
}

func TestTranslateMessages(t *testing.T) {
	s := testSession()
	c := one(t, mustTranslate(t, s, "MESSAGE_CREATE", `{"id":"100","channel_id":"10","guild_id":"1","author":{"id":"2","username":"a"},
	  "member":{"roles":["5"]},"content":"hi","timestamp":"2024-01-01T00:00:00Z"}`)).(discord.MessageCreateEvent)
	if c.Message.Content != "hi" || c.Message.Member == nil || c.Message.Member.User.ID != "2" || discord.ServerIDOf(c) != "1" {
		t.Errorf("create = %#v", c)
	}
	u := one(t, mustTranslate(t, s, "MESSAGE_UPDATE", `{"id":"100","channel_id":"10","author":{"id":"2"},"content":"edited","timestamp":"2024-01-01T00:00:00Z","edited_timestamp":"2024-01-01T00:01:00Z"}`)).(discord.MessageUpdateEvent)
	if u.Message.EditedAt == nil || u.Message.ServerID != "" || u.Message.Member != nil {
		t.Errorf("update = %#v", u)
	}
	d := one(t, mustTranslate(t, s, "MESSAGE_DELETE", `{"id":"100","channel_id":"10","guild_id":"1"}`)).(discord.MessageDeleteEvent)
	if d != (discord.MessageDeleteEvent{ID: "100", ChannelID: "10", ServerID: "1"}) {
		t.Errorf("delete = %#v", d)
	}
	bulk := mustTranslate(t, s, "MESSAGE_DELETE_BULK", `{"ids":["1","2","3"],"channel_id":"10","guild_id":"1"}`)
	if len(bulk) != 3 || bulk[2].(discord.MessageDeleteEvent).ID != "3" {
		t.Errorf("bulk = %#v", bulk)
	}
}

func TestTranslateReactions(t *testing.T) {
	s := testSession()
	a := one(t, mustTranslate(t, s, "MESSAGE_REACTION_ADD", `{"user_id":"2","channel_id":"10","message_id":"100","guild_id":"1",
	  "member":{"user":{"id":"2","username":"a"},"roles":[]},"emoji":{"id":"55","name":"party"}}`)).(discord.ReactionAddEvent)
	if a.Emoji != "party:55" || a.Member == nil || a.Member.User.ID != "2" || a.ServerID != "1" || a.MessageID != "100" {
		t.Errorf("add = %#v", a)
	}
	r := one(t, mustTranslate(t, s, "MESSAGE_REACTION_REMOVE", `{"user_id":"2","channel_id":"10","message_id":"100","emoji":{"id":null,"name":"👍"}}`)).(discord.ReactionRemoveEvent)
	if r.Emoji != "👍" || r.Member != nil || r.ServerID != "" {
		t.Errorf("remove = %#v", r)
	}
}

func TestTranslateChannelsRolesServer(t *testing.T) {
	s := testSession()
	cc := one(t, mustTranslate(t, s, "CHANNEL_CREATE", `{"id":"10","guild_id":"1","name":"general","type":0,"position":1,"permission_overwrites":[]}`)).(discord.ChannelCreateEvent)
	if cc.Channel.Name != "general" || cc.Channel.ServerID != "1" {
		t.Errorf("channel create = %#v", cc)
	}
	if _, ok := one(t, mustTranslate(t, s, "CHANNEL_UPDATE", `{"id":"10","guild_id":"1","name":"g2","type":0}`)).(discord.ChannelUpdateEvent); !ok {
		t.Error("channel update")
	}
	if _, ok := one(t, mustTranslate(t, s, "CHANNEL_DELETE", `{"id":"10","guild_id":"1","name":"g2","type":0}`)).(discord.ChannelDeleteEvent); !ok {
		t.Error("channel delete")
	}
	th := one(t, mustTranslate(t, s, "THREAD_CREATE", `{"id":"20","guild_id":"1","parent_id":"10","name":"t","type":11}`)).(discord.ChannelCreateEvent)
	if th.Channel.Type != discord.ChannelPublicThread || th.Channel.ParentID != "10" {
		t.Errorf("thread = %#v", th)
	}
	if d, ok := one(t, mustTranslate(t, s, "THREAD_DELETE", `{"id":"20","guild_id":"1","parent_id":"10","type":11}`)).(discord.ChannelDeleteEvent); !ok || d.Channel.ID != "20" {
		t.Error("thread delete")
	}

	rc := one(t, mustTranslate(t, s, "GUILD_ROLE_CREATE", `{"guild_id":"1","role":{"id":"5","name":"mod","permissions":"8","position":1}}`)).(discord.RoleCreateEvent)
	if rc.ServerID != "1" || rc.Role.Name != "mod" || rc.Role.Permissions != 8 {
		t.Errorf("role create = %#v", rc)
	}
	if ru := one(t, mustTranslate(t, s, "GUILD_ROLE_UPDATE", `{"guild_id":"1","role":{"id":"5","name":"mod2","permissions":"0"}}`)).(discord.RoleUpdateEvent); ru.Role.Name != "mod2" {
		t.Errorf("role update = %#v", ru)
	}
	if rd := one(t, mustTranslate(t, s, "GUILD_ROLE_DELETE", `{"guild_id":"1","role_id":"5"}`)).(discord.RoleDeleteEvent); rd != (discord.RoleDeleteEvent{ServerID: "1", RoleID: "5"}) {
		t.Errorf("role delete = %#v", rd)
	}
	su := one(t, mustTranslate(t, s, "GUILD_UPDATE", `{"id":"1","name":"S","owner_id":"2","verification_level":3,"default_message_notifications":1,"system_channel_id":null}`)).(discord.ServerUpdateEvent)
	if su.Server.Name != "S" || su.Server.VerificationLevel != "high" || su.Server.DefaultNotifications != "onlyMentions" || su.Server.SystemChannelID != "" {
		t.Errorf("server update = %#v", su)
	}
	ic := one(t, mustTranslate(t, s, "INTERACTION_CREATE", `{"id":"1","type":2,"token":"t","guild_id":"1","member":{"user":{"id":"2"}},"data":{"name":"ping"}}`)).(discord.InteractionCreateEvent)
	if ic.Interaction.CommandName != "ping" || discord.ServerIDOf(ic) != "1" {
		t.Errorf("interaction = %#v", ic)
	}
	if evs := mustTranslate(t, s, "TYPING_START", `{}`); evs != nil {
		t.Errorf("ignored event produced %v", evs)
	}
	if _, err := s.translate("MESSAGE_CREATE", []byte(`{"id":`)); err == nil {
		t.Error("malformed payload should error")
	}
}

func TestQueueOrderAndNonBlocking(t *testing.T) {
	q := newEventQueue()
	var (
		mu  sync.Mutex
		got []string
	)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		q.run(func(e discord.Event) {
			<-release // a slow handler
			mu.Lock()
			got = append(got, e.(discord.MessageDeleteEvent).ID)
			mu.Unlock()
		}, slog.Default())
	}()
	start := time.Now()
	const n = 1000
	for i := range n {
		q.push(discord.MessageDeleteEvent{ID: fmt.Sprint(i)})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("push blocked for %v behind a slow handler", d)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		l := len(got)
		mu.Unlock()
		if l == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d events delivered", l, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i, id := range got {
		if id != fmt.Sprint(i) {
			t.Fatalf("event %d delivered as %s: order not preserved", i, id)
		}
	}
	q.close()
	<-done
	q.push(discord.MessageDeleteEvent{ID: "late"}) // no-op after close
	if q.len() != 0 {
		t.Error("push after close should be dropped")
	}
}

func TestQueueSurvivesPanic(t *testing.T) {
	q := newEventQueue()
	got := make(chan string, 2)
	go q.run(func(e discord.Event) {
		id := e.(discord.MessageDeleteEvent).ID
		if id == "boom" {
			panic("handler bug")
		}
		got <- id
	}, slog.New(slog.DiscardHandler))
	q.push(discord.MessageDeleteEvent{ID: "boom"})
	q.push(discord.MessageDeleteEvent{ID: "ok"})
	select {
	case id := <-got:
		if id != "ok" {
			t.Errorf("got %s", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher died after panic")
	}
	q.close()
}

func TestPresenceToGateway(t *testing.T) {
	p, err := presenceToGateway(discord.Presence{Status: "dnd", ActivityType: "watching", ActivityName: "the logs"})
	if err != nil || p.Status != "dnd" || len(p.Activities) != 1 || p.Activities[0].Name != "the logs" || p.Activities[0].Type != 3 {
		t.Errorf("presence = %+v %v", p, err)
	}
	c, _ := presenceToGateway(discord.Presence{ActivityType: "custom", ActivityName: "Shipping"})
	if c.Status != "online" || c.Activities[0].Type != 4 || c.Activities[0].State == nil || *c.Activities[0].State != "Shipping" {
		t.Errorf("custom = %+v", c)
	}
	st, _ := presenceToGateway(discord.Presence{ActivityType: "streaming", ActivityName: "x", ActivityURL: "https://twitch.tv/x"})
	if st.Activities[0].URL == nil || st.Activities[0].Type != 1 {
		t.Errorf("streaming = %+v", st)
	}
	none, _ := presenceToGateway(discord.Presence{Status: "idle"})
	if len(none.Activities) != 0 || none.Status != "idle" {
		t.Errorf("none = %+v", none)
	}
	if _, err := presenceToGateway(discord.Presence{Status: "away"}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad status err = %v", err)
	}
	if _, err := presenceToGateway(discord.Presence{ActivityType: "dancing"}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad activity err = %v", err)
	}
}

func restErr(status int, code int, msg string) error {
	return &rest.Error{Response: &http.Response{StatusCode: status, Status: http.StatusText(status)}, Code: rest.JSONErrorCode(code), Message: msg}
}

func TestMapError(t *testing.T) {
	cases := []struct {
		err  error
		want error
		text string
	}{
		{restErr(401, 0, "401: Unauthorized"), discord.ErrInvalidToken, "Unauthorized"},
		{restErr(403, 50013, "Missing Permissions"), discord.ErrForbidden, "Missing Permissions"},
		{restErr(403, 50001, "Missing Access"), discord.ErrForbidden, "Missing Access"},
		{restErr(404, 10011, "Unknown Role"), discord.ErrNotFound, "Unknown Role"},
		{restErr(404, 10062, "Unknown interaction"), discord.ErrNotFound, "10062"},
		{restErr(400, 10003, "Unknown Channel"), discord.ErrNotFound, "Unknown Channel"},
		{restErr(400, 50035, "Invalid Form Body"), discord.ErrBadRequest, "Invalid Form Body"},
		{restErr(400, 40060, "Interaction has already been acknowledged."), discord.ErrBadRequest, "acknowledged"},
	}
	for _, c := range cases {
		got := mapError("Op", c.err)
		if !errors.Is(got, c.want) {
			t.Errorf("%v → %v, want %v", c.err, got, c.want)
		}
		if !strings.Contains(got.Error(), c.text) || !strings.Contains(got.Error(), "Op") {
			t.Errorf("error text %q lacks %q", got, c.text)
		}
	}
	// 5xx: no sentinel, but the rest error stays in the chain and the text is safe.
	e500 := mapError("Op", &rest.Error{Response: &http.Response{StatusCode: http.StatusBadGateway}, RsBody: []byte("bad gateway")})
	for _, s := range []error{discord.ErrNotFound, discord.ErrForbidden, discord.ErrBadRequest, discord.ErrInvalidToken} {
		if errors.Is(e500, s) {
			t.Errorf("502 should not map to %v", s)
		}
	}
	var re *rest.Error
	if !errors.As(e500, &re) || !strings.Contains(e500.Error(), "bad gateway") {
		t.Errorf("502 error = %v", e500)
	}
	// A rest.Error without response must not panic.
	if got := mapError("Op", &rest.Error{}); got == nil {
		t.Error("nil")
	}
	if mapError("Op", nil) != nil {
		t.Error("nil should stay nil")
	}
	if got := mapError("Op", errors.New("dial tcp: refused")); !strings.Contains(got.Error(), "refused") {
		t.Errorf("transport = %v", got)
	}
}

func TestMapGatewayError(t *testing.T) {
	if err := mapGatewayError(fmt.Errorf("failed: %w", &websocket.CloseError{Code: 4004, Text: "Authentication failed."})); !errors.Is(err, discord.ErrInvalidToken) {
		t.Errorf("4004 = %v", err)
	}
	if err := mapGatewayError(&websocket.CloseError{Code: 4014, Text: "Disallowed intent(s)."}); !errors.Is(err, discord.ErrForbidden) {
		t.Errorf("4014 = %v", err)
	}
	if err := mapGatewayError(&websocket.CloseError{Code: 4013}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("4013 = %v", err)
	}
}

func TestTranslateServerJoinLeave(t *testing.T) {
	s := testSession()
	// Before READY nothing is a join (the server set is unknown).
	if evs := guildCreate(t, s, `{"id":"7","name":"Early"}`); len(evs) != 0 {
		t.Errorf("guild create before READY emitted %v", evs)
	}
	mustTranslate(t, s, "READY", `{"user":{"id":"600"},"guilds":[{"id":"1","unavailable":true},{"id":"2","unavailable":true}]}`)
	// Lazy loading of the READY servers: no join.
	for _, id := range []string{"1", "2"} {
		if evs := guildCreate(t, s, `{"id":"`+id+`","name":"S"}`); len(evs) != 0 {
			t.Errorf("lazy guild create %s emitted %v", id, evs)
		}
	}
	// Outage: unavailable GUILD_DELETE then GUILD_CREATE is neither a leave nor a join.
	if evs := mustTranslate(t, s, "GUILD_DELETE", `{"id":"1","unavailable":true}`); len(evs) != 0 {
		t.Errorf("unavailable guild delete emitted %v", evs)
	}
	if evs := guildCreate(t, s, `{"id":"1","name":"S"}`); len(evs) != 0 {
		t.Errorf("outage recovery emitted %v", evs)
	}
	// Unavailable GUILD_CREATE never joins.
	if evs := guildCreate(t, s, `{"id":"3","unavailable":true}`); len(evs) != 0 {
		t.Errorf("unavailable guild create emitted %v", evs)
	}
	// A new server: join, once.
	ev := one(t, guildCreate(t, s, `{"id":"3","name":"New","owner_id":"9"}`))
	if j, ok := ev.(discord.ServerJoinEvent); !ok || j.Server.ID != "3" || j.Server.Name != "New" || j.Server.OwnerID != "9" || discord.ServerIDOf(ev) != "3" {
		t.Errorf("join = %#v", ev)
	}
	if evs := guildCreate(t, s, `{"id":"3","name":"New"}`); len(evs) != 0 {
		t.Errorf("second guild create emitted %v", evs)
	}
	// Leave, then a re-invite is a join again.
	ev = one(t, mustTranslate(t, s, "GUILD_DELETE", `{"id":"3"}`))
	if l, ok := ev.(discord.ServerLeaveEvent); !ok || l.ServerID != "3" {
		t.Errorf("leave = %#v", ev)
	}
	if _, ok := one(t, guildCreate(t, s, `{"id":"3","name":"New"}`)).(discord.ServerJoinEvent); !ok {
		t.Error("re-invite is not a join")
	}
	// A new READY (reconnect without resume) replaces the set.
	mustTranslate(t, s, "READY", `{"user":{"id":"600"},"guilds":[{"id":"4","unavailable":true}]}`)
	if evs := guildCreate(t, s, `{"id":"4"}`); len(evs) != 0 {
		t.Errorf("guild of the new READY emitted %v", evs)
	}
}

func TestTranslateBans(t *testing.T) {
	s := testSession()
	body := `{"guild_id":"100","user":{"id":"200","username":"member"}}`
	added := one(t, mustTranslate(t, s, "GUILD_BAN_ADD", body))
	removed := one(t, mustTranslate(t, s, "GUILD_BAN_REMOVE", body))
	if a, ok := added.(discord.BanAddEvent); !ok || a.User.ID != "200" || a.ServerID != "100" {
		t.Fatalf("add: %+v", added)
	}
	if r, ok := removed.(discord.BanRemoveEvent); !ok || r.User.ID != "200" || r.ServerID != "100" {
		t.Fatalf("remove: %+v", removed)
	}
}
