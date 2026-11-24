package disgoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"

	"github.com/npjonath/cicord/internal/discord"
)

func withoutGatewayCompression() Option {
	none := gateway.CompressionNone
	return func(c *config) { c.gatewayCompression = &none }
}

type gwMsg struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int            `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// fakeGateway speaks just enough of the Discord gateway protocol: HELLO, expects
// IDENTIFY, then runs script with a send function for dispatches.
type fakeGateway struct {
	srv      *httptest.Server
	identify chan map[string]any
	ops      chan gwMsg // client → server messages after IDENTIFY
}

func newFakeGateway(t *testing.T, script func(send func(t string, d string), closeWith func(code int, text string), ops <-chan gwMsg)) *fakeGateway {
	f := &fakeGateway{identify: make(chan map[string]any, 1), ops: make(chan gwMsg, 16)}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// nosemgrep: go.gorilla.security.audit.websocket-missing-origin-check.websocket-missing-origin-check -- fake Discord gateway on a loopback test server
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var wmu sync.Mutex
		write := func(m any) {
			wmu.Lock()
			defer wmu.Unlock()
			_ = c.WriteJSON(m)
		}
		write(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 45000}})
		var id gwMsg
		if err := c.ReadJSON(&id); err != nil || id.Op != 2 {
			return
		}
		var d map[string]any
		_ = json.Unmarshal(id.D, &d)
		f.identify <- d
		go func() {
			for {
				var m gwMsg
				if err := c.ReadJSON(&m); err != nil {
					close(f.ops)
					return
				}
				f.ops <- m
			}
		}()
		seq := 0
		send := func(t, d string) {
			seq++
			s := seq
			write(gwMsg{Op: 0, T: t, S: &s, D: json.RawMessage(d)})
		}
		closeWith := func(code int, text string) {
			wmu.Lock()
			defer wmu.Unlock()
			_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, text))
		}
		script(send, closeWith, f.ops)
		// Keep the connection open until the client goes away.
		for range f.ops {
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGateway) url() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") }

const readyPayload = `{"v":10,"user":{"id":"600","username":"cicord","discriminator":"0","bot":true},
  "guilds":[{"id":"1","unavailable":true}],"session_id":"sess","resume_gateway_url":"%s",
  "application":{"id":"601","flags":0}}`

func skipUnderRace(t *testing.T) {
	if raceEnabled {
		t.Skip("disgo v0.20.0 gateway has a data race on heartbeatCancel (see race_on_test.go)")
	}
}

func TestGatewayEndToEnd(t *testing.T) {
	skipUnderRace(t)
	const n = 200
	var fg *fakeGateway
	fg = newFakeGateway(t, func(send func(string, string), _ func(int, string), ops <-chan gwMsg) {
		send("READY", fmt.Sprintf(readyPayload, fg.url()))
		send("GUILD_CREATE", `{"id":"1","name":"S","owner_id":"2","roles":[],"channels":[],"members":[
		  {"user":{"id":"2","username":"a","discriminator":"0"},"roles":["5"],"joined_at":"2024-01-01T00:00:00Z","flags":0}]}`)
		// The adapter requests the member list (op 8) because ServerMembers is on.
		select {
		case m := <-ops:
			if m.Op == 8 {
				send("GUILD_MEMBERS_CHUNK", `{"guild_id":"1","chunk_index":0,"chunk_count":1,"members":[
				  {"user":{"id":"3","username":"b","discriminator":"0"},"roles":["7"],"joined_at":"2024-01-01T00:00:00Z","flags":0}]}`)
			}
		case <-time.After(3 * time.Second):
		}
		send("GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"2","username":"a","discriminator":"0"},"roles":["5","6"],"joined_at":"2024-01-01T00:00:00Z","flags":0}`)
		send("GUILD_MEMBER_UPDATE", `{"guild_id":"1","user":{"id":"3","username":"b","discriminator":"0"},"roles":[],"joined_at":"2024-01-01T00:00:00Z","flags":0}`)
		for i := range n {
			send("MESSAGE_CREATE", fmt.Sprintf(`{"id":"%d","channel_id":"10","guild_id":"1","author":{"id":"2","username":"a","discriminator":"0"},
			  "content":"m%d","timestamp":"2024-01-01T00:00:00Z","type":0,"flags":0,"attachments":[],"embeds":[],"mentions":[],"mention_roles":[],"pinned":false,"mention_everyone":false,"tts":false}`, i+1, i))
		}
	})

	f := newFakeDiscord(t)
	c := NewConnector(WithRESTURL(f.srv.URL), WithGatewayURL(fg.url()), withoutGatewayCompression(), WithLogger(slog.New(slog.DiscardHandler)))
	s, err := c.Connect(context.Background(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if err := s.SetPresence(context.Background(), discord.Presence{Status: "idle"}); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		events []discord.Event
	)
	allDone := make(chan struct{})
	handler := func(e discord.Event) {
		time.Sleep(100 * time.Microsecond) // slow-ish handler
		mu.Lock()
		events = append(events, e)
		if l := len(events); l == n+3 {
			close(allDone)
		}
		mu.Unlock()
	}
	openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	intents := discord.MustIntents("Servers", "ServerMembers", "ServerMessages")
	err = s.Open(openCtx, intents, handler)
	cancel() // the connection must outlive Open's context
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id := <-fg.identify
	if id["token"] != "test-token" || id["intents"] != float64(intents) {
		t.Errorf("identify = %v", id)
	}
	if p, _ := id["presence"].(map[string]any); p == nil || p["status"] != "idle" {
		t.Errorf("identify presence = %v", id["presence"])
	}

	select {
	case <-allDone:
	case <-time.After(10 * time.Second):
		mu.Lock()
		t.Fatalf("received %d/%d events", len(events), n+3)
	}
	mu.Lock()
	defer mu.Unlock()
	r, ok := events[0].(discord.ReadyEvent)
	if !ok || r.User.ID != "600" || len(r.ServerIDs) != 1 {
		t.Fatalf("first event = %#v", events[0])
	}
	u1 := events[1].(discord.MemberUpdateEvent)
	if u1.Previous == nil || len(u1.Previous.RoleIDs) != 1 || len(u1.Member.RoleIDs) != 2 {
		t.Errorf("member update (GUILD_CREATE seed) = %#v", u1)
	}
	u2 := events[2].(discord.MemberUpdateEvent)
	if u2.Previous == nil || u2.Previous.RoleIDs[0] != "7" {
		t.Errorf("member update (chunk seed) = %#v", u2)
	}
	for i := range n {
		m := events[3+i].(discord.MessageCreateEvent)
		if m.Message.ID != fmt.Sprint(i+1) {
			t.Fatalf("message %d has id %s: order not preserved", i+1, m.Message.ID)
		}
	}
}

func TestGatewayDisallowedIntents(t *testing.T) {
	skipUnderRace(t)
	fg := newFakeGateway(t, func(_ func(string, string), closeWith func(int, string), _ <-chan gwMsg) {
		closeWith(4014, "Disallowed intent(s).")
	})
	f := newFakeDiscord(t)
	c := NewConnector(WithRESTURL(f.srv.URL), WithGatewayURL(fg.url()), withoutGatewayCompression(), WithLogger(slog.New(slog.DiscardHandler)))
	s, err := c.Connect(context.Background(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = s.Open(ctx, discord.MustIntents("MessageContent"), func(discord.Event) {})
	if !errors.Is(err, discord.ErrForbidden) {
		t.Errorf("open err = %v, want ErrForbidden", err)
	}
	// A failed Open leaves the session usable for another attempt.
	if err := s.Open(ctx, 0, nil); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("nil handler err = %v", err)
	}
}
