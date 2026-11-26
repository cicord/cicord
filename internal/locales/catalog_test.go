package locales

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "locales"), 0700); err != nil {
		t.Fatal(err)
	}
	for f, data := range map[string]string{"fr": `{"hello":"Bonjour {name}"}`, "en": `{"hello":"Hello {name}","help":"Help"}`} {
		if err := os.WriteFile(filepath.Join(dir, "locales", f+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Load(dir, "./locales")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Text("hello", map[string]any{"name": "{admin}", "admin": "staff"}, "fr-CA"); err != nil || got != "Bonjour {admin}" {
		t.Fatal(got, err)
	}
	if got, err := c.Text("help", nil, "fr"); err != nil || got != "Help" {
		t.Fatal(got, err)
	}
	if _, err := Load(dir, "../"); err == nil {
		t.Fatal("accepted parent")
	}
	if err := os.Symlink(filepath.Join(dir, "locales", "fr.json"), filepath.Join(dir, "locales", "de.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "./locales"); err == nil {
		t.Fatal("accepted symlink")
	}
}

// A template that repeats one placeholder must not multiply a large variable on the
// host: the review's reproduction allocated 4.8 GiB in one synchronous call.
func TestTextOutputIsBounded(t *testing.T) {
	c := Catalog{"en": {"k": strings.Repeat("{a}", 2730), "ok": "<{a}>"}}
	vars := map[string]any{"a": strings.Repeat("x", 2_000_000)}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := c.Text("k", vars)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("got %v, want ErrTextTooLong", err)
	}
	// Unbounded, this call allocated 5.4 GB. The race detector's own allocations
	// stay far below the limit.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Fatalf("substitution allocated %d bytes before failing", grew)
	}
	if got, err := c.Text("ok", map[string]any{"a": strings.Repeat("y", MaxText-2)}); err != nil || len(got) != MaxText {
		t.Fatalf("text at the limit: %d bytes, %v", len(got), err)
	}
	if _, err := c.Text("ok", map[string]any{"a": strings.Repeat("y", MaxText-1)}); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("one byte over the limit: %v", err)
	}
}

// Numbers read like JavaScript's String(n), never Go's exponent form for large integers.
func TestNumberVarsFormatLikeJavaScript(t *testing.T) {
	for n, want := range map[float64]string{1234567: "1234567", 1e20: "100000000000000000000", 1e21: "1e+21", 0.5: "0.5", 1.5e-7: "1.5e-7", -42: "-42", 0: "0", 123456789012: "123456789012"} {
		if got := jsNumber(n); got != want {
			t.Errorf("jsNumber(%v) = %q, want %q", n, got, want)
		}
	}
}
