//go:build unix && !linux

package metrics

// registerPlatformProcess adds CPU time (resident memory and file descriptors are only
// reported on Linux).
func registerPlatformProcess(r *Registry) { registerCPU(r) }
