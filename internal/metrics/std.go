package metrics

import (
	"runtime"
	rtmetrics "runtime/metrics"
	"time"
)

// RegisterBuildInfo registers cicord_build_info (constant 1, the build in its labels).
func RegisterBuildInfo(r *Registry, version, commit string) {
	goVersion := runtime.Version()
	r.NewFunc(Gauge, "cicord_build_info", "Build of the running cicord binary (always 1).",
		[]string{"version", "commit", "goversion"}, func(emit Emit) { emit(1, version, commit, goVersion) })
}

// goSamples are the runtime/metrics read at every scrape, by exposed name. They are cheaper
// than runtime.ReadMemStats (no stop-the-world).
var goSamples = []struct {
	name, help, key string
	typ             Type
}{
	{"go_goroutines", "Number of goroutines that currently exist.", "/sched/goroutines:goroutines", Gauge},
	{"go_gomaxprocs", "GOMAXPROCS: the number of OS threads that can execute Go code simultaneously.", "/sched/gomaxprocs:threads", Gauge},
	{"go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", "/memory/classes/heap/objects:bytes", Gauge},
	{"go_memstats_sys_bytes", "Bytes of memory obtained from the OS by the Go runtime.", "/memory/classes/total:bytes", Gauge},
	{"go_gc_cycles_total", "Completed GC cycles.", "/gc/cycles/total:gc-cycles", Counter},
}

// RegisterGo registers Go runtime metrics: go_info, goroutines, GOMAXPROCS, heap and
// memory obtained from the OS, GC cycles.
func RegisterGo(r *Registry) {
	goVersion := runtime.Version()
	r.NewFunc(Gauge, "go_info", "Information about the Go environment (always 1).", []string{"version"},
		func(emit Emit) { emit(1, goVersion) })
	for _, s := range goSamples {
		key := s.key
		r.NewFunc(s.typ, s.name, s.help, nil, func(emit Emit) {
			m := []rtmetrics.Sample{{Name: key}}
			rtmetrics.Read(m)
			switch m[0].Value.Kind() {
			case rtmetrics.KindUint64:
				emit(float64(m[0].Value.Uint64()))
			case rtmetrics.KindFloat64:
				emit(m[0].Value.Float64())
			}
		})
	}
}

// RegisterProcess registers process metrics: start time everywhere, CPU time on Unix,
// resident memory and open file descriptors on Linux.
func RegisterProcess(r *Registry) {
	start := float64(time.Now().UnixNano()) / 1e9
	r.NewFunc(Gauge, "process_start_time_seconds", "Start time of the process since the Unix epoch, in seconds.", nil,
		func(emit Emit) { emit(start) })
	registerPlatformProcess(r)
}
