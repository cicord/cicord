package api

import (
	"strings"
	"testing"
)

func TestMemberPrivacyAdministration(t *testing.T) {
	e := start(t)
	path := "/api/v1/servers/" + serverKey + "/privacy/"
	body := map[string]string{"userId": "1234567812345678"}
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	for _, action := range []string{"export", "erase"} {
		mod.expect(403, "POST", path+action, body)
	}
	admin.noCSRF = true
	admin.expect(403, "POST", path+"erase", body)
	admin.noCSRF = false
	admin.expect(400, "POST", path+"erase", map[string]string{"userId": "not-an-id"})
	admin.expect(404, "POST", path+"typo", body)
	for _, action := range []string{"export", "erase"} {
		resp := admin.expect(200, "POST", path+action, body)
		if !strings.Contains(resp.header.Get("Cache-Control"), "no-store") || !strings.Contains(string(resp.body), "moderator/welcome") {
			t.Fatal(string(resp.body), resp.header)
		}
	}
	audit := admin.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil)
	if strings.Contains(string(audit.body), body["userId"]) || !strings.Contains(string(audit.body), "privacy.erase") {
		t.Fatal(string(audit.body))
	}
}
