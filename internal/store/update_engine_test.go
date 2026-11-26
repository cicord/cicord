package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/version"
)

// The Store's update badge offers what `cicord plugin update` resolves: never a release
// whose spec.engine refuses the running engine (the tags-only lookup offered it).
func TestUpdateBadgeSkipsReleasesForAnotherEngine(t *testing.T) {
	old := version.Version
	version.Version = "0.1.11"
	t.Cleanup(func() { version.Version = old })

	dir := t.TempDir()
	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for _, rel := range [][2]string{{"0.1.0", ">=0.1.0"}, {"0.1.1", ">=0.1.0 <0.2.0"}, {"0.2.0", ">=0.2.0"}} {
		manifest := "apiVersion: cicord.dev/v1alpha1\nkind: Plugin\nmetadata: {name: xp, version: " + rel[0] + "}\nspec: {entry: ./src/index.ts, engine: '" + rel[1] + "'}\n"
		for p, c := range map[string]string{"plugins/xp/plugin.yaml": manifest, "plugins/xp/src/index.ts": "export default {}"} {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(p); err != nil {
				t.Fatal(err)
			}
		}
		h, err := wt.Commit(rel[0], &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.CreateTag("xp/v"+rel[0], h, nil); err != nil {
			t.Fatal(err)
		}
	}
	regDir := t.TempDir()
	s := &Service{Fetch: func(context.Context, string) (*registry.Index, error) {
		return &registry.Index{Plugins: map[string]registry.IndexEntry{"xp": {Repo: dir, Path: "plugins/xp", Version: "0.2.0"}}}, nil
	}}
	root := t.TempDir()
	if err := registry.WriteLock(root, &registry.Lock{Plugins: map[string]registry.LockEntry{
		"acme/xp": {Version: "0.1.0", Repo: dir, Commit: "c"},
	}}); err != nil {
		t.Fatal(err)
	}
	repo := repoWith(config.Registry{Name: "acme", URL: regDir})
	repo.Root = root
	repo.Instances = []config.Instance{{Bot: "b", Name: "xp", Server: "s",
		Plugin: &config.LoadedPlugin{Source: "acme/xp", Manifest: &config.Plugin{Metadata: config.PluginMetadata{Name: "xp", Version: "0.1.0"}}}}}
	ups := s.Updates(context.Background(), repo, FetchMissing)
	if u := ups["acme/xp"]; u.Latest != "0.1.1" || u.Level != "patch" {
		t.Fatalf("update = %+v, want 0.1.1 (0.2.0 requires engine >=0.2.0)", u)
	}
}
