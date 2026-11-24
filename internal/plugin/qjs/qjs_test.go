package qjs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	_ "embed"
	"errors"
	"strings"
	"testing"
	"time"
)

//go:embed quickjs.wasm.sha256
var wasmSum string

// TestWasmChecksum pins quickjs.wasm to the output of scripts/build-quickjs-wasm.sh.
func TestWasmChecksum(t *testing.T) {
	sum := sha256.Sum256(wasmBytes)
	want := strings.Fields(wasmSum)[0]
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("quickjs.wasm sha256 = %s, quickjs.wasm.sha256 says %s: rebuild with scripts/build-quickjs-wasm.sh", got, want)
	}
}

const testPrelude = `(function (host) {
  return {
    errInfo(e) { return JSON.stringify({ name: String(e && e.name), message: String(e && e.message), stack: String(e && e.stack) }); },
    echo(a, b) { return a + "|" + b; },
    host(op, arg) { const r = host.raw(1, op, arg, 42); if (r && typeof r === "object") throw new Error(r.e); return r; },
    parse(s) { return JSON.stringify(host.parseJSON(s)); },
    run(src) { return String((0, eval)(src)); },
    later() { Promise.resolve().then(() => { globalThis.__later = "done"; }); },
    get(name) { return String(globalThis[name]); },
  };
})`

func newTestVM(t *testing.T, cfg Config, host HostFunc) *VM {
	t.Helper()
	if host == nil {
		host = func(int32, string, string, float64) Reply { return Reply{Undefined: true} }
	}
	v, err := New(context.Background(), cfg, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	if err := v.Prelude(context.Background(), "prelude.js", testPrelude); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCallAndHost(t *testing.T) {
	var seen []string
	v := newTestVM(t, Config{}, func(kind int32, a, b string, n float64) Reply {
		if kind == KindPauseBudget || kind == KindResumeBudget {
			seen = append(seen, "budget")
			return Reply{Undefined: true}
		}
		seen = append(seen, a+":"+b)
		if a == "fail" {
			return Reply{Error: "nope"}
		}
		return Reply{Value: "é" + b}
	})
	ctx := context.Background()
	r, ok, err := v.Call(ctx, "echo", "héllo", "wörld")
	if err != nil || !ok || r != "héllo|wörld" {
		t.Fatalf("echo = %q %v %v", r, ok, err)
	}
	r, _, err = v.Call(ctx, "host", "op", "x")
	if err != nil || r != "éx" {
		t.Fatalf("host = %q %v", r, err)
	}
	_, _, err = v.Call(ctx, "host", "fail", "")
	var ex *Exception
	if !errors.As(err, &ex) || !strings.Contains(ex.Info, "nope") {
		t.Fatalf("err = %v", err)
	}
	r, _, err = v.Call(ctx, "parse", `{"a":[1,2]}`)
	if err != nil || r != `{"a":[1,2]}` {
		t.Fatalf("parse = %q %v", r, err)
	}
	if _, _, err := v.Call(ctx, "later"); err != nil || !v.JobsPending() {
		t.Fatalf("later: %v pending=%v", err, v.JobsPending())
	}
	if err := v.RunJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := v.Call(ctx, "get", "__later"); r != "done" {
		t.Fatalf("job did not run: %q", r)
	}
	if len(seen) != 4 {
		t.Fatalf("seen = %v", seen)
	}
}

func TestInterruptCannotBeCaught(t *testing.T) {
	v := newTestVM(t, Config{}, nil)
	ctx := context.Background()
	time.AfterFunc(50*time.Millisecond, v.Interrupt)
	_, _, err := v.Call(ctx, "run", "let n=0; for(;;){ try { for(;;){} } catch (e) { n++ } }")
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v", err)
	}
	v.ClearInterrupt()
	r, _, err := v.Call(ctx, "run", "1+1")
	if err != nil || r != "2" {
		t.Fatalf("after interrupt: %q %v", r, err)
	}
}

func TestMemoryLimit(t *testing.T) {
	for name, src := range map[string]string{
		"repeat":      `"x".repeat(1e9)`,
		"arraybuffer": `new ArrayBuffer(512*1024*1024)`,
		"array":       `const a=[]; for(;;) a.push({x: a.length})`,
		"string":      `let s="ab"; for(;;) { s = s + s; s.charCodeAt(s.length - 1) }`,
		"caught":      `const k=[]; for(;;){ try { k.push(new Array(1e6).fill(1)) } catch (e) {} }`,
	} {
		t.Run(name, func(t *testing.T) {
			v := newTestVM(t, Config{MemoryLimit: 16 << 20}, nil)
			_, _, err := v.Call(context.Background(), "run", src)
			if !errors.Is(err, ErrOutOfMemory) {
				t.Fatalf("err = %v", err)
			}
			if !errors.Is(v.Dead(), ErrOutOfMemory) {
				t.Fatalf("dead = %v", v.Dead())
			}
		})
	}
}

func TestHardStop(t *testing.T) {
	v := newTestVM(t, Config{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := v.Call(ctx, "run", "for(;;){}")
	if !errors.Is(err, ErrKilled) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := v.Call(context.Background(), "echo", "a", "b"); !errors.Is(err, ErrKilled) {
		t.Fatalf("after kill: %v", err)
	}
}

func TestStackOverflowIsRangeError(t *testing.T) {
	v := newTestVM(t, Config{}, nil)
	r, _, err := v.Call(context.Background(), "run", "function f(n){ return f(n+1)+1 } let m; try { f(0) } catch (e) { m = e.name + ': ' + e.message } m")
	if err != nil || !strings.Contains(r, "RangeError") {
		t.Fatalf("r = %q, err = %v", r, err)
	}
}

// TestKillStopsNativeBuiltins: a loop of heavy native calls is too short to reach
// QuickJS's interrupt check; Kill stops it through the function-entry kill checks.
func TestKillStopsNativeBuiltins(t *testing.T) {
	v := newTestVM(t, Config{}, nil)
	time.AfterFunc(50*time.Millisecond, v.Interrupt)
	time.AfterFunc(100*time.Millisecond, v.Kill)
	start := time.Now()
	_, _, err := v.Call(context.Background(), "run", `const s = "ab".repeat(1e6); for (let i = 0; i < 1e6; i++) s.indexOf("zz")`)
	if !errors.Is(err, ErrKilled) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("kill took %s", d)
	}
}
