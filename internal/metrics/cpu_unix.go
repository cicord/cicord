//go:build unix

package metrics

import "syscall"

// registerCPU registers process_cpu_seconds_total (user + system time, getrusage).
func registerCPU(r *Registry) {
	r.NewFunc(Counter, "process_cpu_seconds_total", "Total user and system CPU time spent, in seconds.", nil, func(emit Emit) {
		var ru syscall.Rusage
		if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
			emit(float64(ru.Utime.Nano()+ru.Stime.Nano()) / 1e9)
		}
	})
}
