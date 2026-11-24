package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestOnHandlerReportsHandlerCalls(t *testing.T) {
	var mu sync.Mutex
	var got []HandlerResult
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  async onStart() {},
  events: {
    memberJoin(ctx, m) { if (m.user.id === "bad") throw new Error("boom"); },
  },
});`, func(env *Env) {
		env.OnHandler = func(r HandlerResult) {
			mu.Lock()
			got = append(got, r)
			mu.Unlock()
		}
	})
	if err := h.inst.HandleEvent(context.Background(), memberJoin("1")); err != nil {
		t.Fatal(err)
	}
	if err := h.inst.HandleEvent(context.Background(), memberJoin("bad")); err == nil {
		t.Fatal("expected the handler error")
	}
	// Events without a handler and calls after Stop run no plugin code: not reported.
	if err := h.inst.HandleEvent(context.Background(), discord.MemberLeaveEvent{ServerID: "10", User: discord.User{ID: "1"}}); err != nil {
		t.Fatal(err)
	}
	_ = h.inst.Stop(context.Background())
	if err := h.inst.HandleEvent(context.Background(), memberJoin("1")); !errors.Is(err, ErrStopped) {
		t.Fatalf("after stop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 (start, event ok, event error): %+v", len(got), got)
	}
	for i, want := range []struct {
		kind string
		err  bool
	}{{"start", false}, {"event", false}, {"event", true}} {
		r := got[i]
		if r.Kind != want.kind || (r.Err != nil) != want.err || !r.Queued || r.Duration <= 0 {
			t.Errorf("result %d = %+v, want kind %s, error %v, queued", i, r, want.kind, want.err)
		}
	}
}
