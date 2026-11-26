package plugin

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Host memory held for one installation, outside its VM's own 64 MiB limit. The
// live instance and its private VMs (member isolation, interop) share one quota:
// privateVM copies Env, and with it this pointer.
type installationQuota struct {
	results resultBudget
	uploads atomic.Int64 // bytes reserved by staged uploads
}

func newInstallationQuota() *installationQuota {
	return &installationQuota{results: resultBudget{perInv: map[*invocation]int64{}, wake: make(chan struct{})}}
}

// Bytes of async host-call results not yet delivered to the VM: reserved before
// an op materializes a large result, resized to the encoded size after
// json.Marshal, released once the settlement ran. Without it, 256 calls in flight
// of a 20 MiB files.read held about 10 GiB of host heap (review finding B2).
const (
	maxResultBytesInstallation = 96 << 20
	maxResultBytesInvocation   = 48 << 20
)

type resultBudget struct {
	mu     sync.Mutex
	used   int64
	perInv map[*invocation]int64
	wake   chan struct{} // closed and replaced on every release
}

func (b *resultBudget) acquire(ctx context.Context, inv *invocation, n int64) error {
	if n <= 0 {
		return nil
	}
	if n > maxResultBytesInvocation {
		return fmt.Errorf("host result of %d bytes exceeds the %d MiB limit per call", n, maxResultBytesInvocation>>20)
	}
	for {
		b.mu.Lock()
		if b.used+n <= maxResultBytesInstallation && b.perInv[inv]+n <= maxResultBytesInvocation {
			b.used += n
			b.perInv[inv] += n
			b.mu.Unlock()
			return nil
		}
		wake := b.wake
		b.mu.Unlock()
		// Earlier results settle on the event loop, which never waits here,
		// so the bytes they hold are always released.
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *resultBudget) release(inv *invocation, n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= n
	if b.perInv[inv] -= n; b.perInv[inv] <= 0 {
		delete(b.perInv, inv)
	}
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()
}

// resultHold is the reservation of one async host call.
type resultHold struct {
	budget *resultBudget
	inv    *invocation
	mu     sync.Mutex
	held   int64
}

type resultHoldKey struct{}

func (i *Instance) resultHold(inv *invocation) *resultHold {
	if i.env.quota == nil { // an Instance not built by NewInstance (tests)
		return &resultHold{budget: &resultBudget{perInv: map[*invocation]int64{}, wake: make(chan struct{})}, inv: inv}
	}
	return &resultHold{budget: &i.env.quota.results, inv: inv}
}

// withResultHold lets the op reserve its result before allocating it.
func withResultHold(ctx context.Context, h *resultHold) context.Context {
	return context.WithValue(ctx, resultHoldKey{}, h)
}

// reserveResult reserves n bytes for the result of the current host call. Ops
// whose results can be large call it before reading or copying the data.
func reserveResult(ctx context.Context, n int64) error {
	h, _ := ctx.Value(resultHoldKey{}).(*resultHold)
	if h == nil {
		return nil
	}
	return h.grow(ctx, n)
}

func (h *resultHold) grow(ctx context.Context, n int64) error {
	if err := h.budget.acquire(ctx, h.inv, n); err != nil {
		return err
	}
	h.mu.Lock()
	h.held += n
	h.mu.Unlock()
	return nil
}

// fit resizes the reservation to the encoded result that waits for the VM.
func (h *resultHold) fit(ctx context.Context, n int) error {
	h.mu.Lock()
	extra := int64(n) - h.held
	h.mu.Unlock()
	if extra > 0 {
		return h.grow(ctx, extra)
	}
	h.mu.Lock()
	h.held += extra
	h.mu.Unlock()
	h.budget.release(h.inv, -extra)
	return nil
}

func (h *resultHold) release() {
	h.mu.Lock()
	n := h.held
	h.held = 0
	h.mu.Unlock()
	h.budget.release(h.inv, n)
}

// jsonStringLen is the length of s once encoding/json has quoted it, including
// the HTML escapes of json.Marshal. It lets files.read reserve its exact result.
func jsonStringLen(s []byte) int64 {
	n := int64(2)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t' || c == '\b' || c == '\f':
				n += 2
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				n += 6
			default:
				n++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			n += 6 // �
		case r == ' ' || r == ' ':
			n += 6
		default:
			n += int64(size)
		}
		i += size
	}
	return n
}
