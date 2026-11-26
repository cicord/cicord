package plugin

import (
	"context"
	"sync"
	"testing"
	"time"
)

// SEC-P-04: one server saturating its interop and member shares leaves the other
// servers' admission untouched.
func TestAdmissionIsPerServer(t *testing.T) {
	p := newSlotPool(interopGlobalSlots, interopServerSlots)
	var releases []func()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range interopServerSlots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := p.acquire(t.Context(), "server-a")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			releases = append(releases, release)
			mu.Unlock()
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.acquire(ctx, "server-a"); err == nil {
		t.Fatal("server A exceeded its share")
	}
	release, err := p.acquire(t.Context(), "server-b")
	if err != nil {
		t.Fatal("server B starved by server A")
	}
	release()
	for _, r := range releases {
		r()
	}
	if release, err := p.acquire(t.Context(), "server-a"); err != nil {
		t.Fatal("slots not released")
	} else {
		release()
	}

	m := newSlotPool(memberGlobalSlots, memberServerSlots)
	for range memberServerSlots {
		if _, ok := m.tryAcquire("server-a"); !ok {
			t.Fatal("member share too small")
		}
	}
	if _, ok := m.tryAcquire("server-a"); ok {
		t.Fatal("member share exceeded")
	}
	if release, ok := m.tryAcquire("server-b"); !ok {
		t.Fatal("server B refused")
	} else {
		release()
	}
}

// Nested peer calls have their own pool, so outer calls holding every top-level
// slot of a server do not block the providers they wait for.
func TestNestedInteropUsesSeparatePool(t *testing.T) {
	if interopPool(CallFrame{Path: []string{"a", "b"}}) == interopPool(CallFrame{Path: []string{"a", "b", "c"}}) {
		t.Fatal("nested calls share the top-level pool")
	}
}
