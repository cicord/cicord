package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, entries ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := ""
		if h.Typeflag == tar.TypeReg {
			body = "content of " + h.Name
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractTarGz(t *testing.T) {
	dst := t.TempDir()
	err := extractTarGz(tarGz(t,
		&tar.Header{Name: "./repo/", Typeflag: tar.TypeDir, Mode: 0o755},
		&tar.Header{Name: "repo/cicord.yaml", Typeflag: tar.TypeReg, Mode: 0o644},
		&tar.Header{Name: "repo/servers/a.yaml", Typeflag: tar.TypeReg, Mode: 0o644},
	), dst)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "repo", "servers", "a.yaml")); err != nil || string(b) != "content of repo/servers/a.yaml" {
		t.Fatalf("extracted file = %q, %v", b, err)
	}

	outside := t.TempDir()
	for name, h := range map[string]*tar.Header{
		"parent":   {Name: "../escape.yaml", Typeflag: tar.TypeReg},
		"nested":   {Name: "repo/../../escape.yaml", Typeflag: tar.TypeReg},
		"absolute": {Name: filepath.Join(outside, "escape.yaml"), Typeflag: tar.TypeReg},
		"symlink":  {Name: "link", Typeflag: tar.TypeSymlink, Linkname: outside},
	} {
		if err := extractTarGz(tarGz(t, h), t.TempDir()); err == nil {
			t.Errorf("%s: extracted %q", name, h.Name)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("files written outside the destination: %v", entries)
	}
	if err := extractTarGz(strings.NewReader("not gzip"), t.TempDir()); err == nil {
		t.Error("garbage accepted")
	}
}
