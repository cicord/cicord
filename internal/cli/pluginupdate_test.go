package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/npjonath/cicord/internal/registry"
)

const updManifest = `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata:
  name: hello
  version: 1.0.0
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [discord.messages.send]
`

func tagRelease(t *testing.T, dir string, files map[string]string, tag string) {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		writeFile(t, filepath.Join(dir, p), c)
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	h, err := wt.Commit(tag, &git.CommitOptions{Author: &object.Signature{Name: "d", Email: "d@x", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateTag(tag, h, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPluginUpdate(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	plugin := commitRepo(t, map[string]string{"plugin.yaml": updManifest, "src/index.ts": "export default {};\n"}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, _, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 {
		t.Fatalf("registry add: %s", errOut)
	}
	if code, _, errOut := run(t, "plugin", "add", "acme/hello", "--bot", "bot", "--dir", dir); code != 0 {
		t.Fatalf("plugin add: %s", errOut)
	}
	if code, out, errOut := run(t, "plugin", "update", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("list before a release: %d %s %s", code, out, errOut)
	}

	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(strings.Replace(updManifest, "1.0.0", "1.1.0", 1),
		"[discord.messages.send]", "[discord.messages.send, storage.kv]", 1)}, "v1.1.0")
	if code, out, errOut := run(t, "plugin", "update", "--dir", dir); code != 0 || !strings.Contains(out, "acme/hello") ||
		!strings.Contains(out, "1.1.0") || !strings.Contains(out, "minor") {
		t.Fatalf("list: %d %s %s", code, out, errOut)
	}
	code, out, errOut := run(t, "plugin", "update", "acme/hello", "--dir", dir)
	if code != 0 || !strings.Contains(out, "1.0.0 → 1.1.0") || !strings.Contains(out, "+ capability storage.kv") {
		t.Fatalf("update: %d %s %s", code, out, errOut)
	}
	lock, _ := registry.ReadLock(dir)
	if lock.Plugins["acme/hello"].Version != "1.1.0" {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
	bot, _ := os.ReadFile(filepath.Join(dir, "bots", "bot", "bot.yaml"))
	if !strings.Contains(string(bot), "storage.kv") || !strings.Contains(string(bot), "version: ^1.0.0") {
		t.Errorf("bot.yaml:\n%s", bot)
	}
	if code, out, _ := run(t, "plugin", "update", "acme/hello", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("second update: %d %s", code, out)
	}

	// A new major version: the range is widened (or set with --to).
	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(updManifest, "1.0.0", "2.0.0", 1)}, "v2.0.0")
	if code, out, errOut := run(t, "plugin", "update", "acme/hello", "--to", "~1.1.0", "--dir", dir); code != 0 || !strings.Contains(out, "^1.0.0 → ~1.1.0") {
		t.Fatalf("update --to: %d %s %s", code, out, errOut)
	}
	code, out, errOut = run(t, "plugin", "update", "acme/hello", "--dir", dir)
	if code != 0 || !strings.Contains(out, "- capability storage.kv") || !strings.Contains(out, "~1.1.0 → ^2.0.0") {
		t.Fatalf("major update: %d %s %s", code, out, errOut)
	}
	bot, _ = os.ReadFile(filepath.Join(dir, "bots", "bot", "bot.yaml"))
	if strings.Contains(string(bot), "storage.kv") || !strings.Contains(string(bot), "version: ^2.0.0") {
		t.Errorf("bot.yaml after the major update:\n%s", bot)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate after update: %s", errOut)
	}
}
