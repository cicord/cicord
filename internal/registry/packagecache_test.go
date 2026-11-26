package registry

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/config"
)

func TestCachedPackageMatchesActualContents(t *testing.T) {
	origin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default 42;", ".ordinary": "part of the package"}, "v1.2.0")
	index := initRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + origin + "\n"})
	cache := t.TempDir()
	ctx := t.Context()
	res, err := ResolveLatest(ctx, []config.Registry{{Name: "sample", URL: index}}, cache, "sample/hello", "*")
	if err != nil {
		t.Fatal(err)
	}
	dir := res.Dir
	file := filepath.Join(dir, "src/index.ts")
	publicSample := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(publicSample, []byte("ordinary external sample"), 0644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		drift func() error
	}{
		{"edited", func() error { return os.WriteFile(file, []byte("changed"), 0644) }},
		{"missing", func() error { return os.Remove(file) }},
		{"extra file", func() error { return os.WriteFile(filepath.Join(dir, "additional.js"), nil, 0644) }},
		{"hidden addition", func() error { return os.WriteFile(filepath.Join(dir, ".ordinary-extra"), nil, 0644) }},
		{"forged marker", func() error {
			return os.WriteFile(filepath.Join(dir, ".cicord-integrity"), []byte(res.Entry.Integrity+"\n"), 0644)
		}},
		{"symlink", func() error {
			if err := os.Remove(file); err != nil {
				return err
			}
			return os.Symlink(publicSample, file)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.drift(); err != nil {
				t.Fatal(err)
			}
			offline := res.Entry
			offline.Repo = filepath.Join(t.TempDir(), "unavailable")
			if got, err := Materialize(ctx, cache, "sample/hello", offline); err == nil || got != "" {
				t.Fatalf("trusted corrupt cache %q", got)
			}
			got, err := Materialize(ctx, cache, "sample/hello", res.Entry)
			if err != nil || got != dir {
				t.Fatal(got, err)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "export default 42;" {
				t.Fatal("wrong restored contents", err)
			}
			if got, err = Materialize(ctx, cache, "sample/hello", offline); err != nil || got != dir {
				t.Fatal("verified cache unavailable offline", err)
			}
		})
	}
	data, err := os.ReadFile(publicSample)
	if err != nil || string(data) != "ordinary external sample" {
		t.Fatal("external file changed", err)
	}
}
func TestLegacyCacheRehashAndConcurrentRepair(t *testing.T) {
	origin := initRepo(t, map[string]string{"plugin.yaml": manifest}, "v1.2.0")
	index := initRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + origin + "\n"})
	cache := t.TempDir()
	ctx := t.Context()
	res, err := ResolveLatest(ctx, []config.Registry{{Name: "sample", URL: index}}, cache, "sample/hello", "*")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(filepath.Dir(res.Dir), legacyCacheName(res.Entry))
	if err = os.Rename(res.Dir, legacy); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(legacy, ".cicord-integrity"), []byte(res.Entry.Integrity+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	off := res.Entry
	off.Repo = "unavailable"
	if got, err := Materialize(ctx, cache, "sample/hello", off); got != legacy || err != nil {
		t.Fatal("legacy verified offline", got, err)
	}
	if err = os.WriteFile(filepath.Join(legacy, ".added"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			d, err := Materialize(ctx, cache, "sample/hello", res.Entry)
			if err != nil || d != res.Dir {
				t.Errorf("repair: %s %v", d, err)
			}
		})
	}
	wg.Wait()
	if _, err := os.Stat(filepath.Join(res.Dir, ".added")); !os.IsNotExist(err) {
		t.Fatal("unreviewed file survived repair", err)
	}
}
func TestRedirectedCacheParentIsRejected(t *testing.T) {
	cache := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(cache, "sample")); err != nil {
		t.Fatal(err)
	}
	_, err := openCacheParent(cache, filepath.Join(cache, "sample", "hello", "package"))
	if err == nil {
		t.Fatal("accepted redirected registry directory")
	}
	contents, err := os.ReadDir(external)
	if err != nil || len(contents) != 0 {
		t.Fatalf("cache wrote outside its root: %v %v", contents, err)
	}
}
