package cli

import (
	"github.com/cicord/cicord/internal/registry"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPluginUpdateBindsCredentialApproval(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	manifest := updManifest + "  network: {allow: [api.example.org, other.example.org]}\n  secrets: [{name: API_KEY, hosts: [api.example.org]}]\n"
	plugin := commitRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "import {definePlugin} from '@cicord/sdk';export default definePlugin({});"}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	dir := t.TempDir()
	for _, args := range [][]string{{"init", dir, "--server", "srv", "--bot", "bot"}, {"registry", "add", "acme", reg, "--dir", dir}, {"plugin", "add", "acme/hello", "--bot", "bot", "--dir", dir}} {
		if code, _, why := run(t, args...); code != 0 {
			t.Fatal(why)
		}
	}
	file := filepath.Join(dir, "bots", "bot", "bot.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	re := regexp.MustCompile(`(?m)^([ ]*)source: acme/hello$`)
	changed := re.ReplaceAllString(original, "${0}\n${1}secrets: {API_KEY: {secretRef: TEST_PROVIDER}}")
	if original == changed {
		t.Fatal("missing fixture source")
	}
	writeFile(t, file, changed)
	next := strings.Replace(strings.Replace(manifest, "1.0.0", "1.1.0", 1), "hosts: [api.example.org]", "hosts: [api.example.org, other.example.org]", 1)
	tagRelease(t, plugin, map[string]string{"plugin.yaml": next}, "v1.1.0")
	args := []string{"plugin", "update", "acme/hello", "--dir", dir}
	code, out, why := run(t, args...)
	if code == 0 || !strings.Contains(out, "credential API_KEY") || !strings.Contains(out, "other.example.org") {
		t.Fatalf("missing privilege review: %d %s %s", code, out, why)
	}
	match := regexp.MustCompile(`--approve-secret-changes ([a-f0-9]{64})`).FindStringSubmatch(why)
	if len(match) != 2 {
		t.Fatal(why)
	}
	lock, _ := registry.ReadLock(dir)
	if lock.Plugins["acme/hello"].Version != "1.0.0" {
		t.Fatal("refusal changed lockfile")
	}
	if code, _, _ := run(t, append(args, "--approve-secret-changes", "stale")...); code == 0 {
		t.Fatal("stale approval accepted")
	}
	if code, _, why := run(t, append(args, "--approve-secret-changes", match[1])...); code != 0 {
		t.Fatal(why)
	}
}
