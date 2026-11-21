package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/sim"
)

// run executes the CLI and returns the exit code and outputs.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := ExecuteContext(context.Background(), args, &out, &errb)
	if testing.Verbose() {
		t.Logf("codi %s → %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, out.String(), errb.String())
	}
	return code, out.String(), errb.String()
}

// useSim makes local commands connect to the simulator, with secrets from env.
func useSim(t *testing.T, w *sim.World, secrets map[string]string) {
	t.Helper()
	oldC, oldS := newConnector, lookupSecret
	newConnector = func() discord.Connector { return w }
	lookupSecret = func(n string) (string, bool) {
		v, ok := secrets[n]
		return v, ok
	}
	t.Cleanup(func() { newConnector, lookupSecret = oldC, oldS })
}

// withStdin simulates a terminal answering line.
func withStdin(t *testing.T, tty bool, line string) {
	t.Helper()
	oldR, oldT := stdinReader, stdinIsTTY
	stdinReader = strings.NewReader(line)
	stdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { stdinReader, stdinIsTTY = oldR, oldT })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// tarNames lists the regular files of a .tar.gz.
func tarNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			if h.Typeflag != tar.TypeDir {
				t.Errorf("%s: type %c in archive", h.Name, h.Typeflag)
			}
			continue
		}
		out[h.Name] = true
	}
}
