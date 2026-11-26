// Command gen rebuilds the isolated Go card renderer with the project's Go toolchain.
package main

import (
	"context"
	"log"
	"os"
	"os/exec"
)

func main() {
	cmd := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", "renderer.wasm", "./worker")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=wasip1", "GOARCH=wasm")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
	if err := os.Chmod("renderer.wasm", 0o644); err != nil {
		log.Fatal(err)
	}
}
