package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

const hogPlugin = `
import { command, definePlugin } from "@cicord/sdk";
const keep: unknown[] = [];
export default definePlugin({
  commands: {
    repeat: command({ description: "x", run() { keep.push("x".repeat(1e9)); } }),
    buffer: command({ description: "x", run() { keep.push(new ArrayBuffer(512 * 1024 * 1024)); } }),
    grow: command({ description: "x", run() { for (;;) keep.push({ n: keep.length, s: "item " + keep.length }); } }),
    caught: command({ description: "x", run() {
      for (;;) { try { keep.push(new Array(1e6).fill(keep.length)); } catch (e) { /* swallowed */ } }
    } }),
    later: command({ description: "x", async run(ctx) {
      await ctx.kv.get("x");
      for (;;) keep.push("y".repeat(1e5) + keep.length);
    } }),
    ok: command({ description: "x", async run(_ctx, it) { await it.reply("alive"); } }),
  },
});`

// TestMemoryLimitCrashesInstance: exhausting Limits.Memory (from native builtins, plain
// allocation in a loop, or after an await, even when the error is caught) crashes the
// instance only. The process and other instances keep running.
func TestMemoryLimitCrashesInstance(t *testing.T) {
	code := bundleSource(t, hogPlugin)
	other := startCode(t, code, nil)
	for _, cmd := range []string{"repeat", "buffer", "grow", "caught", "later"} {
		t.Run(cmd, func(t *testing.T) {
			h := startCode(t, code, func(e *Env) { e.Limits.Memory, e.Limits.SyncBudget = 16<<20, 5*time.Second })
			start := time.Now()
			err := h.command(cmd, nil, discord.Resolved{})
			if !errors.Is(err, ErrCrashed) || !errors.Is(err, ErrOutOfMemory) {
				t.Fatalf("err = %v", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %s", d)
			}
			select {
			case <-h.inst.Done():
			case <-time.After(time.Second):
				t.Fatal("Done not closed")
			}
			if !errors.Is(h.inst.Err(), ErrCrashed) {
				t.Fatalf("Err = %v", h.inst.Err())
			}
			// The user got the generic error, later calls fail fast.
			if r := h.responses(); len(r) != 1 || contentOf(r[0]) != GenericErrorMessage {
				t.Fatalf("responses = %+v", r)
			}
			if err := h.command("ok", nil, discord.Resolved{}); !errors.Is(err, ErrCrashed) {
				t.Fatalf("after crash: %v", err)
			}
			h.logged("plugin instance crashed")
		})
	}
	if err := other.command("ok", nil, discord.Resolved{}); err != nil {
		t.Fatalf("other instance: %v", err)
	}
	if got := contentOf(other.responses()[0]); got != "alive" {
		t.Fatalf("other instance replied %q", got)
	}
}

// TestNativeBuiltinsAreInterrupted: long native builtins that check for interrupts
// (regular expressions, sort) are stopped softly at the budget and the instance keeps
// serving; builtins that never check (a loop of heavy calls too short to reach an
// interrupt check) are stopped by the hard stop, which crashes the instance.
func TestNativeBuiltinsAreInterrupted(t *testing.T) {
	code := bundleSource(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    regex: command({ description: "x", run() { /(a+)+$/.test("a".repeat(40) + "b"); } }),
    sort: command({ description: "x", run() {
      const a = Array.from({ length: 300000 }, (_, i) => String((i * 7919) % 1000003));
      for (;;) a.sort();
    } }),
    join: command({ description: "x", run() {
      const s = "ab".repeat(1e6);
      for (let i = 0; i < 1000; i++) s.indexOf("zz");
    } }),
    ok: command({ description: "x", async run(_ctx, it) { await it.reply("alive"); } }),
  },
});`)
	h := startCode(t, code, func(e *Env) { e.Limits.SyncBudget = 30 * time.Millisecond })
	for _, cmd := range []string{"regex", "sort"} {
		start := time.Now()
		if err := h.command(cmd, nil, discord.Resolved{}); !errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrCrashed) {
			t.Fatalf("%s: %v", cmd, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%s: interrupt took %s", cmd, d)
		}
	}
	if err := h.command("ok", nil, discord.Resolved{}); err != nil {
		t.Fatalf("after soft interrupts: %v", err)
	}

	start := time.Now()
	err := h.command("join", nil, discord.Resolved{})
	if !errors.Is(err, ErrCrashed) || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("join: %v", err)
	}
	if d := time.Since(start); d > 30*time.Millisecond+hardStopGrace(30*time.Millisecond)+time.Second {
		t.Fatalf("hard stop took %s", d)
	}
	if !errors.Is(h.inst.Err(), ErrCrashed) {
		t.Fatalf("Err = %v", h.inst.Err())
	}
}

// TestAbortedCodeThatKeepsRunningCrashes: microtasks that keep spinning after their
// handler was aborted for exceeding the budget crash the instance instead of starving the
// event loop.
func TestAbortedCodeThatKeepsRunningCrashes(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    spin: command({ description: "x", run() {
      const again = (): void => { queueMicrotask(again); for (let i = 0; i < 1e5; i++); };
      again();
    } }),
  },
});`, func(e *Env) { e.Limits.SyncBudget = 30 * time.Millisecond })
	if err := h.command("spin", nil, discord.Resolved{}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("spin: %v", err)
	}
	select {
	case <-h.inst.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("instance still running")
	}
	if !errors.Is(h.inst.Err(), ErrCrashed) {
		t.Fatalf("Err = %v", h.inst.Err())
	}
}

func TestQueueLengthBound(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    memberJoin() { const t = Date.now(); while (Date.now() - t < 100) {} },
  },
});`, func(e *Env) { e.Limits.QueueLength = 2; e.Limits.SyncBudget = time.Second })
	var chans []<-chan error
	for n := range 8 {
		chans = append(chans, h.inst.DispatchEvent(context.Background(), memberJoin(fmt.Sprint(n))))
	}
	busy, ok := 0, 0
	for _, c := range chans {
		switch err := <-c; {
		case err == nil:
			ok++
		case errors.Is(err, ErrBusy):
			busy++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if busy == 0 || ok < 2 {
		t.Fatalf("ok=%d busy=%d", ok, busy)
	}
}

func TestHostQuotas(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    args: command({ description: "x", async run(ctx, it) {
      try { await ctx.kv.set("k", "x".repeat(3 * 1024 * 1024)); await it.reply("no error"); }
      catch (e) { await it.reply((e as Error).name + ": " + (e as Error).message); }
    } }),
    inflight: command({ description: "x", async run(ctx, it) {
      const ps: Promise<unknown>[] = [];
      for (let i = 0; i < 300; i++) ps.push(ctx.kv.get("k" + i));
      const failed = (await Promise.allSettled(ps)).filter((r) => r.status === "rejected") as PromiseRejectedResult[];
      await it.reply(failed.length + " " + (failed[0]?.reason as Error | undefined)?.message);
    } }),
    fields: command({ description: "x", async run(ctx, it) {
      ctx.log.info("big fields", { blob: "z".repeat(20000) });
      await it.reply("logged");
    } }),
    list: command({ description: "x", async run(ctx, it) {
      for (let i = 0; i < 8; i++) await ctx.kv.set("big:" + i, "v".repeat(900 * 1024));
      const page = await ctx.kv.list({ prefix: "big:", limit: 100 });
      await it.reply(page.items.length + " " + page.cursor);
    } }),
  },
  routes: {
    "GET /big": { access: "public", handler() { return { blob: "r".repeat(5 * 1024 * 1024) }; } },
  },
});`, nil)
	reply := func(cmd string) string {
		t.Helper()
		before := len(h.responses())
		if err := h.command(cmd, nil, discord.Resolved{}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		r := h.responses()
		if len(r) <= before {
			t.Fatalf("%s: no reply", cmd)
		}
		return contentOf(r[len(r)-1])
	}
	if got := reply("args"); !strings.Contains(got, "TypeError") || !strings.Contains(got, "arguments larger than") {
		t.Errorf("args: %q", got)
	}
	if got := reply("inflight"); !strings.HasPrefix(got, "44 ") || !strings.Contains(got, "too many pending host calls") {
		t.Errorf("inflight: %q", got)
	}
	if got := reply("fields"); got != "logged" {
		t.Errorf("fields: %q", got)
	}
	if e := h.logged("big fields"); !strings.Contains(fmt.Sprint(e.Attrs), "dropped") {
		t.Errorf("fields attrs: %v", e.Attrs)
	}
	if got := reply("list"); got != "4 big:3" {
		t.Errorf("list: %q", got)
	}
	if _, err := h.inst.HandleRoute(context.Background(), "GET", "/big", RouteRequest{}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("route: %v", err)
	}
}

// TestManyInstancesRunConcurrently is a smoke test of the per-instance VMs under the race
// detector: instances share the compiled engine, not state.
func TestManyInstancesRunConcurrently(t *testing.T) {
	code := bundleSource(t, `
import { command, definePlugin } from "@cicord/sdk";
let n = 0;
export default definePlugin({
  commands: {
    count: command({ description: "x", async run(ctx, it) {
      n++;
      await ctx.kv.set("n", n);
      await it.reply(String(n));
    } }),
  },
});`)
	var wg sync.WaitGroup
	for range 8 {
		h := startCode(t, code, nil)
		wg.Go(func() {
			for range 10 {
				if err := h.command("count", nil, discord.Resolved{}); err != nil {
					t.Error(err)
					return
				}
			}
			v, _ := h.kv.Get(context.Background(), "n")
			var got int
			_ = json.Unmarshal(v, &got)
			if got != 10 {
				t.Errorf("n = %d", got)
			}
		})
	}
	wg.Wait()
}
