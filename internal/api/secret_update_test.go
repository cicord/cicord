package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/setup"
)

func TestStoreUpdateRequiresBoundSecretReview(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	manifest := helloManifest + "  network: {allow: [api.example.org, other.example.org]}\n  secrets: [{name: API_KEY, hosts: [api.example.org]}]\n"
	plugin := gitRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": helloSource}, "v1.0.0")
	reg := gitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	e := startWithRegistry(t, reg, nil)
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	var detail storeDetailDTO
	admin.expect(200, "GET", "/api/v1/store/acme/hello", nil).json(t, &detail)
	admin.expect(200, "POST", "/api/v1/servers/"+serverKey+"/store/install", map[string]any{"source": "acme", "plugin": "hello", "version": detail.Version, "commit": detail.Commit, "integrity": detail.Integrity, "confirmUnofficial": true})
	file := filepath.Join(e.dir, "bots", "moderator", "bot.yaml")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^([ ]*)source: acme/hello$`)
	next := re.ReplaceAllString(string(raw), "${0}\n${1}secrets: {API_KEY: {secretRef: CICORD_TEST_PROVIDER}}")
	if next == string(raw) {
		t.Fatal("missing fixture source")
	}
	if err := os.WriteFile(file, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	e.rt.Trigger()
	e.waitFor("bound credential", func() bool {
		for _, i := range e.rt.Instances(serverKey) {
			if i.Config.Name == "hello" && i.Config.SecretRefs["API_KEY"].SecretRef != "" {
				return true
			}
		}
		return false
	})
	expanded := strings.Replace(strings.Replace(manifest, "1.0.0", "1.1.0", 1), "hosts: [api.example.org]", "hosts: [api.example.org, other.example.org]", 1)
	release(t, plugin, map[string]string{"plugin.yaml": expanded}, "v1.1.0")
	admin.expect(200, "POST", "/api/v1/store/refresh", nil)
	path := "/api/v1/store/acme/hello/update"
	var review struct {
		SecretReview string
		Detail       storeDetailDTO
		Instances    []setup.UpdateInstance
	}
	admin.expect(200, "GET", path, nil).json(t, &review)
	if len(review.SecretReview) != 64 || len(review.Instances) != 1 || len(review.Instances[0].SecretChanges) != 1 || !review.Instances[0].SecretChanges[0].Widened {
		t.Fatal("missing bound secret review")
	}
	req := map[string]any{"version": review.Detail.Version, "commit": review.Detail.Commit, "integrity": review.Detail.Integrity, "confirmUnofficial": true}
	admin.expect(409, "POST", path, req)
	req["confirmSecretChanges"] = "stale"
	admin.expect(409, "POST", path, req)
	req["confirmSecretChanges"] = review.SecretReview
	admin.expect(200, "POST", path, req)
}
