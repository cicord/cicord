//go:build !unix

package metrics

// registerPlatformProcess adds nothing: CPU time, resident memory and file descriptors are
// only reported on Unix systems.
func registerPlatformProcess(*Registry) {}
