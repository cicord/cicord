package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/registry"
)

func TestRegistryKeygenSignVerify(t *testing.T) {
	const regURL = "https://github.com/codi-dev/registry"
	t.Setenv(registry.DevOfficialRegistryEnv, "")
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys", "registry.key")
	code, out, errOut := run(t, "registry", "keygen", "--out", keyFile)
	if code != 0 {
		t.Fatalf("keygen = %d %s", code, errOut)
	}
	pub := regexp.MustCompile(`Public key: (\S+)`).FindStringSubmatch(out)
	if pub == nil {
		t.Fatalf("no public key in %s", out)
	}
	if _, err := registry.ParsePublicKey(pub[1]); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(keyFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file mode: %v %v", st, err)
	}
	if code, _, errOut := run(t, "registry", "keygen", "--out", keyFile); code == 0 || !strings.Contains(errOut, "refusing to overwrite") {
		t.Errorf("overwrite = %d %s", code, errOut)
	}

	reg := t.TempDir()
	index := "plugins:\n  xp:\n    repo: https://example.org/xp.git\n    publisher: acme\n    verified: true\n"
	writeFile(t, filepath.Join(reg, "index.yaml"), index)
	if code, _, errOut := run(t, "registry", "verify", reg, "--public-key", pub[1]); code != 1 || !strings.Contains(errOut, "no index.yaml.sig") {
		t.Errorf("verify unsigned = %d %s", code, errOut)
	}
	code, out, errOut = run(t, "registry", "sign", reg, "--key", keyFile, "--registry-url", regURL)
	if code != 0 || !strings.Contains(out, "Signed") {
		t.Fatalf("sign = %d %s %s", code, out, errOut)
	}
	if code, out, _ := run(t, "registry", "verify", reg, "--public-key", pub[1], "--registry-url", regURL); code != 0 || !strings.Contains(out, "signed by key") {
		t.Errorf("verify = %d %s", code, out)
	}
	if code, _, errOut := run(t, "registry", "verify", reg, "--public-key", pub[1], "--registry-url", "https://other.example/r"); code != 1 || !strings.Contains(errOut, "for the registry") {
		t.Errorf("verify for another registry = %d %s", code, errOut)
	}
	// Each signature gets a higher serial; --serial cannot go back.
	first, _ := registry.ParseEnvelope([]byte(readFile(t, filepath.Join(reg, "index.yaml.sig"))))
	if code, _, _ := run(t, "registry", "sign", reg, "--key", keyFile, "--registry-url", regURL); code != 0 {
		t.Fatal("re-sign")
	}
	second, _ := registry.ParseEnvelope([]byte(readFile(t, filepath.Join(reg, "index.yaml.sig"))))
	if second.Serial <= first.Serial {
		t.Errorf("serials %d then %d", first.Serial, second.Serial)
	}
	if code, _, errOut := run(t, "registry", "sign", reg, "--key", keyFile, "--registry-url", regURL, "--serial", "3"); code == 0 || !strings.Contains(errOut, "must be greater") {
		t.Errorf("lower --serial = %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "registry", "sign", reg, "--key", keyFile, "--registry-url", regURL, "--expires-in", "8760h"); code == 0 || !strings.Contains(errOut, "--expires-in") {
		t.Errorf("long validity = %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "registry", "sign", reg, "--key", keyFile); code == 0 || !strings.Contains(errOut, "--registry-url is required") {
		t.Errorf("no registry URL = %d %s", code, errOut)
	}
	// This build embeds no official key: the default verification fails.
	if code, _, _ := run(t, "registry", "verify", reg); code != 1 {
		t.Errorf("verify with the (empty) official keys = %d", code)
	}
	// Any change to index.yaml invalidates the signature.
	writeFile(t, filepath.Join(reg, "index.yaml"), index+"  evil:\n    repo: https://evil.example/x.git\n")
	if code, _, _ := run(t, "registry", "verify", reg, "--public-key", pub[1]); code != 1 {
		t.Errorf("verify after a change = %d", code)
	}

	// The key can come from an environment variable (never from a flag value).
	t.Setenv("TEST_REGISTRY_KEY", readFile(t, keyFile))
	if code, _, errOut := run(t, "registry", "sign", reg, "--key", "env:TEST_REGISTRY_KEY", "--registry-url", regURL); code != 0 {
		t.Errorf("sign from env = %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "registry", "sign", reg, "--key", "env:TEST_REGISTRY_UNSET", "--registry-url", regURL); code == 0 || !strings.Contains(errOut, "not set") {
		t.Errorf("unset env = %d %s", code, errOut)
	}

	// The index is checked before signing.
	writeFile(t, filepath.Join(reg, "index.yaml"), "plugins:\n  Bad.Name:\n    repo: https://x/y.git\n  leak:\n    repo: https://tok@github.com/a/b.git\n  local:\n    repo: /srv/x\n")
	code, _, errOut = run(t, "registry", "sign", reg, "--key", keyFile, "--registry-url", regURL)
	if code == 0 || !strings.Contains(errOut, "invalid plugin name") || !strings.Contains(errOut, "credentials") || !strings.Contains(errOut, "https://") {
		t.Errorf("sign invalid index = %d %s", code, errOut)
	}
}
