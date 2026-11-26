package registry

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Exercise small limits using a normal Git encoder and ordinary text blobs.
func TestRegistryPackQuotas(t *testing.T) {
	store := memory.NewStorage()
	var hashes []plumbing.Hash
	for _, sample := range []string{"alpha", "bravo"} {
		o := store.NewEncodedObject()
		o.SetType(plumbing.BlobObject)
		o.SetSize(int64(len(sample)))
		w, err := o.Writer()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.WriteString(w, sample)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		h, err := store.SetEncodedObject(o)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	var b bytes.Buffer
	if _, err := packfile.NewEncoder(&b, store, false).Encode(hashes, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		objects       uint32
		single, total int64
		pass          bool
	}{{"fits", 2, 5, 10, true}, {"count", 1, 5, 10, false}, {"blob", 2, 4, 10, false}, {"expanded total", 2, 5, 9, false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPack(t.Context(), b.Bytes(), tc.objects, tc.single, tc.total)
			if tc.pass && err != nil || !tc.pass && !errors.Is(err, ErrRegistryLimit) {
				t.Fatal(err)
			}
		})
	}
	var d deltaSizes
	_, _ = d.Write(binary.AppendUvarint(binary.AppendUvarint(nil, 5), 8))
	if _, err := d.target(7); !errors.Is(err, ErrRegistryLimit) {
		t.Fatal("delta expansion quota", err)
	}
	if n, err := d.target(8); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	for _, sample := range []string{"four", "longer"} {
		in := &limitedResponse{ReadCloser: io.NopCloser(strings.NewReader(sample)), remaining: 4}
		_, err := io.ReadAll(in)
		if (len(sample) > 4) != errors.Is(err, ErrRegistryLimit) {
			t.Fatal(sample, err)
		}
	}
}
func TestRegistryPackageQuotas(t *testing.T) {
	repo, err := git.PlainOpen(initRepo(t, map[string]string{"a.txt": "alpha", "b.txt": "bravo"}))
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		files         int
		total, single int64
		pass          bool
	}{{2, 10, 5, true}, {1, 10, 5, false}, {2, 9, 5, false}, {2, 10, 4, false}} {
		files, err := packageFiles(tree, tc.files, tc.total, tc.single)
		if tc.pass && (err != nil || len(files) != 2) || !tc.pass && !errors.Is(err, ErrRegistryLimit) {
			t.Fatal(tc, files, err)
		}
	}
}

func TestPublicOnlyRegistryRequiresHTTPS(t *testing.T) {
	for _, raw := range []string{"file:///ordinary-registry", "http://registry.example.org/source", "ssh://registry.example.org/source"} {
		_, _, err := scopedFetch(WithPublicOnly(t.Context()), raw)
		if !errors.Is(err, ErrNotPublic) {
			t.Fatal(raw, err)
		}
	}
}
