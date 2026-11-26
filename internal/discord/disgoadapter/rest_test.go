package disgoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

type recorded struct {
	Method string
	URI    string // raw request URI (still percent-encoded)
	Auth   string
	Reason string
	Body   any
	// Raw and ContentType keep the undecoded body (multipart requests).
	Raw         []byte
	ContentType string
}

// fakeDiscord is a minimal Discord REST API served by httptest.
type fakeDiscord struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []recorded
	routes map[string]func(w http.ResponseWriter, r *http.Request) // "METHOD /path" (decoded path)
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{t: t, routes: map[string]func(http.ResponseWriter, *http.Request){}}
	f.json("GET /users/@me", 200, `{"id":"600","username":"cicord","bot":true}`)
	f.json("GET /oauth2/applications/@me", 200, `{"id":"601","name":"Cicord","flags":32768}`)
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscord) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	rec := recorded{Method: r.Method, URI: r.RequestURI, Auth: r.Header.Get("Authorization"), Reason: r.Header.Get("X-Audit-Log-Reason"),
		Raw: raw, ContentType: r.Header.Get("Content-Type")}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rec.Body)
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, rec)
	h := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if h == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":0,"message":"404: Not Found"}`)
		return
	}
	h(w, r)
}

func (f *fakeDiscord) handle(route string, h func(http.ResponseWriter, *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = h
}

func (f *fakeDiscord) json(route string, status int, body string) {
	f.handle(route, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = io.WriteString(w, body)
		}
	})
}

func (f *fakeDiscord) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

func (f *fakeDiscord) all() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeDiscord) connect(t *testing.T) discord.Session {
	t.Helper()
	c := NewConnector(WithRESTURL(f.srv.URL), WithHTTPClient(f.srv.Client()), WithLogger(slog.New(slog.DiscardHandler)))
	s, err := c.Connect(context.Background(), "Bot test-token")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestConnect(t *testing.T) {
	f := newFakeDiscord(t)
	s := f.connect(t)
	if s.Self().ID != "600" || !s.Self().Bot || s.ApplicationID() != "601" {
		t.Errorf("self = %+v app = %s", s.Self(), s.ApplicationID())
	}
	if fr, ok := s.(discord.ApplicationFlagsReader); !ok || discord.IntentsFromFlags(fr.ApplicationFlags()) != discord.MustIntents("ServerMembers") {
		t.Errorf("application flags not read: %v", ok)
	}
	if got := f.all()[0].Auth; got != "Bot test-token" {
		t.Errorf("authorization = %q", got)
	}
}

func TestConnectInvalidToken(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /users/@me", 401, `{"message":"401: Unauthorized","code":0}`)
	c := NewConnector(WithRESTURL(f.srv.URL), WithLogger(slog.New(slog.DiscardHandler)))
	_, err := c.Connect(context.Background(), "bad")
	if !errors.Is(err, discord.ErrInvalidToken) || !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Connect(context.Background(), "  "); !errors.Is(err, discord.ErrInvalidToken) {
		t.Errorf("empty token err = %v", err)
	}
}

func TestCreateChannelRequest(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("POST /guilds/1/channels", 201, `{"id":"10","guild_id":"1","name":"announcements","type":5,"parent_id":"4","position":2,
	  "permission_overwrites":[{"id":"1","type":0,"allow":"0","deny":"2048"}]}`)
	s := f.connect(t)
	ow := []discord.Overwrite{{ID: "1", Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}
	ch, err := s.CreateChannel(context.Background(), "1", discord.ChannelParams{
		Name: ptr("announcements"), Type: discord.ChannelAnnouncement, ParentID: ptr("4"), Topic: ptr("news"), Overwrites: &ow,
	}, "cicord apply rev abc")
	if err != nil {
		t.Fatal(err)
	}
	req := f.last()
	want := map[string]any{"name": "announcements", "type": 5.0, "parent_id": "4", "topic": "news",
		"permission_overwrites": []any{map[string]any{"id": "1", "type": 0.0, "allow": "0", "deny": "2048"}}}
	if req.Method != http.MethodPost || req.URI != "/guilds/1/channels" || !reflect.DeepEqual(req.Body, want) {
		t.Errorf("request = %+v", req)
	}
	if req.Reason != "cicord apply rev abc" {
		t.Errorf("audit log reason = %q", req.Reason)
	}
	if ch.ID != "10" || ch.Type != discord.ChannelAnnouncement || ch.ParentID != "4" || len(ch.Overwrites) != 1 {
		t.Errorf("channel = %+v", ch)
	}
}

func TestDeferredEphemeralResponse(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("POST /interactions/500/tok.en-1/callback", 204, "")
	s := f.connect(t)
	err := s.RespondInteraction(context.Background(), "500", "tok.en-1", discord.InteractionResponse{
		Type: discord.ResponseDeferredMessage, Data: &discord.MessageCreate{Flags: discord.MessageFlagEphemeral},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := f.last()
	if !reflect.DeepEqual(req.Body, map[string]any{"type": 5.0, "data": map[string]any{"flags": 64.0}}) {
		t.Errorf("body = %v", req.Body)
	}
	if req.Auth != "" {
		t.Errorf("interaction callbacks must not carry the bot token, got %q", req.Auth)
	}
}

func TestReactionEmojiIsEscaped(t *testing.T) {
	f := newFakeDiscord(t)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	f.handle("PUT /channels/10/messages/100/reactions/👍/@me", ok)
	f.handle("DELETE /channels/10/messages/100/reactions/party:55/@me", ok)
	s := f.connect(t)
	if err := s.AddReaction(context.Background(), "10", "100", "👍"); err != nil {
		t.Fatal(err)
	}
	if got := f.last().URI; got != "/channels/10/messages/100/reactions/%F0%9F%91%8D/@me" {
		t.Errorf("uri = %s", got)
	}
	if err := s.RemoveOwnReaction(context.Background(), "10", "100", "<:party:55>"); err != nil {
		t.Fatal(err)
	}
	if got := f.last().URI; got != "/channels/10/messages/100/reactions/party:55/@me" {
		t.Errorf("uri = %s", got)
	}
}

func TestUpdateServerAndMemberNulls(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("PATCH /guilds/1", 200, `{"id":"1","name":"S","verification_level":1}`)
	f.json("PATCH /guilds/1/members/2", 200, `{"user":{"id":"2","username":"u"},"roles":[],"nick":null,"communication_disabled_until":null}`)
	s := f.connect(t)
	srv, err := s.UpdateServer(context.Background(), "1", discord.ServerParams{SystemChannelID: ptr(""), VerificationLevel: ptr("low")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"system_channel_id": nil, "verification_level": 1.0}) {
		t.Errorf("server body = %v", f.last().Body)
	}
	if srv.VerificationLevel != "low" {
		t.Errorf("server = %+v", srv)
	}
	m, err := s.UpdateMember(context.Background(), "1", "2", discord.MemberParams{TimeoutUntil: &time.Time{}, Nick: ptr("")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"communication_disabled_until": nil, "nick": nil}) {
		t.Errorf("member body = %v", f.last().Body)
	}
	if m.TimeoutUntil != nil || m.ServerID != "1" {
		t.Errorf("member = %+v", m)
	}
	until := time.Date(2030, 1, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	if _, err := s.UpdateMember(context.Background(), "1", "2", discord.MemberParams{TimeoutUntil: &until}, ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"communication_disabled_until": "2030-01-01T11:00:00Z"}) {
		t.Errorf("timeout body = %v", f.last().Body)
	}
	// Voice: a channel moves the member, "" disconnects (channel_id: null).
	for _, c := range []struct{ id, want any }{{"30", "30"}, {"", nil}} {
		id := c.id.(string)
		if _, err := s.UpdateMember(context.Background(), "1", "2", discord.MemberParams{VoiceChannelID: &id}, ""); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.last().Body, map[string]any{"channel_id": c.want}) {
			t.Errorf("voice body = %v", f.last().Body)
		}
	}
}

func TestBanAndLockPermissions(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("PUT /guilds/1/bans/2", 204, "")
	f.json("GET /channels/10", 200, `{"id":"10","guild_id":"1","name":"c","type":0,"parent_id":"4"}`)
	f.json("GET /channels/4", 200, `{"id":"4","guild_id":"1","name":"cat","type":4,"permission_overwrites":[{"id":"1","type":0,"allow":"1024","deny":"0"}]}`)
	f.json("PATCH /channels/10", 200, `{"id":"10","guild_id":"1","name":"c","type":0,"parent_id":"4","permission_overwrites":[{"id":"1","type":0,"allow":"1024","deny":"0"}]}`)
	s := f.connect(t)
	if err := s.BanMember(context.Background(), "1", "2", 999999, "spam"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"delete_message_seconds": 604800.0}) {
		t.Errorf("ban body = %v", f.last().Body)
	}
	ch, err := s.UpdateChannel(context.Background(), "10", discord.ChannelParams{LockPermissions: ptr(true)}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"permission_overwrites": []any{map[string]any{"id": "1", "type": 0.0, "allow": "1024", "deny": "0"}}}
	if !reflect.DeepEqual(f.last().Body, want) {
		t.Errorf("lock body = %v", f.last().Body)
	}
	if len(ch.Overwrites) != 1 {
		t.Errorf("channel = %+v", ch)
	}
}

func TestErrorMappingOverHTTP(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("DELETE /guilds/1/roles/5", 403, `{"message":"Missing Permissions","code":50013}`)
	f.json("GET /guilds/1/members/2", 404, `{"message":"Unknown Member","code":10007}`)
	f.json("POST /channels/10/messages", 400, `{"message":"Invalid Form Body","code":50035,"errors":{"content":{"_errors":[{"code":"BASE_TYPE_MAX_LENGTH","message":"Must be 2000 or fewer in length."}]}}}`)
	s := f.connect(t)
	if err := s.DeleteRole(context.Background(), "1", "5", ""); !errors.Is(err, discord.ErrForbidden) || !strings.Contains(err.Error(), "Missing Permissions") {
		t.Errorf("403 = %v", err)
	}
	if _, err := s.GetMember(context.Background(), "1", "2"); !errors.Is(err, discord.ErrNotFound) || !strings.Contains(err.Error(), "Unknown Member") {
		t.Errorf("404 = %v", err)
	}
	_, err := s.SendMessage(context.Background(), "10", discord.MessageCreate{Content: strings.Repeat("x", 2001)})
	if !errors.Is(err, discord.ErrBadRequest) || !strings.Contains(err.Error(), "2000 or fewer") {
		t.Errorf("400 = %v", err)
	}
	// Invalid IDs never reach the network.
	n := len(f.all())
	if _, err := s.GetChannel(context.Background(), "../guilds/1"); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad id = %v", err)
	}
	if len(f.all()) != n {
		t.Error("invalid id was sent")
	}
}

func TestRateLimitIsRetried(t *testing.T) {
	f := newFakeDiscord(t)
	var calls int
	f.handle("GET /guilds/1/roles", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Bucket", "roles")
		w.Header().Set("via", "1.1 google") // not a Cloudflare ban
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-RateLimit-Limit", "1")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"You are being rate limited.","retry_after":1,"global":false}`)
			return
		}
		w.Header().Set("X-RateLimit-Limit", "5")
		w.Header().Set("X-RateLimit-Remaining", "4")
		w.Header().Set("X-RateLimit-Reset-After", "1")
		_, _ = io.WriteString(w, `[{"id":"1","name":"@everyone","permissions":"0","position":0}]`)
	})
	s := f.connect(t)
	start := time.Now()
	roles, err := s.ListRoles(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(roles) != 1 || !roles[0].IsEveryone("1") {
		t.Errorf("calls = %d roles = %+v", calls, roles)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("rate limiter did not wait for Retry-After (took %v)", d)
	}
}

func TestMessagesAndInteractionsRequests(t *testing.T) {
	f := newFakeDiscord(t)
	msg := `{"id":"100","channel_id":"10","author":{"id":"600","username":"cicord","bot":true},"content":"x","timestamp":"2024-01-01T00:00:00Z"}`
	f.json("PATCH /channels/10/messages/100", 200, msg)
	f.json("GET /channels/10/messages", 200, "["+msg+"]")
	f.json("GET /channels/10/messages/100", 200, msg)
	f.json("GET /channels/10/messages/101", 404, `{"message":"Unknown Message","code":10008}`)
	f.json("PATCH /webhooks/601/tok/messages/@original", 200, msg)
	f.json("POST /webhooks/601/tok", 200, msg)
	f.json("PUT /applications/601/guilds/1/commands", 200, `[]`)
	f.json("POST /users/@me/channels", 200, `{"id":"77","type":1}`)
	f.json("POST /channels/10/threads", 201, `{"id":"20","guild_id":"1","parent_id":"10","name":"t","type":12}`)
	s := f.connect(t)
	ctx := context.Background()

	if _, err := s.EditMessage(ctx, "10", "100", discord.MessageCreate{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"content": "x", "embeds": []any{}, "components": []any{}}) {
		t.Errorf("edit body = %v", f.last().Body)
	}
	ms, err := s.ListMessages(ctx, "10", discord.ListMessagesParams{Limit: 500, Before: "99"})
	if err != nil || len(ms) != 1 {
		t.Fatal(err, ms)
	}
	if u := f.last().URI; !strings.Contains(u, "limit=100") || !strings.Contains(u, "before=99") {
		t.Errorf("list uri = %s", u)
	}
	if m, err := s.GetMessage(ctx, "10", "100"); err != nil || m.ID != "100" || m.Content != "x" || m.Author.ID != "600" {
		t.Errorf("get message = %+v, %v", m, err)
	}
	if _, err := s.GetMessage(ctx, "10", "101"); !errors.Is(err, discord.ErrNotFound) {
		t.Errorf("unknown message err = %v", err)
	}
	if _, err := s.GetMessage(ctx, "10", "x"); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("invalid id err = %v", err)
	}
	if _, err := s.EditOriginalResponse(ctx, "601", "tok", discord.MessageCreate{Content: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFollowup(ctx, "601", "tok", discord.MessageCreate{Content: "secret", Flags: discord.MessageFlagEphemeral}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, map[string]any{"content": "secret", "flags": 64.0}) {
		t.Errorf("followup body = %v", f.last().Body)
	}
	if err := s.SetServerCommands(ctx, "601", "1", nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.last().Body, []any{}) {
		t.Errorf("empty command overwrite must send [], got %#v", f.last().Body)
	}
	dm, err := s.CreateDM(ctx, "2")
	if err != nil || dm.Type != discord.ChannelDM || !reflect.DeepEqual(f.last().Body, map[string]any{"recipient_id": "2"}) {
		t.Errorf("dm = %+v %v body %v", dm, err, f.last().Body)
	}
	th, err := s.CreateThread(ctx, "10", discord.ThreadParams{Name: "t", Private: true})
	if err != nil || th.Type != discord.ChannelPrivateThread || !reflect.DeepEqual(f.last().Body, map[string]any{"name": "t", "type": 12.0}) {
		t.Errorf("thread = %+v %v body %v", th, err, f.last().Body)
	}
	if _, err := s.CreateThread(ctx, "10", discord.ThreadParams{Name: "t", Private: true, MessageID: "100"}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("private thread from message err = %v", err)
	}
}

func TestUpdateSelfConcurrentWithSelf(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("PATCH /users/@me", 200, `{"id":"600","username":"renamed","bot":true}`)
	s := f.connect(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 20 {
			_ = s.Self()
		}
	}()
	go func() {
		defer wg.Done()
		u, err := s.UpdateSelf(context.Background(), discord.SelfParams{Username: ptr("renamed"), Avatar: ptr([]byte("\x89PNG...."))})
		if err != nil || u.Username != "renamed" {
			t.Errorf("update self = %+v %v", u, err)
		}
	}()
	wg.Wait()
	if s.Self().Username != "renamed" {
		t.Errorf("self = %+v", s.Self())
	}
	body := f.last().Body.(map[string]any)
	if !strings.HasPrefix(body["avatar"].(string), "data:image/png;base64,") {
		t.Errorf("avatar = %v", body["avatar"])
	}
}

func TestPresenceBeforeOpenAndClose(t *testing.T) {
	f := newFakeDiscord(t)
	s := f.connect(t)
	if err := s.SetPresence(context.Background(), discord.Presence{Status: "idle", ActivityType: "playing", ActivityName: "GitOps"}); err != nil {
		t.Fatalf("presence before open should be stored: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal("close must be idempotent")
	}
	if err := s.Open(context.Background(), 0, func(discord.Event) {}); err == nil {
		t.Error("open after close should fail")
	}
}

// Discord can return several roles at the same position (seen live after a drag in
// the client: every role at 1) and ranks the older one (lower ID) higher. ListRoles
// must hand Cicord distinct positions in that order, or a bot whose role ties with the
// roles it manages is wrongly reported below them.
func TestListRolesBreaksPositionTies(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/roles", 200, `[
		{"id":"1","name":"@everyone","position":0},
		{"id":"300","name":"Admin","position":1},
		{"id":"200","name":"Bot","position":1,"managed":true},
		{"id":"400","name":"Member","position":1},
		{"id":"150","name":"Top","position":2}
	]`)
	s := f.connect(t)
	roles, err := s.ListRoles(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range roles {
		got[r.Name] = r.Position
	}
	want := map[string]int{"@everyone": 0, "Member": 1, "Admin": 2, "Bot": 3, "Top": 4}
	for name, pos := range want {
		if got[name] != pos {
			t.Errorf("%s position = %d, want %d (all: %v)", name, got[name], pos, got)
		}
	}
}

func TestListMembersPages(t *testing.T) {
	f := newFakeDiscord(t)
	// 1000 members on the first page (ids 1..1000, 7 is a bot), then 2 more.
	f.handle("GET /guilds/1/members", func(w http.ResponseWriter, r *http.Request) {
		var page []map[string]any
		switch r.URL.Query().Get("after") {
		case "0":
			for i := 1; i <= 1000; i++ {
				page = append(page, map[string]any{"user": map[string]any{"id": strconv.Itoa(i), "username": "u", "bot": i == 7},
					"roles": []string{"50"}, "joined_at": "2026-01-02T03:04:05Z"})
			}
		case "1000":
			page = append(page, map[string]any{"user": map[string]any{"id": "1001", "username": "u"}, "nick": "N", "roles": []string{}},
				map[string]any{"user": map[string]any{"id": "1002", "username": "u", "bot": true}})
		default:
			t.Errorf("unexpected after=%q", r.URL.Query().Get("after"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})
	s := f.connect(t)
	ms, err := s.(discord.MemberLister).ListMembers(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1002 || ms[0].User.ID != "1" || !ms[0].HasRole("50") || ms[0].ServerID != "1" || ms[1001].User.ID != "1002" || ms[1000].Nick != "N" {
		t.Fatalf("members = %d, first %+v, last %+v", len(ms), ms[0], ms[len(ms)-1])
	}
	for _, rec := range f.all()[2:] {
		if !strings.Contains(rec.URI, "limit=1000") {
			t.Errorf("request %s: no limit=1000", rec.URI)
		}
	}
	bots, err := s.(discord.AnalyticsReader).ListServerBots(context.Background(), "1")
	if err != nil || !reflect.DeepEqual(bots, []string{"7", "1002"}) {
		t.Fatalf("bots = %v, %v", bots, err)
	}

	f.json("GET /guilds/2/members", 403, `{"message":"Missing Access","code":50001}`)
	if _, err := s.(discord.MemberLister).ListMembers(context.Background(), "2"); !errors.Is(err, discord.ErrForbidden) {
		t.Errorf("403 = %v", err)
	}
}

func TestModerationRequests(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/bans", 200, `[{"reason":"reviewed","user":{"id":"31","username":"example"}}]`)
	f.json("DELETE /guilds/1/bans/31", 204, "")
	f.json("POST /channels/10/messages/bulk-delete", 204, "")
	f.json("PATCH /guilds/1/members/31", 200, `{"user":{"id":"31","username":"example"},"roles":[]}`)
	s := f.connect(t)
	bans, err := s.ListBans(t.Context(), "1", "20", 10)
	if err != nil || len(bans) != 1 || bans[0].User.ID != "31" || bans[0].Reason != "reviewed" || f.last().URI != "/guilds/1/bans?after=20&limit=10" {
		t.Fatalf("list bans: %+v, %s, %v", bans, f.last().URI, err)
	}
	if err := s.UnbanMember(t.Context(), "1", "31", "expiry"); err != nil || f.last().Reason != "expiry" {
		t.Fatalf("unban: %+v %v", f.last(), err)
	}
	if err := s.BulkDeleteMessages(t.Context(), "10", []string{"31", "32"}, "purge"); err != nil || !reflect.DeepEqual(f.last().Body, map[string]any{"messages": []any{"31", "32"}}) {
		t.Fatalf("bulk delete: %+v %v", f.last(), err)
	}
	if _, err := s.UpdateMember(t.Context(), "1", "31", discord.MemberParams{RoleIDs: []string{}}, "release"); err != nil || !reflect.DeepEqual(f.last().Body, map[string]any{"roles": []any{}}) {
		t.Fatalf("set roles: %+v %v", f.last(), err)
	}
}

func TestAuditListPreservesEvidence(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/audit-logs", 200, `{"users":[{"id":"22","username":"mod"}],"audit_log_entries":[{"id":"500","action_type":31,"user_id":"22","target_id":"33","reason":"staff review","options":{"count":"2"},"changes":[{"key":"permissions","old_value":"1","new_value":"2"}]}]}`)
	s := f.connect(t)
	items, err := s.ListAuditLog(t.Context(), "1", discord.AuditQuery{Limit: 10, Action: 31, ActorID: "22", Before: "55"})
	if err != nil || len(items) != 1 {
		t.Fatalf("entries: %+v %v", items, err)
	}
	a := items[0]
	if a.Actor == nil || a.Actor.Username != "mod" || a.Reason != "staff review" || string(a.Options["count"]) != `"2"` || len(a.Changes) != 1 || a.Changes[0].Key != "permissions" {
		t.Fatalf("evidence: %+v", a)
	}
	if f.last().URI != "/guilds/1/audit-logs?action_type=31&before=55&limit=10&user_id=22" {
		t.Fatal(f.last().URI)
	}
}
