package cli

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/gitauth"
)

func TestReadSecretValue(t *testing.T) {
	// A PEM block (the type is not a key: secret scanners).
	block := "-----BEGIN TEST BLOCK-----\nAAAA\nBBBB\n-----END TEST BLOCK-----"
	cases := map[string]string{
		"token\n":                            "token",
		"  token  \r\nsecond line\n":         "token",
		"token-without-newline":              "token-without-newline",
		block + "\n":                         block,
		block + "\ntrailing junk\n":          block,
		"\n" + block:                         "",
		"-----BEGIN TEST BLOCK-----\nAAAA\n": "-----BEGIN TEST BLOCK-----\nAAAA",
		"  " + strings.ReplaceAll(block, "\n", "\r\n") + "\r\n": block,
	}
	for in, want := range cases {
		got, err := readSecretValue(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := readSecretValue(strings.NewReader("-----BEGIN TEST BLOCK-----\n" + strings.Repeat("A\n", maxSecretValue))); err == nil {
		t.Error("oversized PEM accepted")
	}
	if _, err := readSecretValue(strings.NewReader(strings.Repeat("A", maxSecretValue+10))); err == nil {
		t.Error("oversized line accepted")
	}
}

func TestAdminURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://cicord.example.org":      "https://cicord.example.org/admin/",
		"https://cicord.example.org/":     "https://cicord.example.org/admin/",
		"https://example.org/cicord":      "https://example.org/cicord/admin/",
		"http://localhost:7420":           "",
		"http://127.0.0.1:7420":           "",
		"http://[::1]:7420":               "",
		"http://0.0.0.0:7420":             "",
		"http://cicord.localhost":         "",
		"":                                "",
		"ftp://cicord.example.org":        "",
		"http://cicord.internal.lan:7420": "http://cicord.internal.lan:7420/admin/",
	} {
		if got := adminURL(in); got != want {
			t.Errorf("adminURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommitStatusHook(t *testing.T) {
	if commitStatusHook(nil, "", gitauth.Static("x"), "") != nil || commitStatusHook(nil, "acme/community", nil, "") != nil {
		t.Fatal("a hook without a GitHub repository or credentials")
	}
	if commitStatusHook(nil, "acme/community", gitauth.Static("x"), "https://cicord.example.org") == nil {
		t.Fatal("no hook for a GitHub repository with credentials")
	}
}
