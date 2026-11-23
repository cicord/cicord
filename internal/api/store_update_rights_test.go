package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The version is shared by every instance: an admin of one server sees only that server's
// instances in the review (not another server's config or diagnostics) and cannot apply
// the update while the plugin also runs on a server where they are not admin.
func TestStoreUpdateNeedsAdminEverywhere(t *testing.T) {
	e, pluginDir := startStoreRepoWith(t, func(dir string) {
		writeTestFile(t, filepath.Join(dir, "servers", "annexe.yaml"),
			"apiVersion: codi.dev/v1alpha1\nkind: Server\nmetadata:\n  name: annexe\nspec:\n  discordId: \"223344556677889900\"\n")
		p := filepath.Join(dir, "bots", "moderator", "bot.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(data), "servers: [ma-commu]", "servers: [ma-commu, annexe]", 1)
		for _, n := range []string{"welcome", "onboarding"} {
			s = strings.Replace(s, "      source: ../../plugins/"+n+"\n", "      source: ../../plugins/"+n+"\n      servers: [ma-commu]\n", 1)
		}
		writeTestFile(t, p, s)
	})
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	var d storeDetailDTO
	owner.expect(200, "GET", "/api/v1/store/acme/hello", nil).json(t, &d)
	for server, instance := range map[string]string{serverKey: "hello", "annexe": "hello-annexe"} {
		owner.expect(200, "POST", "/api/v1/servers/"+server+"/store/install", map[string]any{"source": "acme", "plugin": "hello",
			"instance": instance, "version": d.Version, "commit": d.Commit, "integrity": d.Integrity, "confirmUnofficial": true})
	}
	release(t, pluginDir, map[string]string{"plugin.yaml": strings.Replace(helloManifest, "version: 1.0.0", "version: 1.1.0", 1)}, "v1.1.0")
	owner.expect(200, "POST", "/api/v1/store/refresh", nil)

	// An admin of ma-commu only (the admin role there, nothing on annexe).
	u := e.world.CreateUser("local-admin")
	if err := e.world.AddMember(e.serverID(), u.ID); err != nil {
		t.Fatal(err)
	}
	role, ok := e.world.RoleByName(e.serverID(), e.repo.Servers[serverKey].Spec.Roles["admin"].Name)
	if !ok {
		t.Fatal("admin role not created")
	}
	if err := e.world.AdminAddMemberRole(e.serverID(), u.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	c.login(u.ID)

	r := c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil)
	var rv struct {
		updateReviewTestDTO
		Restricted []struct {
			Server    string
			Instances int
		}
	}
	r.json(t, &rv)
	if len(rv.MissingAdmin) != 1 || rv.MissingAdmin[0] != "annexe" || len(rv.Restricted) != 1 || rv.Restricted[0].Instances != 1 {
		t.Fatalf("review = %+v", rv)
	}
	if len(rv.Instances) != 1 || rv.Instances[0].Server != serverKey || strings.Contains(string(r.body), "hello-annexe") {
		t.Fatalf("instances of another server leaked: %s", r.body)
	}
	req := map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit, "integrity": rv.Detail.Integrity, "confirmUnofficial": true}
	if r := c.do("POST", "/api/v1/store/acme/hello/update", req); r.status != 403 || !strings.Contains(string(r.body), "annexe") {
		t.Fatalf("update by an admin of one server: %d %s", r.status, r.body)
	}
	// The owner (admin everywhere) sees both and can update.
	var full updateReviewTestDTO
	owner.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &full)
	if len(full.Instances) != 2 || len(full.MissingAdmin) != 0 {
		t.Fatalf("owner review = %+v", full)
	}
	var res updateResultDTO
	owner.expect(200, "POST", "/api/v1/store/acme/hello/update", req).json(t, &res)
	if res.Version != "1.1.0" || len(res.Instances) != 2 {
		t.Fatalf("update = %+v", res)
	}
}
