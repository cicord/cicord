package plugin

import (
	"context"
	"encoding/json"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- test input generation
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// Review finding B2: 250 concurrent files.read of a 20 MiB file peaked at 9.8 GiB
// of host heap. Results now wait for the installation's byte budget instead: 60
// reads used to peak above 2 GiB.
func TestFilesReadFanOutStaysWithinTheResultBudget(t *testing.T) {
	source := `import {definePlugin,command} from "@cicord/sdk";
 export default definePlugin({commands:{inspect:command({description:"read files",async run(ctx,it){
  const ps=[]; for (let i=0;i<60;i++) ps.push(ctx.files.read("big.txt").then(s=>s.length===20*1024*1024-16?0:1,()=>1));
  const failed=(await Promise.all(ps)).reduce((a,b)=>a+b,0); await it.reply("failed "+failed);
 }})}});`
	data := make([]byte, 20<<20-16)
	for i := range data {
		data[i] = 'a'
	}
	h := start(t, source, func(e *Env) {
		e.Grants = append([]string{"files.read"}, e.Grants...)
		e.Files = map[string][]byte{"big.txt": data}
		e.Limits.HandlerTimeout = time.Minute
		e.Limits.AutoDeferAfter = time.Minute
		// The test measures memory, not CPU: receiving 20 MiB strings is slow under -race.
		e.Limits.SyncBudget = 10 * time.Second
	})
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	err := h.command("inspect", nil, discord.Resolved{})
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != "failed 0" {
		t.Fatalf("reads: %s (%+v)", got, h.responses())
	}
	grew := int64(peak.Load()) - int64(base.HeapAlloc)
	t.Logf("peak heap growth %d MiB", grew>>20)
	if grew > 512<<20 {
		t.Fatalf("peak heap grew by %d MiB", grew>>20)
	}
}

func TestResultBudget(t *testing.T) {
	q := newInstallationQuota()
	a, b, c := &invocation{}, &invocation{}, &invocation{}
	ctx := t.Context()
	if err := q.results.acquire(ctx, a, maxResultBytesInvocation+1); err == nil {
		t.Fatal("accepted a result larger than the per-call limit")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.results.acquire(ctx, a, maxResultBytesInvocation))
	// The invocation is full; another one still has room in the installation.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := q.results.acquire(short, a, 1); err == nil {
		t.Fatal("invocation exceeded its budget")
	}
	must(q.results.acquire(ctx, b, maxResultBytesInstallation-maxResultBytesInvocation))
	waited := make(chan error, 1)
	go func() { waited <- q.results.acquire(ctx, c, 1<<20) }()
	select {
	case <-waited:
		t.Fatal("installation exceeded its budget")
	case <-time.After(20 * time.Millisecond):
	}
	q.results.release(a, maxResultBytesInvocation)
	must(<-waited)
	if _, ok := q.results.perInv[a]; ok {
		t.Fatal("released invocation not forgotten")
	}
	h := &resultHold{budget: &q.results, inv: a}
	must(h.grow(ctx, 10))
	must(h.fit(ctx, 4))
	must(h.fit(ctx, 30))
	if h.held != 30 || q.results.perInv[a] != 30 {
		t.Fatalf("hold %d, accounted %d", h.held, q.results.perInv[a])
	}
	h.release()
	if q.results.perInv[a] != 0 {
		t.Fatal("hold not released")
	}
}

func TestJSONStringLenMatchesEncoding(t *testing.T) {
	alphabet := []string{"a", "é", "\"", "\\", "\n", "\t", "\x01", "<", ">", "&", " ", " ", "\b", "\f", "\r", "😀", "\x7f"}
	for range 500 {
		var s []byte
		for range rand.IntN(40) {
			s = append(s, alphabet[rand.IntN(len(alphabet))]...)
		}
		b, _ := json.Marshal(string(s))
		if got := jsonStringLen(s); got != int64(len(b)) {
			t.Fatalf("%q: %d, encoding/json %d", s, got, len(b))
		}
	}
}
