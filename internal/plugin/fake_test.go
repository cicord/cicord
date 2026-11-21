package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/npjonath/codi/internal/build"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

// ───────────────────────────────────────────────────────────── fake Discord client

type call struct {
	Name string
	Args []any
}

// fakeDiscord implements the discord.Client methods the plugin host uses and records every
// call. Unimplemented methods panic through the nil embedded interface.
type fakeDiscord struct {
	discord.Client

	mu     sync.Mutex
	calls  []call
	nextID int
	// hook runs before each call; a non-nil error is returned by the call.
	hook     func(ctx context.Context, name string, args []any) error
	channels map[string]discord.Channel
	members  map[string]discord.Member
}

func newFakeDiscord() *fakeDiscord {
	return &fakeDiscord{nextID: 5000, channels: map[string]discord.Channel{}, members: map[string]discord.Member{}}
}

func (f *fakeDiscord) record(ctx context.Context, name string, args ...any) error {
	f.mu.Lock()
	f.calls = append(f.calls, call{Name: name, Args: args})
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, name, args)
	}
	return nil
}

func (f *fakeDiscord) id() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return strconv.Itoa(f.nextID)
}

func (f *fakeDiscord) Calls(name string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDiscord) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for k, c := range f.calls {
		out[k] = c.Name
	}
	return out
}

func (f *fakeDiscord) GetUser(ctx context.Context, id string) (discord.User, error) {
	return discord.User{ID: id, Username: "user" + id}, f.record(ctx, "GetUser", id)
}

func (f *fakeDiscord) GetChannel(ctx context.Context, id string) (discord.Channel, error) {
	if err := f.record(ctx, "GetChannel", id); err != nil {
		return discord.Channel{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.channels[id]
	if !ok {
		return discord.Channel{}, discord.ErrNotFound
	}
	return ch, nil
}

func (f *fakeDiscord) SendMessage(ctx context.Context, channelID string, m discord.MessageCreate) (discord.Message, error) {
	if err := f.record(ctx, "SendMessage", channelID, m); err != nil {
		return discord.Message{}, err
	}
	return discord.Message{ID: f.id(), ChannelID: channelID, Content: m.Content, Author: discord.User{ID: "1", Username: "codi", Bot: true}, CreatedAt: time.Unix(1700000000, 0)}, nil
}

func (f *fakeDiscord) EditMessage(ctx context.Context, channelID, messageID string, m discord.MessageCreate) (discord.Message, error) {
	if err := f.record(ctx, "EditMessage", channelID, messageID, m); err != nil {
		return discord.Message{}, err
	}
	return discord.Message{ID: messageID, ChannelID: channelID, Content: m.Content}, nil
}

func (f *fakeDiscord) DeleteMessage(ctx context.Context, channelID, messageID, reason string) error {
	return f.record(ctx, "DeleteMessage", channelID, messageID, reason)
}

func (f *fakeDiscord) ListMessages(ctx context.Context, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	if err := f.record(ctx, "ListMessages", channelID, p); err != nil {
		return nil, err
	}
	return []discord.Message{
		{ID: "901", ChannelID: channelID, ServerID: "10", Content: "hello", Author: discord.User{ID: "42", Username: "alice"}},
		{ID: "902", ChannelID: channelID, ServerID: "10", Content: "world", Author: discord.User{ID: "43", Username: "bob"}},
	}, nil
}

func (f *fakeDiscord) AddReaction(ctx context.Context, channelID, messageID, emoji string) error {
	return f.record(ctx, "AddReaction", channelID, messageID, emoji)
}

func (f *fakeDiscord) AddMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	return f.record(ctx, "AddMemberRole", serverID, userID, roleID, reason)
}

func (f *fakeDiscord) RemoveMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	return f.record(ctx, "RemoveMemberRole", serverID, userID, roleID, reason)
}

func (f *fakeDiscord) UpdateMember(ctx context.Context, serverID, userID string, p discord.MemberParams, reason string) (discord.Member, error) {
	return discord.Member{ServerID: serverID, User: discord.User{ID: userID}}, f.record(ctx, "UpdateMember", serverID, userID, p, reason)
}

func (f *fakeDiscord) KickMember(ctx context.Context, serverID, userID, reason string) error {
	return f.record(ctx, "KickMember", serverID, userID, reason)
}

func (f *fakeDiscord) BanMember(ctx context.Context, serverID, userID string, del int, reason string) error {
	return f.record(ctx, "BanMember", serverID, userID, del, reason)
}

func (f *fakeDiscord) GetMember(ctx context.Context, serverID, userID string) (discord.Member, error) {
	if err := f.record(ctx, "GetMember", serverID, userID); err != nil {
		return discord.Member{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.members[userID]
	if !ok {
		return discord.Member{}, discord.ErrNotFound
	}
	return m, nil
}

func (f *fakeDiscord) CreateDM(ctx context.Context, userID string) (discord.Channel, error) {
	if err := f.record(ctx, "CreateDM", userID); err != nil {
		return discord.Channel{}, err
	}
	return discord.Channel{ID: "dm-" + userID, Type: discord.ChannelDM}, nil
}

func (f *fakeDiscord) CreateChannel(ctx context.Context, serverID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := f.record(ctx, "CreateChannel", serverID, p, reason); err != nil {
		return discord.Channel{}, err
	}
	ch := discord.Channel{ID: f.id(), ServerID: serverID, Name: *p.Name, Type: p.Type}
	f.mu.Lock()
	f.channels[ch.ID] = ch
	f.mu.Unlock()
	return ch, nil
}

func (f *fakeDiscord) DeleteChannel(ctx context.Context, channelID, reason string) error {
	return f.record(ctx, "DeleteChannel", channelID, reason)
}

func (f *fakeDiscord) CreateThread(ctx context.Context, channelID string, p discord.ThreadParams) (discord.Channel, error) {
	if err := f.record(ctx, "CreateThread", channelID, p); err != nil {
		return discord.Channel{}, err
	}
	return discord.Channel{ID: f.id(), ServerID: "10", Name: p.Name, Type: discord.ChannelPublicThread, ParentID: channelID}, nil
}

func (f *fakeDiscord) RespondInteraction(ctx context.Context, id, token string, r discord.InteractionResponse) error {
	return f.record(ctx, "RespondInteraction", id, token, r)
}

func (f *fakeDiscord) EditOriginalResponse(ctx context.Context, appID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := f.record(ctx, "EditOriginalResponse", appID, token, m); err != nil {
		return discord.Message{}, err
	}
	return discord.Message{ID: f.id(), Content: m.Content}, nil
}

func (f *fakeDiscord) CreateFollowup(ctx context.Context, appID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := f.record(ctx, "CreateFollowup", appID, token, m); err != nil {
		return discord.Message{}, err
	}
	return discord.Message{ID: f.id(), ChannelID: "100", Content: m.Content, Author: discord.User{ID: "1", Bot: true}}, nil
}

// ───────────────────────────────────────────────────────────── fake state

type memKV struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

func newMemKV() *memKV { return &memKV{data: map[string]json.RawMessage{}} }

func (k *memKV) Get(_ context.Context, key string) (json.RawMessage, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.data[key]
	if !ok {
		return nil, state.ErrNotFound
	}
	return v, nil
}

func (k *memKV) Set(_ context.Context, key string, v json.RawMessage, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.data[key] = append(json.RawMessage(nil), v...)
	return nil
}

func (k *memKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.data, key)
	return nil
}

func (k *memKV) Increment(_ context.Context, key string, by float64) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var n float64
	if v, ok := k.data[key]; ok {
		if err := json.Unmarshal(v, &n); err != nil {
			return 0, fmt.Errorf("not a number")
		}
	}
	n += by
	k.data[key] = json.RawMessage(strconv.FormatFloat(n, 'f', -1, 64))
	return n, nil
}

func (k *memKV) List(_ context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var keys []string
	for key := range k.data {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[limit-1]
	}
	items := make([]state.KVItem, len(keys))
	for n, key := range keys {
		items[n] = state.KVItem{Key: key, Value: k.data[key]}
	}
	return items, next, nil
}

type memTasks struct {
	mu    sync.Mutex
	tasks map[string]state.Task
}

func newMemTasks() *memTasks { return &memTasks{tasks: map[string]state.Task{}} }

func (q *memTasks) Schedule(_ context.Context, t state.Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[t.ID] = t
	return nil
}

func (q *memTasks) Cancel(_ context.Context, scope, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.tasks[id]; !ok || t.Scope != scope {
		return state.ErrNotFound
	}
	delete(q.tasks, id)
	return nil
}

func (q *memTasks) Due(_ context.Context, now time.Time, limit int) ([]state.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []state.Task
	for _, t := range q.tasks {
		if !t.RunAt.After(now) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].RunAt.Before(out[b].RunAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (q *memTasks) Complete(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.tasks, id)
	return nil
}

func (q *memTasks) All() []state.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []state.Task
	for _, t := range q.tasks {
		out = append(out, t)
	}
	return out
}

type memResources struct {
	mu  sync.Mutex
	res map[string]state.Resource
}

func newMemResources() *memResources { return &memResources{res: map[string]state.Resource{}} }

func resKey(server string, kind state.ResourceKind, key string) string {
	return server + "|" + string(kind) + "|" + key
}

func (m *memResources) Get(_ context.Context, server string, kind state.ResourceKind, key string) (state.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[resKey(server, kind, key)]
	if !ok {
		return r, state.ErrNotFound
	}
	return r, nil
}

func (m *memResources) GetByID(_ context.Context, server, id string) (state.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.res {
		if r.Server == server && r.ID == id {
			return r, nil
		}
	}
	return state.Resource{}, state.ErrNotFound
}

func (m *memResources) Put(_ context.Context, r state.Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.res[resKey(r.Server, r.Kind, r.Key)] = r
	return nil
}

func (m *memResources) Delete(_ context.Context, server string, kind state.ResourceKind, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.res, resKey(server, kind, key))
	return nil
}

func (m *memResources) List(_ context.Context, server string) ([]state.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Resource
	for _, r := range m.res {
		if r.Server == server {
			out = append(out, r)
		}
	}
	return out, nil
}

// fakeResolver resolves keys of the test server (ID "10").
type fakeResolver struct {
	ids      map[state.ResourceKind]map[string]string
	channels map[string]discord.Channel
	roles    map[string]discord.Role
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		ids: map[state.ResourceKind]map[string]string{
			state.KindChannel:  {"general": "100", "rules": "101"},
			state.KindRole:     {"member": "200", "moderator": "201"},
			state.KindCategory: {"support": "300"},
		},
		channels: map[string]discord.Channel{
			"100": {ID: "100", ServerID: "10", Name: "général", Type: discord.ChannelText},
			"101": {ID: "101", ServerID: "10", Name: "règles", Type: discord.ChannelText},
			"102": {ID: "102", ServerID: "10", Name: "unmanaged", Type: discord.ChannelText},
		},
		roles: map[string]discord.Role{
			"10":  {ID: "10", Name: "@everyone"},
			"200": {ID: "200", Name: "Membre", Color: 0x3498db},
			"201": {ID: "201", Name: "Modo"},
		},
	}
}

func (r *fakeResolver) ID(kind state.ResourceKind, key string) (string, bool) {
	id, ok := r.ids[kind][key]
	return id, ok
}

func (r *fakeResolver) Key(id string) (state.ResourceKind, string, bool) {
	for kind, m := range r.ids {
		for k, v := range m {
			if v == id {
				return kind, k, true
			}
		}
	}
	return "", "", false
}

func (r *fakeResolver) Channel(id string) (discord.Channel, bool) {
	ch, ok := r.channels[id]
	return ch, ok
}

func (r *fakeResolver) Role(id string) (discord.Role, bool) {
	ro, ok := r.roles[id]
	return ro, ok
}

type fakePortal struct{}

func (fakePortal) Link(path, userID string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("https://portal.example.com%s?member=%s&ttl=%s", path, userID, ttl), nil
}

// ───────────────────────────────────────────────────────────── log capture

type logEntry struct {
	Level slog.Level
	Msg   string
	Attrs map[string]any
}

type logRecorder struct {
	mu      *sync.Mutex
	entries *[]logEntry
	attrs   []slog.Attr
}

func newLogRecorder() *logRecorder {
	return &logRecorder{mu: &sync.Mutex{}, entries: &[]logEntry{}}
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	e := logEntry{Level: r.Level, Msg: r.Message, Attrs: map[string]any{}}
	for _, a := range l.attrs {
		e.Attrs[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool { e.Attrs[a.Key] = a.Value.Any(); return true })
	l.mu.Lock()
	*l.entries = append(*l.entries, e)
	l.mu.Unlock()
	return nil
}
func (l *logRecorder) WithAttrs(as []slog.Attr) slog.Handler {
	return &logRecorder{mu: l.mu, entries: l.entries, attrs: append(append([]slog.Attr{}, l.attrs...), as...)}
}
func (l *logRecorder) WithGroup(string) slog.Handler { return l }

func (l *logRecorder) Find(msg string) []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []logEntry
	for _, e := range *l.entries {
		if e.Msg == msg {
			out = append(out, e)
		}
	}
	return out
}

func (l *logRecorder) Messages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(*l.entries))
	for k, e := range *l.entries {
		out[k] = e.Msg
	}
	return out
}

// ───────────────────────────────────────────────────────────── harness

var allGrants = []string{
	capMessagesRead, capMessagesSend, capMessagesManage, capReactions, capMembersRead,
	capMembersManage, capMembersModerate, capRolesAssign, capChannelsRead, capChannelsManage,
	capThreadsManage, capDMSend, capKV, capScheduler, capHTTPFetch, capPortalRoutes,
	capPortalLinks, capEventsPublish, capEventsSubscribe,
}

type harness struct {
	t         *testing.T
	inst      *Instance
	def       *Definition
	dc        *fakeDiscord
	kv        *memKV
	tasks     *memTasks
	resources *memResources
	logs      *logRecorder
	published chan publishedEvent
}

type publishedEvent struct {
	Topic   string
	Payload string
}

// bundleSource bundles a single-file TypeScript plugin.
func bundleSource(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := build.Bundle(dir, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	return res.Code
}

func testEnv(dc *fakeDiscord) Env {
	return Env{
		BotKey: "moderator", InstanceName: "inst", PluginName: "test", PluginVersion: "0.1.0",
		Bot: discord.User{ID: "1", Username: "codi", Bot: true}, ApplicationID: "app",
		ServerKey: "ma-commu", ServerID: "10", ServerName: "Ma Commu",
		Config: map[string]any{"greeting": "hi"}, Grants: allGrants,
		Discord: dc, Resolver: newFakeResolver(), Portal: fakePortal{}, TaskScope: "plugin:moderator/inst/ma-commu",
	}
}

// start bundles src, inspects it and starts an instance. mod customizes the Env.
func start(t *testing.T, src string, mod func(*Env)) *harness {
	t.Helper()
	return startCode(t, bundleSource(t, src), mod)
}

func startCode(t *testing.T, code string, mod func(*Env)) *harness {
	t.Helper()
	def, err := Inspect(code)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, def: def, dc: newFakeDiscord(), kv: newMemKV(), tasks: newMemTasks(), resources: newMemResources(),
		logs: newLogRecorder(), published: make(chan publishedEvent, 16)}
	env := testEnv(h.dc)
	env.KV, env.Tasks, env.Resources = h.kv, h.tasks, h.resources
	env.Logger = slog.New(h.logs)
	env.Publish = func(topic string, payload json.RawMessage) error {
		h.published <- publishedEvent{topic, string(payload)}
		return nil
	}
	if mod != nil {
		mod(&env)
	}
	inst, err := NewInstance(code, def, env)
	if err != nil {
		t.Fatal(err)
	}
	h.inst = inst
	t.Cleanup(func() { _ = inst.Stop(context.Background()) })
	if err := inst.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) command(name string, opts []discord.InteractionOption, res discord.Resolved) error {
	it := discord.Interaction{ID: "it-" + name, Token: "tok", ApplicationID: "app", Type: discord.InteractionCommand,
		ServerID: "10", ChannelID: "100", User: discord.User{ID: "42", Username: "alice"},
		Member:      &discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, RoleIDs: []string{"200"}},
		CommandName: name, Options: opts, Resolved: res}
	return h.inst.HandleInteraction(context.Background(), it, "command", name)
}

// responses returns the RespondInteraction responses.
func (h *harness) responses() []discord.InteractionResponse {
	var out []discord.InteractionResponse
	for _, c := range h.dc.Calls("RespondInteraction") {
		out = append(out, c.Args[2].(discord.InteractionResponse))
	}
	return out
}

func (h *harness) logged(msg string) logEntry {
	h.t.Helper()
	got := h.logs.Find(msg)
	if len(got) == 0 {
		h.t.Fatalf("no log %q; logs: %q", msg, h.logs.Messages())
	}
	return got[len(got)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
