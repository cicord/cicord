package plugin

import (
	"context"
	"sync"
)

// Admission budgets for private VMs (interop calls and isolated member requests).
// Each server has its own share in front of the process-wide cap, so one server's
// slow provider or burst of member commands cannot queue or refuse work on every
// other server. Top-level and nested peer calls use separate pools: callers
// holding a slot while waiting for their nested provider cannot exhaust the slots
// that provider needs.
const (
	interopGlobalSlots = 32
	interopServerSlots = 8
	memberGlobalSlots  = 64
	memberServerSlots  = 16
)

type slotPool struct {
	global  chan struct{}
	perKey  int
	servers sync.Map // server ID → chan struct{}
}

func newSlotPool(global, perKey int) *slotPool {
	return &slotPool{global: make(chan struct{}, global), perKey: perKey}
}

func (p *slotPool) server(id string) chan struct{} {
	if v, ok := p.servers.Load(id); ok {
		return v.(chan struct{})
	}
	v, _ := p.servers.LoadOrStore(id, make(chan struct{}, p.perKey))
	return v.(chan struct{})
}

// acquire waits for the server's share, then for the global cap.
func (p *slotPool) acquire(ctx context.Context, server string) (func(), error) {
	own := p.server(server)
	select {
	case own <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case p.global <- struct{}{}:
		return func() { <-p.global; <-own }, nil
	case <-ctx.Done():
		<-own
		return nil, ctx.Err()
	}
}

// tryAcquire takes a slot without waiting.
func (p *slotPool) tryAcquire(server string) (func(), bool) {
	own := p.server(server)
	select {
	case own <- struct{}{}:
	default:
		return nil, false
	}
	select {
	case p.global <- struct{}{}:
		return func() { <-p.global; <-own }, true
	default:
		<-own
		return nil, false
	}
}

var (
	interopSlots       = newSlotPool(interopGlobalSlots, interopServerSlots)
	nestedInteropSlots = newSlotPool(interopGlobalSlots, interopServerSlots)
	memberSlots        = newSlotPool(memberGlobalSlots, memberServerSlots)
)

// interopPool selects the pool of a call: Path holds the caller chain plus the
// provider, so a top-level call has two entries and a nested one three.
func interopPool(frame CallFrame) *slotPool {
	if len(frame.Path) > 2 {
		return nestedInteropSlots
	}
	return interopSlots
}
