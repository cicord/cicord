//go:build !unix

package qjs

// newLinearMemory allocates max bytes on the Go heap (the base address never moves).
func newLinearMemory(max uint64) (*linearMemory, error) {
	if max == 0 {
		max = pageSize
	}
	return &linearMemory{buf: make([]byte, max), free: func() {}}, nil
}
