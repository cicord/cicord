package writeback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/npjonath/codi/internal/config"
)

const botYAML = `# The moderator bot
apiVersion: codi.dev/v1alpha1
kind: Bot
metadata:
  name: moderator
spec:
  applicationId: "998877665544332211"
  token:
    secretRef: DISCORD_TOKEN_MODERATOR # never commit tokens
  servers: [ma-commu]
  plugins:
    - name: welcome
      source: ../../plugins/welcome
      config:
        channel: general # where to greet
        message: "Bienvenue !"
`

func TestSetYAMLPreservesComments(t *testing.T) {
	out, err := PluginConfigEdit("welcome", "", map[string]any{"channel": "general", "message": "Hello", "autoRole": "member"})([]byte(botYAML))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# The moderator bot", "# never commit tokens", "channel: general # where to greet", `message: "Hello"`, "autoRole: member"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	// server override, then removal
	out, err = PluginConfigEdit("welcome", "ma-commu", map[string]any{"message": "Salut"})(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "overrides:\n        ma-commu:\n          config:\n            message: Salut") {
		t.Errorf("override not written:\n%s", out)
	}
	out, err = PluginConfigEdit("welcome", "ma-commu", nil)(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "overrides") {
		t.Errorf("empty overrides should be removed:\n%s", out)
	}
	if _, err := PluginConfigEdit("ghost", "", map[string]any{})([]byte(botYAML)); err == nil {
		t.Errorf("unknown instance should fail")
	}
}

func TestDiffMergePatch(t *testing.T) {
	from := map[string]any{"a": 1.0, "b": "x", "n": map[string]any{"p": 1.0, "q": 2.0}}
	to := map[string]any{"a": 1.0, "b": "y", "n": map[string]any{"p": 1.0}, "c": true}
	p := DiffMergePatch(from, to)
	if p["b"] != "y" || p["c"] != true || p["a"] != nil {
		t.Errorf("patch = %v", p)
	}
	if n := p["n"].(map[string]any); len(n) != 1 || n["q"] != nil {
		t.Errorf("nested = %v", n)
	}
	if _, has := p["n"].(map[string]any)["q"]; !has {
		t.Errorf("removed key must be null")
	}
	if DiffMergePatch(from, from) != nil {
		t.Errorf("equal maps must give nil")
	}
}

func TestModeFor(t *testing.T) {
	wb := &config.WriteBack{DefaultMode: "pullRequest", Policies: []config.WriteBackPolicy{
		{Paths: []string{"bots/**"}, Mode: "commit"},
		{Paths: []string{"servers/*.yaml"}, Mode: "pullRequest"},
	}}
	cases := map[string]string{
		"bots/moderator/bot.yaml": "commit",
		"servers/a.yaml":          "pullRequest",
		"codi.yaml":               "pullRequest",
	}
	for p, want := range cases {
		if got := ModeFor(wb, []string{p}); got != want {
			t.Errorf("%s: %s, want %s", p, got, want)
		}
	}
	if got := ModeFor(wb, []string{"bots/x/bot.yaml", "servers/a.yaml"}); got != "pullRequest" {
		t.Errorf("strictest mode should win, got %s", got)
	}
	if got := ModeFor(nil, []string{"bots/x"}); got != "pullRequest" {
		t.Errorf("nil policy default = %s", got)
	}
}

func TestGitWriter(t *testing.T) {
	upstream := t.TempDir()
	repo, err := git.PlainInitWithOptions(upstream, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	p := filepath.Join(upstream, "bots/moderator/bot.yaml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(botYAML), 0o644)
	wt.Add("bots/moderator/bot.yaml")
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	// upstream is non-bare with main checked out: pushing to main would be refused, so
	// push to a bare clone instead.
	bare := t.TempDir()
	if _, err := git.PlainClone(bare, true, &git.CloneOptions{URL: upstream}); err != nil {
		t.Fatal(err)
	}

	w := NewGitWriter(GitOptions{URL: bare, Branch: "main", WorkDir: filepath.Join(t.TempDir(), "wc")})
	res, err := w.Write(context.Background(), Request{
		Edits:   map[string]EditFunc{"bots/moderator/bot.yaml": PluginConfigEdit("welcome", "", map[string]any{"channel": "general", "message": "Yo"})},
		Message: "config(welcome): update message",
		Actor:   "discord:1 (Alice)",
		Source:  "admin-ui",
		Mode:    ModeCommit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Revision) != 7 {
		t.Fatalf("result = %+v", res)
	}
	br, _ := git.PlainOpen(bare)
	ref, _ := br.Reference("refs/heads/main", true)
	c, _ := br.CommitObject(ref.Hash())
	if !strings.Contains(c.Message, "Codi-Actor: discord:1 (Alice)") || c.Author.Name != "codi[bot]" {
		t.Errorf("commit = %q by %s", c.Message, c.Author.Name)
	}
	f, _ := c.File("bots/moderator/bot.yaml")
	content, _ := f.Contents()
	if !strings.Contains(content, `message: "Yo"`) || !strings.Contains(content, "# never commit tokens") {
		t.Errorf("content:\n%s", content)
	}

	// PR mode without GitHub: pushes a branch, main unchanged
	res2, err := w.Write(context.Background(), Request{
		Edits:   map[string]EditFunc{"bots/moderator/bot.yaml": PluginConfigEdit("welcome", "", map[string]any{"channel": "general", "message": "PR"})},
		Message: "config: via PR", Mode: ModePullRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res2.Branch, "codi/change-") {
		t.Errorf("branch = %q", res2.Branch)
	}
	ref2, _ := br.Reference("refs/heads/main", true)
	if ref2.Hash() != ref.Hash() {
		t.Errorf("PR mode must not move main")
	}

	// A nil edit result deletes the file.
	if _, err := w.Write(context.Background(), Request{
		Edits:   map[string]EditFunc{"bots/moderator/bot.yaml": func([]byte) ([]byte, error) { return nil, nil }, "missing.txt": func([]byte) ([]byte, error) { return nil, nil }},
		Message: "remove", Mode: ModeCommit,
	}); err != nil {
		t.Fatal(err)
	}
	ref3, _ := br.Reference("refs/heads/main", true)
	c3, _ := br.CommitObject(ref3.Hash())
	if _, err := c3.File("bots/moderator/bot.yaml"); err == nil {
		t.Error("bot.yaml was not deleted")
	}
}

func TestDirWriter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bots/moderator/bot.yaml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(botYAML), 0o644)
	w := &DirWriter{Root: dir}
	if _, err := w.Write(context.Background(), Request{Edits: map[string]EditFunc{
		"bots/moderator/bot.yaml": PluginConfigEdit("welcome", "", map[string]any{"channel": "x"}),
	}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "channel: x") {
		t.Errorf("not written:\n%s", data)
	}
	if _, err := w.Write(context.Background(), Request{Edits: map[string]EditFunc{"bots/moderator/bot.yaml": func([]byte) ([]byte, error) { return nil, nil }}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("bot.yaml not deleted: %v", err)
	}
	if _, err := w.Write(context.Background(), Request{Edits: map[string]EditFunc{"../evil": func([]byte) ([]byte, error) { return nil, nil }}}); err == nil {
		t.Errorf("path escape must fail")
	}
}

func TestEmojiPreserved(t *testing.T) {
	src := "spec:\n  plugins:\n    - name: welcome\n      config:\n        message: \"Bienvenue ! 👋\"\n        emoji: \"🎯\"\n        raw: \"a\\\\U0001F3AF\"\n"
	out, err := PluginConfigEdit("welcome", "", map[string]any{"message": "Bienvenue ! 👋", "emoji": "🎯", "raw": `a\U0001F3AF`, "x": "🛡️"})([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`message: "Bienvenue ! 👋"`, `emoji: "🎯"`, `raw: "a\\U0001F3AF"`, "🛡️"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `\U0001F44B`) {
		t.Errorf("emoji escaped:\n%s", s)
	}
}

func TestEmojiUnescapeNeverChangesData(t *testing.T) {
	// A literal \U sequence in a comment and in single-quoted / plain scalars must survive.
	src := "spec: # see \\U0001F3AF docs\n  plugins:\n    - name: welcome\n      config:\n        a: 'lit \\U0001F3AF'\n        b: plain \\U0001F3AF\n        c: \"👋\"\n"
	out, err := PluginConfigEdit("welcome", "", map[string]any{"a": `lit \U0001F3AF`, "b": `plain \U0001F3AF`, "c": "👋", "d": "🎯"})([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Spec struct {
			Plugins []struct {
				Config map[string]string `yaml:"config"`
			} `yaml:"plugins"`
		} `yaml:"spec"`
	}
	if err := yamlUnmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	cfg := got.Spec.Plugins[0].Config
	if cfg["a"] != `lit \U0001F3AF` || cfg["b"] != `plain \U0001F3AF` || cfg["c"] != "👋" || cfg["d"] != "🎯" {
		t.Fatalf("data changed: %v\n%s", cfg, out)
	}
	if !strings.Contains(string(out), `# see \U0001F3AF docs`) {
		t.Errorf("comment changed:\n%s", out)
	}
}
