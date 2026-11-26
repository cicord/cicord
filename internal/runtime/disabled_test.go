package runtime

import (
	"errors"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDisabledInstallationResumesExistingWork(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
		bot := filepath.Join(dir, "bots/moderator/bot.yaml")
		replaceIn(t, manifest, "  capabilities:", "  delivery: at-least-once\n  capabilities:")
		replaceIn(t, manifest, "    - portal.links", "    - scheduler\n    - portal.routes\n    - portal.links")
		replaceIn(t, bot, "        - discord.dm.send", "        - scheduler\n        - portal.routes\n        - discord.dm.send")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  async onStart(ctx){await ctx.kv.increment("starts")},
  async onStop(ctx){await ctx.kv.increment("stops")},
  tasks:{async queued(ctx,input,task){await ctx.kv.set("attempt",task.attempt)}},
  routes:{"GET /probe":{access:"public",handler(){return {ok:true}}}},
  events: {`)
	}, -1)
	ctx := t.Context()
	scope := "plugin:moderator/welcome/my-community"
	kv := e.rt.Store().KV(scope)
	q := e.rt.Store().Tasks()
	cmd := func() bool {
		return slices.ContainsFunc(e.world.Commands(e.appID, e.serverID), func(c discord.Command) bool { return c.Name == "welcome-preview" })
	}
	stateIs := func(health string) bool {
		for _, i := range e.rt.Instances("my-community") {
			if i.Config.Name == "welcome" {
				return i.Health == health && ((i.Definition == nil) == (health == HealthDisabled))
			}
		}
		return false
	}
	if !cmd() || !stateIs(HealthHealthy) {
		t.Fatal("active plugin missing")
	}
	due := time.Now().Add(48 * time.Hour)
	if err := q.Schedule(ctx, state.Task{ID: "before-disable", Name: "queued", Scope: scope, RunAt: due}); err != nil {
		t.Fatal(err)
	}
	bot := filepath.Join(e.dir, "bots/moderator/bot.yaml")
	replaceIn(t, bot, "        channel: general", "        enabled: false\n        channel: general")
	e.rt.Trigger()
	e.waitFor("installation disabled", func() bool { return stateIs(HealthDisabled) })
	rev, _ := e.rt.Revision()
	if err := e.rt.Activate(ctx, rev, true); err != nil {
		t.Fatal(err)
	}
	if cmd() {
		t.Fatal("disabled command still registered")
	}
	if _, ok := e.rt.MatchRoute("my-community", "moderator", "welcome", "GET", "/probe"); ok {
		t.Fatal("disabled route reachable")
	}
	if _, err := kv.Get(ctx, "stops"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("disable ran a shutdown hook", err)
	}
	now := due.Add(72 * time.Hour)
	for range 6 {
		e.rt.sched.runTasks(ctx, now)
		e.waitFor("scheduler idle", func() bool { return len(e.rt.sched.slots) == 0 })
		now = now.Add(time.Minute)
	}
	if _, err := kv.Get(ctx, "attempt"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("disabled task executed", err)
	}
	if dead, err := q.DeadLetters(ctx, "my-community", 50); err != nil || len(dead) > 0 {
		t.Fatal(dead, err)
	}
	replaceIn(t, bot, "        enabled: false", "        enabled: true")
	e.rt.Trigger()
	e.waitFor("installation enabled", func() bool { return stateIs(HealthHealthy) && cmd() })
	e.rt.sched.runTasks(ctx, now)
	e.waitFor("original task delivered", func() bool { v, err := kv.Get(ctx, "attempt"); return err == nil && string(v) == "1" })
	if v, err := kv.Get(ctx, "starts"); err != nil || string(v) != "2" {
		t.Fatal("startup count", string(v), err)
	}
}
