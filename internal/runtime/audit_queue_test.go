package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// Discord audit action types.
const (
	auditChannelDelete = 12
	auditRoleCreate    = 30
	auditRoleUpdate    = 31
)

var uncheckedSummary = regexp.MustCompile(`msg="audit entries not checked for attribution[^"]*" queueFull=(\d+) checkFailed=(\d+)`)

// noAuditAlarm fails when the runtime raised the CORE-04 alert or logged audit entries
// one by one as unchecked or unconfirmed.
func noAuditAlarm(t *testing.T, e *env, logs string) {
	t.Helper()
	for _, s := range e.rt.Status().Servers {
		if s.UnconfirmedAudit != nil {
			t.Fatalf("CORE-04 alert raised: %+v", s.UnconfirmedAudit)
		}
	}
	for _, bad := range []string{"unconfirmed audit activity", "queue full", "entry not checked"} {
		if strings.Contains(logs, bad) {
			t.Fatalf("log contains %q:\n%s", bad, logs)
		}
	}
}

// boundTargets returns the targets of the journal operations bound to an audit entry,
// by audit action.
func boundTargets(t *testing.T, e *env) map[string]bool {
	t.Helper()
	kv := e.rt.Store().KV("emitted:" + e.serverID)
	out := map[string]bool{}
	cursor := ""
	for {
		rows, next, err := kv.List(t.Context(), "entry:", 100, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			var key string
			if json.Unmarshal(row.Value, &key) == nil {
				if parts := strings.Split(key, ":"); len(parts) > 2 {
					out[parts[1]+":"+parts[2]] = true
				}
			}
		}
		if next == "" {
			return out
		}
		cursor = next
	}
}

func (e *env) auditIdle() bool {
	q := &e.rt.auditQ
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending == 0
}

// The first sync of a large server creates its roles in one burst of audit entries.
// Every one is checked and confirmed: no entry is dropped, no warning burst, no alert.
func TestFirstSyncOfALargeServerChecksEveryOwnEntry(t *testing.T) {
	const n = 120
	e := auditObserverWith(t, func(dir string) {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "    bulk-%d:\n      name: Bulk %d\n      permissions: [ManageRoles]\n", i, i)
		}
		replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), "  roles:\n", "  roles:\n"+b.String())
	})
	var ids []string
	for i := range n {
		role, ok := e.world.RoleByName(e.serverID, fmt.Sprintf("Bulk %d", i))
		if !ok {
			t.Fatalf("role Bulk %d not created", i)
		}
		ids = append(ids, role.ID)
	}
	action := strconv.Itoa(discord.JournalAction(auditRoleCreate))
	e.waitFor("every created role confirmed", func() bool {
		bound := boundTargets(t, e)
		for _, id := range ids {
			if !bound[action+":"+id] {
				return false
			}
		}
		return e.auditIdle()
	})
	for _, id := range ids {
		for _, got := range attributions(e, "role.create", id) {
			if got != "cicord/confirmed" {
				t.Errorf("role.create on %s: %s, want cicord/confirmed", id, got)
			}
		}
	}
	logs := e.logs.String()
	noAuditAlarm(t, e, logs)
	if uncheckedSummary.MatchString(logs) {
		t.Fatalf("entries not checked:\n%s", logs)
	}
}

// A gateway burst of audit entries (a raid, own actions of a large plugin) is
// attributed in full: own entries are confirmed, a raider's entries keep its actor.
func TestGatewayAuditBurstIsCheckedInFull(t *testing.T) {
	const n = 200
	e := auditObserver(t)
	e.waitFor("first sync attributed", e.auditIdle)
	_, ctx := e.ownCall(t)
	scope := discord.Operations(ctx)
	raider := e.world.CreateUser("raider")
	for i := range n {
		target := strconv.FormatUint(1<<60+uint64(i), 10)
		scope.Emitted(ctx, "UpdateRole", target, time.Now())
		if err := e.world.AuditLog(e.serverID, e.bot.ID, auditRoleUpdate, target); err != nil {
			t.Fatal(err)
		}
		if err := e.world.AuditLog(e.serverID, raider.ID, auditChannelDelete, target); err != nil {
			t.Fatal(err)
		}
	}
	count := func(action, want string) (got, matching int) {
		items, _, err := e.rt.Store().KV(welcomeScope).List(t.Context(), "audit:"+action+":1", 1000, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			var v string
			_ = json.Unmarshal(it.Value, &v)
			if v == want {
				matching++
			}
		}
		return len(items), matching
	}
	e.waitFor("burst observed", func() bool {
		own, _ := count("role.update", "")
		raid, _ := count("channel.delete", "")
		return own == n && raid == n && e.auditIdle()
	})
	if _, ok := count("role.update", "cicord/confirmed"); ok != n {
		t.Fatalf("%d of %d own entries confirmed", ok, n)
	}
	if _, ok := count("channel.delete", "human/confirmed"); ok != n {
		t.Fatalf("%d of %d raider entries attributed to the raider", ok, n)
	}
	noAuditAlarm(t, e, e.logs.String())
}

// An overflowing queue fails safe: entries that do not fit are not checked, so they
// raise no alert, reach subscribers as unknown, and are logged once with a count.
func TestAuditOverflowFailsSafeAndLogsOnce(t *testing.T) {
	var hold atomic.Bool
	release := make(chan struct{})
	oldCap, oldWorkers, oldGate := auditQueueCap, auditWorkers, auditGate
	auditQueueCap, auditWorkers = 4, 1
	auditGate = func() {
		if hold.Load() {
			<-release
		}
	}
	t.Cleanup(func() { auditQueueCap, auditWorkers, auditGate = oldCap, oldWorkers, oldGate })
	e := auditObserver(t)
	e.waitFor("first sync attributed", e.auditIdle)
	mark := len(e.logs.String())
	_, ctx := e.ownCall(t)
	scope := discord.Operations(ctx)
	hold.Store(true)
	const n = 30
	for i := range n {
		target := strconv.FormatUint(1<<60+uint64(i), 10)
		scope.Emitted(ctx, "UpdateRole", target, time.Now())
		if err := e.world.AuditLog(e.serverID, e.bot.ID, auditRoleUpdate, target); err != nil {
			t.Fatal(err)
		}
	}
	observed := func() map[string]int {
		items, _, err := e.rt.Store().KV(welcomeScope).List(t.Context(), "audit:role.update:1", 1000, "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, it := range items {
			var v string
			_ = json.Unmarshal(it.Value, &v)
			out[v]++
		}
		return out
	}
	e.waitFor("overflow dispatched as unknown", func() bool { return observed()["unknown/unknown"] == n-auditQueueCap })
	close(release)
	e.waitFor("queue drained", func() bool { return observed()["cicord/confirmed"] == auditQueueCap && e.auditIdle() })
	logs := e.logs.String()[mark:]
	noAuditAlarm(t, e, logs)
	got := uncheckedSummary.FindAllStringSubmatch(logs, -1)
	if len(got) != 1 || got[0][1] != strconv.Itoa(n-auditQueueCap) || got[0][2] != "0" {
		t.Fatalf("overflow summaries %v, want one with queueFull=%d:\n%s", got, n-auditQueueCap, logs)
	}
}

// A journal check that cannot run (the state store fails or times out) is not
// evidence: the entry is cicord/unknown, raises no alert, and is counted in the summary.
func TestFailedAuditCheckNeverRaisesTheAlert(t *testing.T) {
	old := auditCheckTimeout
	oldRecheck := auditRecheck
	auditCheckTimeout, auditRecheck = time.Nanosecond, 10*time.Millisecond
	t.Cleanup(func() { auditCheckTimeout, auditRecheck = old, oldRecheck })
	e := auditObserver(t)
	e.waitFor("first sync attributed", e.auditIdle)
	_, ctx := e.ownCall(t)
	scope := discord.Operations(ctx)
	role, _ := e.world.RoleByName(e.serverID, "Member")
	scope.Emitted(ctx, "UpdateRole", role.ID, time.Now())
	if err := e.world.AuditLog(e.serverID, e.bot.ID, auditRoleUpdate, role.ID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("entry observed", func() bool { return len(attributions(e, "role.update", role.ID)) == 1 && e.auditIdle() })
	if got := attributions(e, "role.update", role.ID)[0]; got != "cicord/unknown" {
		t.Fatalf("unchecked own entry attributed %s, want cicord/unknown", got)
	}
	logs := e.logs.String()
	noAuditAlarm(t, e, logs)
	if !regexp.MustCompile(`msg="audit entries not checked for attribution[^"]*" queueFull=\d+ checkFailed=[1-9]`).MatchString(logs) {
		t.Fatalf("no summary of failed checks:\n%s", logs)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	entry := discord.AuditLogEntryEvent{ServerID: e.serverID, ID: strconv.FormatUint(uint64(time.Now().UnixMilli()-1420070400000)<<22, 10), UserID: e.bot.ID, TargetID: role.ID, ActionType: auditRoleUpdate}
	if a, checked := e.rt.checkAttribution(canceled, entry); checked || a.Kind != "cicord" || a.Certainty != "unknown" {
		t.Fatalf("check with a failing store: %+v checked=%v", a, checked)
	}
}

type countingUsers struct {
	discord.Client
	calls atomic.Int32
	fail  atomic.Bool
}

func (c *countingUsers) GetUser(ctx context.Context, id string) (discord.User, error) {
	c.calls.Add(1)
	time.Sleep(20 * time.Millisecond)
	if c.fail.Load() {
		return discord.User{}, errors.New("rate limited")
	}
	return discord.User{ID: id, Username: "raider"}, nil
}

// A burst of entries by one actor costs one Discord lookup; failures are not cached.
func TestAuditActorLookupsAreShared(t *testing.T) {
	var c actorCache
	client := &countingUsers{}
	client.fail.Store(true)
	if _, err := c.lookup(t.Context(), client, "1", time.Second); err == nil {
		t.Fatal("lookup error lost")
	}
	client.fail.Store(false)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if u, err := c.lookup(t.Context(), client, "1", time.Second); err != nil || u.Username != "raider" {
				t.Errorf("lookup: %+v %v", u, err)
			}
		})
	}
	wg.Wait()
	if _, err := c.lookup(t.Context(), client, "1", time.Second); err != nil {
		t.Fatal(err)
	}
	if got := client.calls.Load(); got != 2 {
		t.Fatalf("%d Discord lookups, want 2 (one failed, one shared)", got)
	}
}
