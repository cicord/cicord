package registry

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// memoryTree builds a git tree of files in memory: git itself is case-sensitive, so the
// colliding names coexist there whatever the test machine's filesystem.
func memoryTree(t *testing.T, files map[string]string) *object.Tree {
	t.Helper()
	st := memory.NewStorage()
	var build func(prefix string) plumbing.Hash
	build = func(prefix string) plumbing.Hash {
		entries := map[string]object.TreeEntry{}
		for p, c := range files {
			rest, ok := strings.CutPrefix(p, prefix)
			if !ok {
				continue
			}
			if dir, _, nested := strings.Cut(rest, "/"); nested {
				if _, done := entries[dir]; !done {
					entries[dir] = object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: build(prefix + dir + "/")}
				}
				continue
			}
			blob := st.NewEncodedObject()
			blob.SetType(plumbing.BlobObject)
			w, _ := blob.Writer()
			_, _ = w.Write([]byte(c))
			_ = w.Close()
			h, err := st.SetEncodedObject(blob)
			if err != nil {
				t.Fatal(err)
			}
			entries[rest] = object.TreeEntry{Name: rest, Mode: filemode.Regular, Hash: h}
		}
		tree := &object.Tree{}
		for _, e := range entries {
			tree.Entries = append(tree.Entries, e)
		}
		sort.Slice(tree.Entries, func(a, b int) bool { return tree.Entries[a].Name < tree.Entries[b].Name })
		obj := st.NewEncodedObject()
		if err := tree.Encode(obj); err != nil {
			t.Fatal(err)
		}
		h, err := st.SetEncodedObject(obj)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	tree, err := object.GetTree(st, build(""))
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// SEC-B-04: a package whose paths collide once case or Unicode normalization is folded is
// refused before it reaches a cache on macOS or Windows.
func TestPackageFilesRefuseFoldCollisions(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"file case":      {"plugin.yaml": manifest, "src/index.ts": "benign()", "src/Index.ts": "evil()"},
		"directory case": {"plugin.yaml": manifest, "src/a.ts": "benign()", "SRC/b.ts": "evil()"},
		"normalization":  {"plugin.yaml": manifest, "src/café.ts": "benign()", "src/café.ts": "evil()"},
		"full fold":      {"plugin.yaml": manifest, "src/straße.ts": "benign()", "src/STRASSE.ts": "evil()"},
	} {
		if _, err := packageFiles(memoryTree(t, files), maxPackageFiles, maxPackageSize, maxObjectSize); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok := map[string]string{"plugin.yaml": manifest, "src/index.ts": "x", "src/index.test.ts": "y", "src/lib/index.ts": "z"}
	if files, err := packageFiles(memoryTree(t, ok), maxPackageFiles, maxPackageSize, maxObjectSize); err != nil || len(files) != 4 {
		t.Fatalf("distinct paths refused: %v %v", files, err)
	}
}

// SEC-B-04: the staged cache tree is verified before it is published, so a filesystem that
// merges two package paths never yields a cached package that differs from the lock.
func TestInstallCacheVerifiesStagedTree(t *testing.T) {
	cache := t.TempDir()
	parent, err := os.OpenRoot(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	files := map[string][]byte{"plugin.yaml": []byte(manifest), "src/index.ts": []byte("benign()")}
	if err := installCache(t.Context(), parent, "wrong", Integrity(map[string][]byte{"plugin.yaml": []byte("other")}), files); err == nil {
		t.Fatal("published a tree that does not match its integrity")
	}
	if _, err := os.Stat(filepath.Join(cache, "wrong")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched tree was published: %v", err)
	}
	// Two names that one filesystem folds together: refused there, cached elsewhere.
	folded := map[string][]byte{"plugin.yaml": []byte(manifest), "a.ts": []byte("benign()"), "A.ts": []byte("evil()")}
	err = installCache(t.Context(), parent, "folded", Integrity(folded), folded)
	insensitive := caseInsensitive(t, cache)
	if insensitive && err == nil {
		t.Fatal("case-insensitive filesystem published a merged package")
	}
	if !insensitive && err != nil {
		t.Fatalf("case-sensitive filesystem: %v", err)
	}
}

func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}
