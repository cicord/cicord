package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
)

// Engine benchmarks (numbers in docs/adr/0018-quickjs-wasm-engine.md). They only use the
// engine-neutral API, so the same file runs against other engines.

const benchPlugin = `
import { command, definePlugin } from "@cicord/sdk";
function work(n: number): number {
  const words: string[] = [];
  for (let i = 0; i < n; i++) words.push("w" + ((i * 7919) % 1000));
  const counts = new Map<string, number>();
  for (const w of words) counts.set(w, (counts.get(w) ?? 0) + 1);
  const top = [...counts.entries()].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1)).slice(0, 5);
  return JSON.parse(JSON.stringify({ top })).top.length + words.join(" ").length;
}
export default definePlugin({
  events: {
    async memberJoin(ctx, member) {
      await ctx.kv.increment("joins");
      await ctx.kv.set("last", { id: member.id, name: member.displayName });
    },
  },
  commands: {
    cpu: command({ description: "x", async run(_ctx, it) { await it.reply(String(work(20000))); } }),
  },
});`

func benchBundle(b *testing.B) (string, *Definition) {
	b.Helper()
	dir := b.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.ts"), []byte(benchPlugin), 0o644); err != nil {
		b.Fatal(err)
	}
	res, err := build.Bundle(dir, "src/index.ts")
	if err != nil {
		b.Fatal(err)
	}
	def, err := Inspect(res.Code)
	if err != nil {
		b.Fatal(err)
	}
	return res.Code, def
}

func benchEnv() (Env, *fakeDiscord) {
	dc := newFakeDiscord()
	env := testEnv(dc)
	env.KV = newMemKV()
	return env, dc
}

func benchInstance(b *testing.B, mod func(*Env)) *Instance {
	b.Helper()
	code, def := benchBundle(b)
	env, _ := benchEnv()
	if mod != nil {
		mod(&env)
	}
	inst, err := NewInstance(code, def, env)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = inst.Stop(context.Background()) })
	if err := inst.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	return inst
}

func BenchmarkInstanceStart(b *testing.B) {
	code, def := benchBundle(b)
	env, _ := benchEnv()
	b.ReportAllocs()
	for b.Loop() {
		inst, err := NewInstance(code, def, env)
		if err != nil {
			b.Fatal(err)
		}
		if err := inst.Start(context.Background()); err != nil {
			b.Fatal(err)
		}
		_ = inst.Stop(context.Background())
	}
}

func BenchmarkHandleEvent(b *testing.B) {
	inst := benchInstance(b, nil)
	ev := memberJoin("42")
	b.ReportAllocs()
	for b.Loop() {
		if err := inst.HandleEvent(context.Background(), ev); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCPUBoundCommand(b *testing.B) {
	inst := benchInstance(b, func(e *Env) { e.Limits.SyncBudget = 10e9 })
	it := discord.Interaction{ID: "it", Token: "tok", ApplicationID: "app", Type: discord.InteractionCommand,
		ServerID: "10", ChannelID: "100", User: discord.User{ID: "42", Username: "alice"}, CommandName: "cpu"}
	b.ReportAllocs()
	for b.Loop() {
		if err := inst.HandleInteraction(context.Background(), it, "command", "cpu"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIdleInstanceMemory reports the memory held by one started, idle instance: the Go
// heap and the VM's wasm memory (outside the Go heap; its size bounds what was touched).
func BenchmarkIdleInstanceMemory(b *testing.B) {
	code, def := benchBundle(b)
	env, _ := benchEnv()
	const n = 20
	var heap, linear float64
	for b.Loop() {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		insts := make([]*Instance, n)
		for k := range insts {
			inst, err := NewInstance(code, def, env)
			if err != nil {
				b.Fatal(err)
			}
			if err := inst.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			insts[k] = inst
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		heap += (float64(after.HeapInuse) - float64(before.HeapInuse)) / n
		for _, inst := range insts {
			linear += float64(inst.vm.q.LinearMemorySize()) / n
			_ = inst.Stop(context.Background())
		}
	}
	b.ReportMetric(heap/float64(b.N)/1024, "heap-KiB/instance")
	b.ReportMetric(linear/float64(b.N)/1024, "wasm-KiB/instance")
}
