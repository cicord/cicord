package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/postgres"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// CORE-05 restart proofs. Unlike scheduler_delivery_test.go, which simulates a
// lost worker with a second ClaimDue, these tests stop the whole runtime in the
// middle of a delivery, close the database, reopen it (a SQLite file, then
// PostgreSQL) and deliver the task from a fresh runtime process.

// restartDatabase opens a new handle on the same durable database.
type restartDatabase struct {
	name string
	open func(t *testing.T) state.Store
}

func restartDatabases(t *testing.T) []restartDatabase {
	path := filepath.Join(t.TempDir(), "state.db")
	dbs := []restartDatabase{{"sqlite-file", func(t *testing.T) state.Store {
		s, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}}}
	base := os.Getenv("CICORD_TEST_POSTGRES_URL")
	if base == "" {
		t.Log("CICORD_TEST_POSTGRES_URL is not set: PostgreSQL restart variant skipped")
		return dbs
	}
	conn := postgresTestSchema(t, base)
	return append(dbs, restartDatabase{"postgres", func(t *testing.T) state.Store {
		s, err := postgres.Open(t.Context(), conn)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}})
}

// postgresTestSchema creates a schema dropped after the test and returns a
// connection string whose search_path selects it.
func postgresTestSchema(t *testing.T, base string) string {
	t.Helper()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := "cicord_restart_" + hex.EncodeToString(b)
	admin := func(sql string) {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(c, base)
		if err != nil {
			t.Fatalf("connect to CICORD_TEST_POSTGRES_URL: %v", err)
		}
		defer conn.Close(c)
		if _, err := conn.Exec(c, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	ident := pgx.Identifier{name}.Sanitize()
	admin("CREATE SCHEMA " + ident)
	t.Cleanup(func() { admin("DROP SCHEMA " + ident + " CASCADE") })
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal("CICORD_TEST_POSTGRES_URL must be a postgres:// URL")
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}

// restartProject is a simulated Discord server and a project directory shared
// by successive runtime processes.
type restartProject struct {
	world    *sim.World
	dir      string
	privacy  string
	serverID string
	appID    string
	bot      discord.User
	tokens   map[string]string
}

func newRestartProject(t *testing.T, edit func(dir string)) *restartProject {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("My Community", owner.ID)
	token, bot, appID := w.CreateBot("Warden")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyTree(t, "../../examples/basic", dir)
	replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), `discordId: "112233445566778899"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `applicationId: "998877665544332211"`, `applicationId: "`+appID+`"`)
	edit(dir)
	if err := build.PrepareCompiler(); err != nil {
		t.Fatal(err)
	}
	return &restartProject{world: w, dir: dir, privacy: t.TempDir(), serverID: srv.ID, appID: appID, bot: bot,
		tokens: map[string]string{"DISCORD_TOKEN_MODERATOR": token}}
}

// boot starts one runtime process on store. stop ends the process (in-flight
// handlers are abandoned with their leases, as on a crash or SIGTERM) and closes
// the store handle.
func (p *restartProject) boot(t *testing.T, store state.Store) (e *env, stop func()) {
	t.Helper()
	src, err := source.NewLocal(p.dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	var out io.Writer = logs
	if testing.Verbose() {
		out = io.MultiWriter(logs, os.Stderr)
	}
	rt := New(Options{
		PrivacyDir: p.privacy,
		Source:     src, Connector: p.world, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { v, ok := p.tokens[n]; return v, ok },
		PortalURL: "http://localhost:7420", Logger: slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ResyncInterval: -1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		<-done
		src.Close()
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}
	t.Cleanup(stop)
	e = &env{t: t, world: p.world, rt: rt, dir: p.dir, serverID: p.serverID, appID: p.appID, bot: p.bot, cancel: cancel, logs: logs}
	e.waitFor("first revision", func() bool { _, ok := rt.Revision(); return ok })
	e.waitFor("plugins running", func() bool {
		for _, b := range rt.Status().Bots {
			for _, pl := range b.Plugins {
				if pl.Health != HealthHealthy {
					return false
				}
			}
		}
		return true
	})
	return e, stop
}

func enableScheduler(t *testing.T, dir string, delivery bool) {
	welcomeWith(t, dir, delivery, "scheduler")
}

const restartScope = "plugin:moderator/welcome/my-community"

func kvString(t *testing.T, s state.Store, key string) string {
	t.Helper()
	v, err := s.KV(restartScope).Get(t.Context(), key)
	if err != nil {
		return ""
	}
	return string(v)
}

// An at-least-once task whose Discord effect succeeded when the process died,
// before the acknowledgement, is redelivered by the next process with the same
// ID and attempt+1. The plugin deduplicates its effect by task ID, so the
// member sees exactly one message.
func TestDurableTaskSurvivesRestartAfterEffect(t *testing.T) {
	for _, db := range restartDatabases(t) {
		t.Run(db.name, func(t *testing.T) {
			p := newRestartProject(t, func(dir string) {
				enableScheduler(t, dir, true)
				replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  tasks: {
       async expire(ctx, payload, task) {
         await ctx.kv.set("attempt:" + task.attempt, task.id);
         if (!(await ctx.kv.get("sent:" + task.id))) {
           await ctx.server.channel(ctx.config.channel).send("expired " + task.id);
           await ctx.kv.set("sent:" + task.id, true);
         }
         // The process stops here on the first delivery: effect done, no ack.
         if (task.attempt === 1) await new Promise(() => {});
         await ctx.kv.set("finalAttempt", task.attempt);
       }
     },
     events: {`)
			})
			e, stop := p.boot(t, db.open(t))
			ctx := t.Context()
			when := time.Now().Add(time.Hour)
			if err := e.rt.Store().Tasks().Schedule(ctx, state.Task{ID: "expiry", Scope: restartScope, Name: "expire", RunAt: when}); err != nil {
				t.Fatal(err)
			}
			e.rt.sched.runTasks(ctx, when)
			general, ok := p.world.ChannelByName(p.serverID, "general")
			if !ok {
				t.Fatal("no general channel")
			}
			e.waitFor("first delivery effect", func() bool {
				return kvString(t, e.rt.Store(), "sent:expiry") == "true" && len(p.world.Messages(general.ID)) == 1
			})
			stop()

			store := db.open(t)
			// Depending on whether the instance or the scheduler stopped first, the
			// task was released or keeps its lease (2 × handler timeout): look past it.
			due, err := store.Tasks().Due(ctx, when.Add(time.Hour), 10)
			if err != nil || len(due) != 1 || due[0].ID != "expiry" {
				t.Fatalf("after the crash the task must still be queued: %+v %v", due, err)
			}
			e, _ = p.boot(t, store)
			// After the first lease (2 × handler timeout) has expired.
			e.rt.sched.runTasks(ctx, when.Add(time.Hour))
			e.waitFor("redelivery", func() bool { return kvString(t, store, "finalAttempt") == "2" })
			e.waitFor("redelivery settled", func() bool { return len(e.rt.sched.slots) == 0 })
			if got := kvString(t, store, "attempt:2"); got != `"expiry"` {
				t.Errorf("second delivery saw task id %s, want the same ID", got)
			}
			if n := len(p.world.Messages(general.ID)); n != 1 {
				t.Errorf("%d messages after redelivery, want exactly one effect", n)
			}
			if due, err := store.Tasks().Due(ctx, when.Add(2*time.Hour), 10); err != nil || len(due) != 0 {
				t.Errorf("task not acknowledged after redelivery: %+v %v", due, err)
			}
			if dead, err := store.Tasks().DeadLetters(ctx, "my-community", 10); err != nil || len(dead) != 0 {
				t.Errorf("dead letters %+v %v", dead, err)
			}
		})
	}
}

// Legacy (at-most-once) tasks across a restart: a task leased by a process that
// died before starting it runs exactly once in the next process; a task whose
// handler had started is never replayed, even though its result was lost.
func TestLegacyTaskRestart(t *testing.T) {
	for _, db := range restartDatabases(t) {
		t.Run(db.name, func(t *testing.T) {
			p := newRestartProject(t, func(dir string) {
				enableScheduler(t, dir, false)
				replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  tasks: {
       async legacy(ctx, payload, task) {
         await ctx.kv.increment("runs:" + task.id);
         if (payload && payload.hang) await new Promise(() => {});
       }
     },
     events: {`)
			})
			e, stop := p.boot(t, db.open(t))
			ctx := t.Context()
			q := e.rt.Store().Tasks()
			when := time.Now().Add(time.Hour)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(q.Schedule(ctx, state.Task{ID: "leased", Scope: restartScope, Name: "legacy", RunAt: when.Add(-time.Second)}))
			must(q.Schedule(ctx, state.Task{ID: "started", Scope: restartScope, Name: "legacy", Payload: []byte(`{"hang":true}`), RunAt: when}))
			// A previous worker leased the first task and died before starting it.
			if old, err := q.ClaimDue(ctx, when, when.Add(time.Minute), "previous", 1, onboardingScope); err != nil || len(old) != 1 || old[0].ID != "leased" {
				t.Fatalf("lease by the lost worker: %+v %v", old, err)
			}
			e.rt.sched.runTasks(ctx, when)
			e.waitFor("started handler", func() bool { return kvString(t, e.rt.Store(), "runs:started") == "1" })
			stop()

			store := db.open(t)
			e, _ = p.boot(t, store)
			e.rt.sched.runTasks(ctx, when.Add(time.Hour))
			e.waitFor("leased task delivered", func() bool { return kvString(t, store, "runs:leased") == "1" })
			e.waitFor("delivery settled", func() bool { return len(e.rt.sched.slots) == 0 })
			e.rt.sched.runTasks(ctx, when.Add(2*time.Hour))
			e.waitFor("second pass settled", func() bool { return len(e.rt.sched.slots) == 0 })
			for key, want := range map[string]string{"runs:started": "1", "runs:leased": "1"} {
				if got := kvString(t, store, key); got != want {
					t.Errorf("%s = %q, want %s", key, got, want)
				}
			}
			if due, err := store.Tasks().Due(ctx, when.Add(3*time.Hour), 10); err != nil || len(due) != 0 {
				t.Errorf("legacy tasks left after restart: %+v %v", due, err)
			}
			if dead, err := store.Tasks().DeadLetters(ctx, "my-community", 10); err != nil || len(dead) != 0 {
				t.Errorf("dead letters %+v %v", dead, err)
			}
		})
	}
}
