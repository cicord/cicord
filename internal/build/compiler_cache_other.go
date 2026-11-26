//go:build !linux && !darwin

package build

import (
	"github.com/tetratelabs/wazero"
)

// Use the process-local engine where a private native-code directory cannot be verified.
func compilerDiskCache() (wazero.CompilationCache, func(bool)) { return nil, func(bool) {} }
