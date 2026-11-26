// Command gen rebuilds the plugin compiler with the project's Go toolchain.
package main

import (
	"compress/gzip"
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if err := generate(); err != nil {
		log.Fatal(err)
	}
}
func generate() error {
	tmp, err := os.MkdirTemp("", "cicord-compiler-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "compiler.wasm")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", bin, "./worker") //nolint:gosec // G204: executable and build arguments are fixed; bin is our temporary output.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=wasip1", "GOARCH=wasm")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return err
	}
	in, err := os.Open(bin)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create("compiler.wasm.gz")
	if err != nil {
		return err
	}
	defer out.Close()
	w, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err = io.Copy(w, in); err != nil {
		return err
	}
	return w.Close()
}
