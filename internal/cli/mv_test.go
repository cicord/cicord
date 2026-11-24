package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/state"
	"github.com/npjonath/cicord/internal/state/sqlite"
)

func TestMvRewritesReferences(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	srvFile := filepath.Join(dir, "servers", "ma-commu.yaml")
	botFile := filepath.Join(dir, "bots", "moderator", "bot.yaml")
	srvBefore, botBefore := readFile(t, srvFile), readFile(t, botFile)

	// a state database with the mapping of the role
	statePath := filepath.Join(dir, ".cicord", "state.db")
	writeFile(t, filepath.Join(dir, ".cicord", ".keep"), "")
	store := must[*sqlite.Store](t)(sqlite.Open(statePath))
	ctx := context.Background()
	store.Resources().Put(ctx, state.Resource{Server: "ma-commu", Kind: state.KindRole, Key: "modo", ID: "111111111111111111", Owner: state.OwnerGit})
	store.Close()

	// dry run: nothing written
	code, out, errOut := run(t, "mv", "ma-commu", "role", "modo", "moderation", dir, "--dry-run")
	if code != 0 || !strings.Contains(out, "Dry run") {
		t.Fatalf("dry run = %d\n%s\n%s", code, out, errOut)
	}
	if readFile(t, srvFile) != srvBefore {
		t.Fatal("dry run wrote the server file")
	}

	// role: key, hierarchy, access, overwrite target (the bot config does not reference it)
	code, out, errOut = run(t, "mv", "ma-commu", "role", "modo", "moderation", dir)
	if code != 0 {
		t.Fatalf("mv role = %d\n%s\n%s", code, out, errOut)
	}
	srv := readFile(t, srvFile)
	want := strings.NewReplacer(
		"    modo:\n      name: Modo", "    moderation:\n      name: Modo",
		"hierarchy: [admin, modo,", "hierarchy: [admin, moderation,",
		"        modo:\n          allow: [ViewChannel]", "        moderation:\n          allow: [ViewChannel]",
		"      roles: [modo]", "      roles: [moderation]",
	).Replace(srvBefore)
	if srv != want {
		t.Errorf("server YAML after mv role:\n%s", srv)
	}
	store = must[*sqlite.Store](t)(sqlite.Open(statePath))
	r, err := store.Resources().Get(ctx, "ma-commu", state.KindRole, "moderation")
	_, oldErr := store.Resources().Get(ctx, "ma-commu", state.KindRole, "modo")
	store.Close()
	if err != nil || r.ID != "111111111111111111" || oldErr == nil {
		t.Errorf("state mapping: %+v %v, old still there: %v", r, err, oldErr == nil)
	}

	// role referenced by plugin configs through x-cicord-ref (onboarding steps, nested in arrays)
	code, out, errOut = run(t, "mv", "ma-commu", "role", "minecraft", "mc", dir)
	if code != 0 {
		t.Fatalf("mv minecraft = %d\n%s\n%s", code, out, errOut)
	}
	bot := readFile(t, botFile)
	if bot != strings.Replace(botBefore, "              - role: minecraft\n", "              - role: mc\n", 1) {
		t.Errorf("bot.yaml after mv role minecraft:\n%s", bot)
	}
	if !strings.Contains(out, "plugin onboarding config.steps[0].options[0].role") {
		t.Errorf("mv output: %s", out)
	}

	// channel: key, profile.systemChannel, welcome's config (base) — emojis and comments kept
	botBefore = readFile(t, botFile)
	srvBefore = readFile(t, srvFile)
	code, out, errOut = run(t, "mv", "ma-commu", "channel", "general", "discussions", dir)
	if code != 0 {
		t.Fatalf("mv channel = %d\n%s\n%s", code, out, errOut)
	}
	if got := readFile(t, botFile); got != strings.Replace(botBefore, "        channel: general\n", "        channel: discussions\n", 1) {
		t.Errorf("bot.yaml after mv channel:\n%s", got)
	}
	if got := readFile(t, srvFile); got != strings.NewReplacer(
		"    systemChannel: general", "    systemChannel: discussions",
		"    general:\n      name: général", "    discussions:\n      name: général",
	).Replace(srvBefore) {
		t.Errorf("server YAML after mv channel:\n%s", got)
	}

	// category: channel references
	srvBefore = readFile(t, srvFile)
	if code, out, errOut := run(t, "mv", "ma-commu", "categories", "info", "infos", dir); code != 0 {
		t.Fatalf("mv category = %d\n%s\n%s", code, out, errOut)
	}
	if got := readFile(t, srvFile); got != strings.NewReplacer(
		"    info:\n      name: Infos", "    infos:\n      name: Infos",
		"      category: info\n", "      category: infos\n",
	).Replace(srvBefore) {
		t.Errorf("server YAML after mv category:\n%s", got)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate after mv = %d: %s", code, errOut)
	}

	// errors
	for name, args := range map[string][]string{
		"unknown key":  {"mv", "ma-commu", "role", "nope", "x", dir},
		"existing key": {"mv", "ma-commu", "role", "admin", "mc", dir},
		"shared ns":    {"mv", "ma-commu", "channel", "rules", "staff", dir},
		"invalid key":  {"mv", "ma-commu", "role", "admin", "Admin!", dir},
		"unknown kind": {"mv", "ma-commu", "emoji", "a", "b", dir},
		"unknown srv":  {"mv", "nope", "role", "admin", "b", dir},
	} {
		if code, _, _ := run(t, args...); code != 1 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}

func TestMvSharedBaseConfigWarns(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	// a second server for the same bot: welcome's base config is shared
	srv := readFile(t, filepath.Join(dir, "servers", "ma-commu.yaml"))
	srv = strings.Replace(srv, "name: ma-commu", "name: other", 1)
	srv = strings.Replace(srv, `discordId: "112233445566778899"`, `discordId: "112233445566778800"`, 1)
	writeFile(t, filepath.Join(dir, "servers", "other.yaml"), srv)
	bf := filepath.Join(dir, "bots", "moderator", "bot.yaml")
	writeFile(t, bf, strings.Replace(readFile(t, bf), "servers: [ma-commu]", "servers: [ma-commu, other]", 1))
	before, srvBefore := readFile(t, bf), readFile(t, filepath.Join(dir, "servers", "ma-commu.yaml"))
	code, _, errOut := run(t, "mv", "ma-commu", "channel", "general", "discussions", dir)
	if code != 1 || !strings.Contains(errOut, "plugin welcome also runs on other") || !strings.Contains(errOut, "nothing written") {
		t.Errorf("shared base config = %d: %s", code, errOut)
	}
	if readFile(t, bf) != before || readFile(t, filepath.Join(dir, "servers", "ma-commu.yaml")) != srvBefore {
		t.Error("files written although the rename was refused")
	}
	// --force renames the server key and leaves the shared config: validation then fails
	code, _, errOut = run(t, "mv", "ma-commu", "channel", "general", "discussions", dir, "--force")
	if code != 1 || readFile(t, bf) != before || readFile(t, filepath.Join(dir, "servers", "ma-commu.yaml")) == srvBefore {
		t.Errorf("--force = %d: %s", code, errOut)
	}
}
