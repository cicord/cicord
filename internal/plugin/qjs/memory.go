package qjs

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tetratelabs/wazero/experimental"
)

// linearMemory backs a VM's wasm memory with a buffer of the maximum size whose address
// never changes (wazero experimental.LinearMemory).
type linearMemory struct {
	buf  []byte
	free func()

	mu    sync.RWMutex // guards freed against stores into buf (store32)
	freed bool
}

var _ experimental.LinearMemory = (*linearMemory)(nil)

// Reallocate implements experimental.LinearMemory: the buffer is already at its maximum.
func (m *linearMemory) Reallocate(size uint64) []byte {
	if size > uint64(len(m.buf)) {
		return nil
	}
	return m.buf[:size]
}

// Free implements experimental.LinearMemory.
func (m *linearMemory) Free() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.freed {
		m.freed = true
		m.free()
	}
}

// store32 atomically stores v at addr (from any goroutine, while the guest runs).
func (m *linearMemory) store32(addr uint32, v int32) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.freed || uint64(addr)+4 > uint64(len(m.buf)) || addr%4 != 0 {
		return
	}
	// The guest reads this word with plain loads while it runs: an atomic store into the
	// fixed mapping is the cross-thread write wazero documents for shared memories.
	// nosemgrep: go.lang.security.audit.unsafe.use-of-unsafe-block -- bounds and alignment checked above; the mapping never moves and is not freed while mu is held
	atomic.StoreInt32((*int32)(unsafe.Pointer(&m.buf[addr])), v) //nolint:gosec // bounds and alignment checked above, buf is never moved or freed while held
}
