package cli

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/version"
	"github.com/cicord/cicord/spec"
)

//go:embed all:templates
var templates embed.FS

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// engineRange returns the default engine constraint for new repos/plugins: the current minor.
func engineRange() string { return version.EngineRange(version.Version) }

// renderTree renders every *.tmpl under templates/<sub> into dst. rename maps template
// paths (relative to sub, without .tmpl) to output paths.
func renderTree(sub, dst string, data any, rename func(string) string, force bool) ([]string, error) {
	root := "templates/" + sub
	var written []string
	err := fs.WalkDir(templates, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
		if rename != nil {
			rel = rename(rel)
		}
		raw, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		t, err := template.New(rel).Option("missingkey=error").Parse(string(raw))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))
		if _, err := os.Stat(out); err == nil && !force {
			return fmt.Errorf("%s already exists (use --force to overwrite)", out)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			return err
		}
		written = append(written, out)
		return nil
	})
	return written, err
}

func newInitCmd() *cobra.Command {
	var o struct {
		project, server, serverID, bot, appID, repo, provider string
		force                                                 bool
	}
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new Cicord repository",
		Example: `  cicord init my-community --server-id 112233445566778899 --application-id 998877665544332211
  cicord init . --server my-server --bot helper`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			if o.project == "" {
				o.project = sanitizeKey(filepath.Base(abs))
			}
			if o.server == "" {
				o.server = o.project
			}
			for name, v := range map[string]string{"--project": o.project, "--server": o.server, "--bot": o.bot} {
				if !keyRe.MatchString(v) {
					return fmt.Errorf("%s %q is not a valid key (lowercase letters, digits and dashes)", name, v)
				}
			}
			if o.provider == "" && o.repo != "" {
				o.provider = "git"
				if strings.Contains(o.repo, "github.com") {
					o.provider = "github"
				}
			}
			data := map[string]any{
				"Project":       o.project,
				"Server":        o.server,
				"ServerID":      o.serverID,
				"Bot":           o.bot,
				"ApplicationID": o.appID,
				"TokenEnv":      "DISCORD_TOKEN_" + strings.ToUpper(strings.ReplaceAll(o.bot, "-", "_")),
				"EngineRange":   engineRange(),
				"Provider":      o.provider,
				"Repo":          o.repo,
			}
			files, err := renderTree("repo", abs, data, func(rel string) string {
				switch rel {
				case "servers/server.yaml":
					return "servers/" + o.server + ".yaml"
				case "bots/bot/bot.yaml":
					return "bots/" + o.bot + "/bot.yaml"
				case "gitignore":
					return ".gitignore"
				}
				return rel
			}, o.force)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, f := range files {
				rel, _ := filepath.Rel(abs, f)
				fmt.Fprintln(w, "  created", rel)
			}
			fmt.Fprintf(w, "\nNext steps:\n  cd %s\n  cicord validate\n  cicord dev --sim\n", dir)
			if o.serverID == placeholderServerID || o.appID == placeholderAppID {
				fmt.Fprintln(w, "\nReplace the placeholder discordId / applicationId with your real IDs before `cicord plan`.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.project, "project", "", "project key (default: directory name)")
	f.StringVar(&o.server, "server", "", "server key (default: project key)")
	f.StringVar(&o.serverID, "server-id", placeholderServerID, "Discord server ID (default: a placeholder that works with cicord dev --sim)")
	f.StringVar(&o.bot, "bot", "bot", "bot key")
	f.StringVar(&o.appID, "application-id", placeholderAppID, "Discord application ID of the bot (default: a placeholder that works with cicord dev --sim)")
	f.StringVar(&o.provider, "provider", "", "source provider: github | git | embedded (default: inferred from --repo, none for local-only repos)")
	f.StringVar(&o.repo, "repo", "", "repository URL (github / git providers)")
	f.BoolVar(&o.force, "force", false, "overwrite existing files")
	return cmd
}

func sanitizeKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = "cicord-" + out
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

// Placeholder IDs written by `cicord init` without --server-id / --application-id. They are
// valid snowflakes, so `cicord dev --sim` (which creates the server and the application with
// the IDs of the repository) works right away; they must be replaced before `cicord plan`.
const (
	placeholderServerID = "100000000000000001"
	placeholderAppID    = "100000000000000002"
)

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Create and manage plugins",
	}
	var force, fromNPM bool
	newCmd := &cobra.Command{
		Use:   "new <dir>",
		Short: "Scaffold a TypeScript plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			name := sanitizeKey(filepath.Base(abs))
			files, err := renderTree("plugin", abs, map[string]any{"Name": name, "EngineRange": engineRange()}, nil, force)
			if err != nil {
				return err
			}
			if err := writeSDKTypes(abs); err != nil {
				return err
			}
			testFiles, testErr := scaffoldPluginTests(abs, name, force, fromNPM)
			if testErr != nil && !errors.Is(testErr, errNoSDKTarball) {
				return testErr
			}
			for _, f := range append(files, testFiles...) {
				rel, _ := filepath.Rel(abs, f)
				fmt.Fprintln(cmd.OutOrStdout(), "  created", rel)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  created .cicord/cicord-sdk.d.ts")
			if testErr != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "\nNo unit test setup: %v.\n", testErr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\nUnit tests (Node 24, @cicord/sdk/testing):\n    cd %s && npm install && npm test\n", args[0])
			}
			where, source := "a bot", "<relative path from bot.yaml to "+args[0]+">"
			if botFile := findBotFile(abs); botFile != "" {
				if rel, err := filepath.Rel(filepath.Dir(botFile), abs); err == nil {
					source = filepath.ToSlash(rel)
					if !strings.HasPrefix(source, ".") {
						source = "./" + source
					}
					where = botFile
					if wd, err := os.Getwd(); err == nil {
						if r, err := filepath.Rel(wd, botFile); err == nil {
							where = r
						}
					}
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nAdd it to %s (spec.plugins), then run `cicord dev --sim`:\n    - name: %s\n      source: %s\n      grants: [discord.messages.send]\n", where, name, source)
			return nil
		},
	}
	newCmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	newCmd.Flags().BoolVar(&fromNPM, "from-npm", fromNPMDefault(), "depend on @cicord/sdk from the npm registry instead of vendoring the embedded tarball")
	sdkCmd := &cobra.Command{
		Use:   "sdk-types <plugin-dir>",
		Short: "Write the @cicord/sdk type definitions into <plugin-dir>/.cicord for editors and tsc",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeSDKTypes(args[0])
		},
	}
	cmd.AddCommand(newCmd, sdkCmd)
	for _, c := range pluginSubcommands {
		cmd.AddCommand(c())
	}
	return cmd
}

// pluginSubcommands lets other files add `cicord plugin …` commands (build, test, typegen…).
var pluginSubcommands []func() *cobra.Command

func writeSDKTypes(pluginDir string) error {
	dir := filepath.Join(pluginDir, ".cicord")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "cicord-sdk.d.ts"), []byte(spec.SDKTypes), 0o644)
}

// findBotFile returns the first bots/<key>/bot.yaml of the Cicord repository containing dir
// (the nearest parent with a cicord.yaml), or "".
func findBotFile(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "cicord.yaml")); err == nil {
			matches, _ := filepath.Glob(filepath.Join(d, "bots", "*", "bot.yaml"))
			sort.Strings(matches)
			if len(matches) > 0 {
				return matches[0]
			}
			return ""
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
