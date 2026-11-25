package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// portalDiags loads a copy of the example with cicord.yaml spec.portal.path set to path
// (after prepare ran on the copy) and returns the portal diagnostics.
func portalDiags(t *testing.T, path string, prepare func(dir string)) Diagnostics {
	t.Helper()
	dir := copyExample(t)
	if prepare != nil {
		prepare(dir)
	}
	f := filepath.Join(dir, "cicord.yaml")
	data, _ := os.ReadFile(f)
	if err := os.WriteFile(f, append(data, []byte("  portal:\n    path: "+path+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ds := Load(dir, LoadOptions{})
	var out Diagnostics
	for _, d := range ds {
		if strings.Contains(d.Path, "portal") {
			out = append(out, d)
		}
	}
	return out
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPortalPathValidation(t *testing.T) {
	built := func(dir string) { mustWrite(t, filepath.Join(dir, "portal", "dist", "index.html"), "<html></html>") }
	if ds := portalDiags(t, "./portal/dist", built); len(ds) != 0 {
		t.Errorf("built portal: %v", ds)
	}

	cases := []struct {
		name, path string
		prepare    func(dir string)
		severity   Severity
		want       string
	}{
		{"missing", "./portal/dist", nil, SeverityWarning, "does not exist"},
		{"no index", "./portal/dist", func(dir string) { mustWrite(t, filepath.Join(dir, "portal", "dist", "app.js"), "") }, SeverityWarning, "index.html not found"},
		{"outside", "../elsewhere", nil, SeverityError, "inside the repository"},
		{"root", "./", nil, SeverityError, "inside the repository"},
		{"file", "./cicord.yaml", nil, SeverityError, "must be a directory"},
		{"symlink", "./portal/dist", func(dir string) {
			out := t.TempDir()
			mustWrite(t, filepath.Join(out, "index.html"), "x")
			if err := os.MkdirAll(filepath.Join(dir, "portal"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(out, filepath.Join(dir, "portal", "dist")); err != nil {
				t.Fatal(err)
			}
		}, SeverityError, "symbolic link"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := portalDiags(t, tc.path, tc.prepare)
			if len(ds) != 1 || ds[0].Severity != tc.severity || !strings.Contains(ds[0].Message, tc.want) || ds[0].File != "cicord.yaml" || ds[0].Line == 0 {
				t.Fatalf("diagnostics = %v", ds)
			}
		})
	}
}

func TestPortalImagesValidation(t *testing.T) {
	_, ds := Load(exampleDir(t), LoadOptions{})
	for _, d := range ds {
		if strings.Contains(d.Path, "portal") {
			t.Errorf("example portal: %v", d)
		}
	}
	dir := copyExample(t)
	srv := filepath.Join(dir, "servers", "my-community.yaml")
	edit(t, srv, "logo: ../assets/logo.svg", "logo: ../assets/logo.txt")
	edit(t, srv, "banner: ../assets/banner.jpg", "banner: ../assets/nope.png")
	_, ds = Load(dir, LoadOptions{})
	var got []string
	for _, d := range ds {
		if strings.Contains(d.Path, "portal") {
			got = append(got, string(d.Severity)+" "+d.Path+" "+d.Message)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], "error /spec/portal/theme/logo") || !strings.Contains(got[0], "not a supported image") ||
		!strings.Contains(got[1], "warning /spec/portal/banner") || !strings.Contains(got[1], "not found") {
		t.Fatalf("diagnostics = %q", got)
	}

	dir = copyExample(t)
	srv = filepath.Join(dir, "servers", "my-community.yaml")
	edit(t, srv, "banner: ../assets/banner.jpg", "banner: ../../outside.png")
	_, ds = Load(dir, LoadOptions{})
	if !ds.HasErrors() || !strings.Contains(ds.Err().Error(), "must stay inside the repository") {
		t.Fatalf("escape not reported: %v", ds)
	}

	// Schema: link URLs must be http(s).
	dir = copyExample(t)
	srv = filepath.Join(dir, "servers", "my-community.yaml")
	edit(t, srv, "url: https://my-community.example\n", "url: javascript:alert(1)\n")
	_, ds = Load(dir, LoadOptions{})
	if !ds.HasErrors() {
		t.Fatal("javascript: link accepted")
	}
}
