package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/config"
)

// releaseRepo commits one plugin.yaml per release (in order) and tags each commit
// <prefix>v<version>. Each release is {version, engine}.
func releaseRepo(t *testing.T, sub, prefix string, releases ...[2]string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for _, rel := range releases {
		m := strings.NewReplacer("version: 1.2.0", "version: "+rel[0], `engine: ">=0.1.0"`, fmt.Sprintf("engine: %q", rel[1])).Replace(manifest)
		for p, c := range map[string]string{"plugin.yaml": m, "src/index.ts": "export default {}"} {
			p = filepath.Join(sub, p)
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(filepath.ToSlash(p)); err != nil {
				t.Fatal(err)
			}
		}
		h, err := wt.Commit("release "+rel[0], &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.CreateTag(prefix+"v"+rel[0], h, nil); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func useEngine(t *testing.T, v string) {
	t.Helper()
	old := engineVersion
	engineVersion = func() string { return v }
	t.Cleanup(func() { engineVersion = old })
}

// Release B1: a 0.1.x engine must never resolve (and lock) a plugin release whose
// spec.engine requires 0.2, on `plugin add` (range "*") or a default `plugin update`.
func TestResolutionSkipsReleasesForAnotherEngine(t *testing.T) {
	mono := releaseRepo(t, "plugins/tickets", "tickets/",
		[2]string{"0.1.0", ">=0.1.0"}, [2]string{"0.1.1", ">=0.1.0 <0.2.0"},
		[2]string{"0.4.0-rc.1", ">=0.2.0-0"}, [2]string{"0.4.0", ">=0.2.0-0"})
	reg := initRepo(t, map[string]string{"index.yaml": "plugins:\n  tickets:\n    repo: " + mono + "\n    path: plugins/tickets\n    version: 0.4.0\n"})
	regs := []config.Registry{{Name: "acme", URL: reg}}
	ctx := context.Background()
	ie := IndexEntry{Repo: mono, Path: "plugins/tickets"}

	for _, tc := range []struct{ engine, want string }{
		{"0.1.11", "0.1.1"},     // the 0.4.0 manifest requires 0.2
		{"0.2.0-rc.1", "0.4.0"}, // pre-release engines satisfy ">=0.2.0-0" (like the loader)
		{"dev", "0.4.0"},        // development builds skip the check, like the loader
	} {
		useEngine(t, tc.engine)
		res, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/tickets", "*")
		if err != nil || res.Entry.Version != tc.want || res.Manifest.Metadata.Version != tc.want {
			t.Fatalf("engine %s: ResolveLatest = %+v, %v; want %s", tc.engine, res, err, tc.want)
		}
		if v, err := LatestFor(ctx, reg, "tickets", ie, false); err != nil || v != tc.want {
			t.Fatalf("engine %s: LatestFor = %q, %v; want %s", tc.engine, v, err, tc.want)
		}
	}

	// No release of the range supports the engine: say which engine the newest needs.
	useEngine(t, "0.1.11")
	_, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/tickets", "^0.4.0")
	if err == nil || !strings.Contains(err.Error(), `requires Cicord ">=0.2.0-0"`) || !strings.Contains(err.Error(), "supports Cicord 0.1.11") {
		t.Fatalf("incompatible range: %v", err)
	}
	// The loader agrees: what is resolved for the engine passes its spec.engine check.
	if !EngineAccepts(">=0.1.0 <0.2.0") || EngineAccepts(">=0.2.0-0") || EngineAccepts("") || EngineAccepts("not a range") {
		t.Fatal("EngineAccepts disagrees with the loader")
	}
}

// Probing is bounded: only the maxEngineProbes newest matching releases are fetched.
func TestResolutionEngineProbesAreBounded(t *testing.T) {
	var rels [][2]string
	rels = append(rels, [2]string{"1.0.0", ">=0.1.0"})
	for i := 1; i <= maxEngineProbes+2; i++ {
		rels = append(rels, [2]string{fmt.Sprintf("2.%d.0", i), ">=9.0.0"})
	}
	plugin := releaseRepo(t, "", "", rels...)
	reg := initRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	useEngine(t, "0.2.0")
	_, err := ResolveLatest(context.Background(), []config.Registry{{Name: "acme", URL: reg}}, t.TempDir(), "acme/hello", "*")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("checked the %d newest", maxEngineProbes)) || !strings.Contains(err.Error(), "2.10.0") {
		t.Fatalf("unbounded or unexplained: %v", err)
	}
	// An explicit older range still resolves.
	res, err := ResolveLatest(context.Background(), []config.Registry{{Name: "acme", URL: reg}}, t.TempDir(), "acme/hello", "^1.0.0")
	if err != nil || res.Entry.Version != "1.0.0" {
		t.Fatalf("^1.0.0 = %+v, %v", res, err)
	}
}

// The official registry vouches for a version through its signed index: a newer tag in the
// repository that the index does not publish yet is never resolved.
func TestOfficialResolutionStopsAtSignedIndexVersion(t *testing.T) {
	useEngine(t, "0.2.0")
	plugin := releaseRepo(t, "", "", [2]string{"1.1.0", ">=0.1.0"}, [2]string{"1.2.0", ">=0.1.0"})
	key := useOfficial(t)
	index := "plugins:\n  hello:\n    repo: " + plugin + "\n    verified: true\n    version: 1.1.0\n"
	url := initRepo(t, map[string]string{"index.yaml": index})
	writeSig(t, url, mustSign(t, index, envFor(url, 1), key))
	OfficialURLs = []string{url}
	ctx := context.Background()
	res, err := ResolveLatest(ctx, nil, t.TempDir(), "cicord/hello", "*")
	if err != nil || res.Entry.Version != "1.1.0" {
		t.Fatalf("ResolveLatest = %+v, %v (want the signed 1.1.0, not the unpublished 1.2.0)", res, err)
	}
	idx, err := FetchIndex(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := LatestFor(ctx, url, "hello", idx.Plugins["hello"], idx.SignedBy != ""); err != nil || v != "1.1.0" {
		t.Fatalf("LatestFor = %q, %v", v, err)
	}
	// An unsigned (third-party) index is not authoritative: no ceiling.
	if v, err := LatestFor(ctx, url, "hello", idx.Plugins["hello"], false); err != nil || v != "1.2.0" {
		t.Fatalf("LatestFor unsigned = %q, %v", v, err)
	}
}
