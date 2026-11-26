package plugin

import (
	"errors"
	"sync"
	"sync/atomic"
)

// PrivacyFence belongs to one server. An outer handler holds a read lease through
// its asynchronous operations. A nested action inherits its caller's lease.
type PrivacyFence struct {
	sync.RWMutex
	generation atomic.Uint64
}

// LockErase advances the generation before a member's records are rewritten.
func (f *PrivacyFence) LockErase()   { f.Lock(); f.generation.Add(1) }
func (f *PrivacyFence) UnlockErase() { f.generation.Add(1); f.Unlock() }

// invocationWork admits host work before settlement. A detached timer may still
// perform a new operation until its normal deadline, provided no erasure has
// intervened. It cannot acquire recursively on the single-threaded VM loop.
type invocationWork struct {
	mu         sync.Mutex
	finished   bool
	pending    sync.WaitGroup
	generation uint64
}

var errPrivacyChanged = errors.New("member data changed during this invocation; retry with fresh state")

func (i *Instance) beginPrivacyWork(inv *invocation) (func(), error) {
	f := i.env.PrivacyFence
	if f == nil {
		return nil, nil
	}
	w := &inv.work
	w.mu.Lock()
	if !w.finished && inv != i.bg {
		w.pending.Add(1)
		w.mu.Unlock()
		return w.pending.Done, nil
	}
	w.mu.Unlock()
	if inv.kind == "privacy" || inv.kind == "query" || inv.kind == "action" || !f.TryRLock() {
		return nil, errPrivacyChanged
	}
	if f.generation.Load() != w.generation {
		f.RUnlock()
		return nil, errPrivacyChanged
	}
	return f.RUnlock, nil
}

func (w *invocationWork) drain() {
	w.mu.Lock()
	w.finished = true
	w.mu.Unlock()
	w.pending.Wait()
}
