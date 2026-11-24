package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"

	"github.com/cicord/cicord/internal/discord"
)

// fakeDiscord is a minimal Discord REST API for the probe.
func fakeDiscord(t *testing.T, token string, flags int, guilds []map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bot "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message": "401: Unauthorized", "code": 0}`))
			return false
		}
		return true
	}
	mux.HandleFunc("GET /users/@me", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "111", "username": "Gardien", "avatar": "abc", "bot": true})
		}
	})
	mux.HandleFunc("GET /oauth2/applications/@me", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "999", "name": "Gardien app", "flags": flags})
		}
	})
	mux.HandleFunc("GET /users/@me/guilds", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_ = json.NewEncoder(w).Encode(guilds)
		}
	})
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "999" || p != "the-right-client-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"x","token_type":"Bearer"}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTPProbe(t *testing.T) {
	ctx := context.Background()
	tok := "MTIz.fake.token-for-tests"
	ts := fakeDiscord(t, tok, FlagGatewayGuildMembersLimited|FlagGatewayMessageContent, []map[string]any{{"id": "42", "name": "Ma Commu", "icon": "ic"}})
	p := &HTTPProbe{BaseURL: ts.URL}
	id, err := p.Identify(ctx, "Bot "+tok+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if id.ApplicationID != "999" || id.BotUsername != "Gardien" || !strings.HasSuffix(id.BotAvatarURL, "/avatars/111/abc.png") {
		t.Fatalf("identity = %+v", id)
	}
	in := IntentsFromFlags(id.Flags)
	if !in.Has(discord.MustIntents("ServerMembers", "MessageContent")) || in.Has(discord.MustIntents("ServerPresences")) {
		t.Fatalf("intents = %v (LIMITED flags count as enabled)", in.Names())
	}
	if _, err := p.Identify(ctx, "wrong.token.value-xxxxxxxx"); !errors.Is(err, discord.ErrInvalidToken) {
		t.Fatalf("bad token: %v", err)
	}
	ss, err := p.Servers(ctx, tok)
	if err != nil || len(ss) != 1 || ss[0].ID != "42" || !strings.Contains(ss[0].IconURL, "/icons/42/ic.png") {
		t.Fatalf("servers = %+v %v", ss, err)
	}
	if err := p.ValidateClientSecret(ctx, "999", "the-right-client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateClientSecret(ctx, "999", "nope"); !errors.Is(err, discord.ErrInvalidToken) {
		t.Fatalf("bad client secret: %v", err)
	}
}

func TestIntentsCheck(t *testing.T) {
	c := IntentsCheck("999", 0, 0, RecommendedIntents)
	if c.Status != StatusWarn || !strings.Contains(c.Hint, "“Message Content Intent” and “Server Members Intent”") ||
		c.Link != "https://discord.com/developers/applications/999/bot" {
		t.Fatalf("warn = %+v", c)
	}
	c = IntentsCheck("999", FlagGatewayGuildMembers, discord.MustIntents("MessageContent"), RecommendedIntents)
	if c.Status != StatusFail || !strings.Contains(c.Message, "Message Content Intent") || strings.Contains(c.Message, "Server Members") {
		t.Fatalf("fail = %+v", c)
	}
	if c := IntentsCheck("999", FlagsFromIntents(discord.PrivilegedIntents), 0, RecommendedIntents); c.Status != StatusOK {
		t.Fatalf("ok = %+v", c)
	}
}

func TestPermissionsAndHierarchy(t *testing.T) {
	const sid = "1"
	roles := []discord.Role{
		{ID: sid, Name: "@everyone", Position: 0, Permissions: discord.MustPermissions("ViewChannel")},
		{ID: "10", Name: "Member", Position: 1},
		{ID: "11", Name: "Cicord", Position: 2, Managed: true, Permissions: discord.MustPermissions("ManageRoles", "ManageChannels")},
		{ID: "12", Name: "Moderator", Position: 3},
		{ID: "13", Name: "Admin", Position: 4},
		{ID: "14", Name: "OtherBot", Position: 5, Managed: true},
	}
	bot := discord.Member{User: discord.User{ID: "b"}, RoleIDs: []string{"11"}}
	h := HierarchyCheck(sid, roles, bot, "Cicord")
	if h.Status != StatusFail || !strings.Contains(h.Hint, "drag the role “Cicord” above “Admin”") || !strings.Contains(h.Message, "below 2 role(s)") {
		t.Fatalf("hierarchy = %+v", h)
	}
	p := PermissionsCheck(sid, roles, bot, "owner")
	if p.Status != StatusFail || !strings.Contains(p.Message, "ManageServer") || strings.Contains(p.Message, "ManageRoles,") {
		t.Fatalf("permissions = %+v", p)
	}
	roles[2].Position, roles[4].Position, roles[3].Position = 4, 2, 3
	roles[2].Permissions = discord.MustPermissions("Administrator")
	if h := HierarchyCheck(sid, roles, bot, "Cicord"); h.Status != StatusOK {
		t.Fatalf("hierarchy ok = %+v", h)
	}
	if p := PermissionsCheck(sid, roles, bot, "owner"); p.Status != StatusOK || !strings.Contains(p.Message, "Administrator") {
		t.Fatalf("permissions ok = %+v", p)
	}
}

func TestInitRepositoryGitHubPath(t *testing.T) {
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	empty, hasCicord, err := CheckRemote(ctx, remote, "main", "")
	if err != nil || !empty || hasCicord {
		t.Fatalf("empty remote: %v %v %v", empty, hasCicord, err)
	}
	files, err := RepoFiles{Project: "ma-commu", ServerKey: "ma-commu", ServerYAML: []byte("x: 1\n"), BotKey: "cicord",
		ApplicationID: "999", SecretRef: "DISCORD_TOKEN_CICORD", Repo: RepoConfig{Provider: ProviderGitHub, URL: "https://github.com/me/commu", Branch: "main"},
		PublicURL: "https://cicord.example.org"}.Generate()
	if err != nil {
		t.Fatal(err)
	}
	proj := string(files["cicord.yaml"])
	for _, want := range []string{"provider: github", "repo: https://github.com/me/commu", `paths: ["bots/**", "plugins/**"]`, "url: https://cicord.example.org"} {
		if !strings.Contains(proj, want) {
			t.Fatalf("cicord.yaml lacks %q:\n%s", want, proj)
		}
	}
	if !strings.Contains(string(files["bots/cicord/bot.yaml"]), "intents: [ServerMessages]") {
		t.Fatalf("bot.yaml = %s", files["bots/cicord/bot.yaml"])
	}
	sha, err := InitRepository(ctx, RepoConfig{Provider: ProviderGitHub, URL: remote, Branch: "main"}, "", files, "Set up Cicord")
	if err != nil || sha == "" {
		t.Fatalf("init: %q %v", sha, err)
	}
	_, hasCicord, err = CheckRemote(ctx, remote, "main", "")
	if err != nil || !hasCicord {
		t.Fatalf("after push: %v %v", hasCicord, err)
	}
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderGitHub, URL: remote, Branch: "main"}, "", files, "again"); err == nil ||
		!strings.Contains(err.Error(), "already contains a Cicord configuration") {
		t.Fatalf("second init: %v", err)
	}
	// A non-empty repository (README on main) gets the configuration next to it.
	withReadme := filepath.Join(t.TempDir(), "readme.git")
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderEmbedded, Dir: withReadme, Branch: "main"}, "",
		map[string][]byte{"README.md": []byte("# hello\n")}, "Initial commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderGitHub, URL: withReadme, Branch: "main"}, "", files, "Set up Cicord"); err != nil {
		t.Fatal(err)
	}
	r, _ := git.PlainOpen(withReadme)
	head, _ := r.Reference("refs/heads/main", true)
	cm, _ := r.CommitObject(head.Hash())
	if cm.NumParents() != 1 {
		t.Fatalf("setup commit should build on the README commit (parents = %d)", cm.NumParents())
	}
	if _, err := cm.File("cicord.yaml"); err != nil {
		t.Fatal(err)
	}
	if f, err := cm.File("README.md"); err != nil {
		t.Fatal(err)
	} else if c, _ := f.Contents(); c != "# hello\n" {
		t.Fatalf("the existing README was overwritten: %q", c)
	}

	// Embedded: refuses an existing repository; local: refuses an existing cicord.yaml.
	bare := filepath.Join(t.TempDir(), "repo.git")
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderEmbedded, Dir: bare}, "", files, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderEmbedded, Dir: bare}, "", files, "x"); err == nil {
		t.Fatal("embedded overwrite")
	}
	local := t.TempDir()
	_ = os.WriteFile(filepath.Join(local, "README.md"), []byte("mine\n"), 0o644)
	_ = os.WriteFile(filepath.Join(local, ".gitignore"), []byte("*.log\n.env\n"), 0o644)
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderLocal, Dir: local}, "", files, "x"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "README.md")); string(b) != "mine\n" {
		t.Fatalf("README overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(local, ".gitignore")); !strings.HasPrefix(string(b), "*.log\n.env\n# Cicord\n") || strings.Count(string(b), ".env") != 1 {
		t.Fatalf(".gitignore = %q", b)
	}
	if _, err := InitRepository(ctx, RepoConfig{Provider: ProviderLocal, Dir: local}, "", files, "x"); err == nil {
		t.Fatal("local overwrite")
	}
}

func TestSetupURLAndInvite(t *testing.T) {
	if u := SetupURL("http://localhost:7493/", "a b"); u != "http://localhost:7493/admin/setup?token=a+b" {
		t.Fatal(u)
	}
	u := InviteURL("999")
	for _, want := range []string{"client_id=999", "permissions=8", "scope=bot+applications.commands"} {
		if !strings.Contains(u, want) {
			t.Fatalf("invite %s lacks %s", u, want)
		}
	}
}
