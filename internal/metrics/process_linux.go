package metrics

import (
	"bytes"
	"os"
	"strconv"
)

// registerPlatformProcess adds CPU time, resident memory (/proc/self/statm) and open file
// descriptors (/proc/self/fd).
func registerPlatformProcess(r *Registry) {
	registerCPU(r)
	pageSize := float64(os.Getpagesize())
	r.NewFunc(Gauge, "process_resident_memory_bytes", "Resident memory size, in bytes.", nil, func(emit Emit) {
		data, err := os.ReadFile("/proc/self/statm")
		if err != nil {
			return
		}
		fields := bytes.Fields(data)
		if len(fields) < 2 {
			return
		}
		if pages, err := strconv.ParseUint(string(fields[1]), 10, 64); err == nil {
			emit(float64(pages) * pageSize)
		}
	})
	r.NewFunc(Gauge, "process_open_fds", "Number of open file descriptors.", nil, func(emit Emit) {
		if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
			emit(float64(len(entries)))
		}
	})
}
