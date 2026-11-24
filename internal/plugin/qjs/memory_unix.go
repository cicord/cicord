//go:build unix

package qjs

import "syscall"

// newLinearMemory reserves max bytes of anonymous memory outside the Go heap: pages are
// committed by the OS on first touch, and the base address never moves, so Kill can store
// into it from another goroutine (see VM.Kill).
func newLinearMemory(max uint64) (*linearMemory, error) {
	if max == 0 {
		max = pageSize
	}
	b, err := syscall.Mmap(-1, 0, int(max), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	return &linearMemory{buf: b, free: func() { _ = syscall.Munmap(b) }}, nil
}
