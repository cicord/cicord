package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/auth"
)

// The simulator performs the guild ban. Only Discord's external identity provider
// is replaced: login still passes the state cookie and exchanges the code.
func TestCaseOAuthAfterBan(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": r.Form.Get("code")})
		case "/users/@me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "username": "appellant"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	e := startFull(t, func(dir string) {
		patch := func(path, from, to string) {
			p := filepath.Join(dir, path)
			b, err := os.ReadFile(p)
			if err != nil || !strings.Contains(string(b), from) {
				t.Fatal(path, err)
			}
			writeTestFile(t, p, strings.Replace(string(b), from, to, 1))
		}
		patch("plugins/welcome/plugin.yaml", "\n  capabilities:\n", "\n  capabilities:\n    - portal.routes\n")
		patch("bots/moderator/bot.yaml", "        - portal.links", "        - portal.routes\n        - portal.links")
		patch("plugins/welcome/src/index.ts", "  events: {", `  routes:{
    "GET /issue/:id":{access:"admin",async handler(ctx,r){return {url:await ctx.portal.link("/case",{scope:{kind:"case",id:r.params.id},user:r.query.user,expiresInSeconds:604800})}}},
    "POST /scope/renew":{access:"scoped",handler(ctx,r){return {id:r.scope.id}}},
    "GET /case/:id":{access:"scoped",handler(ctx,r){return {id:r.scope.id,user:r.viewer.user.id}}},
    "POST /appeals":{access:"scoped",handler(ctx,r){return {id:r.scope.id}}},
    "POST /invalidate/:id":{access:"admin",async handler(ctx,r){await ctx.portal.revoke({kind:"case",id:r.params.id})}},
  }, events: {`)
	}, nil, func(o *Options) {
		o.OAuth = OAuth{ClientID: "42", ClientSecret: "test", TokenURL: provider.URL + "/token", APIURL: provider.URL}
	})
	user := e.seed.Users[0].ID
	if err := e.world.AddMember(e.serverID(), user); err != nil {
		t.Fatal(err)
	}
	admin := e.client()
	admin.bearer = testToken
	path := "/api/v1/portal/" + serverKey + "/plugins/moderator/welcome"
	var link struct{ URL string }
	admin.expect(200, "GET", path+"/issue/42?user="+user, nil).json(t, &link)
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("cicord_scope")
	if token == "" {
		t.Fatal("missing case credential")
	}
	if err := e.world.AdminBanMember(e.serverID(), user); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	c.expect(403, "POST", "/auth/link", map[string]string{"token": token})
	login := func(c *client, account string, want int) {
		loc := c.expect(302, "GET", "/auth/login?case_token="+url.QueryEscape(token), nil).header.Get("Location")
		l, err := url.Parse(loc)
		if err != nil {
			t.Fatal(err)
		}
		c.expect(want, "GET", "/auth/callback?state="+l.Query().Get("state")+"&code="+account, nil)
	}
	login(e.client(), e.seed.Users[1].ID, 403)
	login(c, user, 302)
	var reply struct{ ID, User string }
	c.expect(200, "GET", path+"/case/42", nil).json(t, &reply)
	if reply.ID != "42" || reply.User != user {
		t.Fatalf("unexpected case: %+v", reply)
	}
	c.expect(200, "POST", path+"/appeals", map[string]string{"case": "43"}).json(t, &reply)
	if reply.ID != "42" {
		t.Fatal("body changed authorized case")
	}
	c.expect(403, "GET", path+"/case/43", nil)
	c.expect(403, "GET", "/api/v1/portal/"+serverKey+"/me", nil)
	admin.expect(403, "GET", path+"/case/42", nil)
	c.expect(403, "GET", path+"/issue/43", nil)
	// Expired proof can start an OAuth renewal challenge, never a normal case session.
	claim, err := e.rt.Signer().Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	claim.Expires = time.Now().Add(-time.Hour).Unix()
	stale := e.rt.Signer().Sign(claim)
	renew := e.client()
	dest := renew.expect(302, "GET", "/auth/login?case_token="+url.QueryEscape(stale)+"&case_renew=1", nil).header.Get("Location")
	target, err := url.Parse(dest)
	if err != nil {
		t.Fatal(err)
	}
	renew.expect(302, "GET", "/auth/callback?state="+target.Query().Get("state")+"&code="+user, nil)
	renew.expect(403, "GET", path+"/case/42", nil)
	renew.expect(403, "POST", path+"/appeals", map[string]string{"text": "test"})
	renew.expect(200, "POST", path+"/scope/renew", nil)
	admin.expect(204, "POST", path+"/invalidate/42", nil)
	c.expect(403, "GET", path+"/case/42", nil)
	c.expect(403, "GET", "/auth/login?case_token="+url.QueryEscape(token), nil)
	expired := e.rt.Signer().Sign(auth.LinkClaims{UserID: user, Server: serverKey, Expires: time.Now().Unix(), Scope: &auth.ScopedGrant{ResourceScope: auth.ResourceScope{Kind: "case", ID: "42"}}})
	c.expect(401, "GET", "/auth/login?case_token="+url.QueryEscape(expired), nil)
}
