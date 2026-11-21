package cli

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/version"
	"github.com/npjonath/codi/spec"
)

//go:embed all:templates
var templates embed.FS

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// engineRange returns the default engine constraint for new repos/plugins: the current minor.
func engineRange() string {
	var major, minor int
	fmt.Sscanf(version.Version, "%d.%d", &major, &minor)
	if major == 0 {
		return fmt.Sprintf(">=0.%d.0 <0.%d.0", minor, minor+1)
	}
	return fmt.Sprintf(">=%d.0.0 <%d.0.0", major, major+1)
}

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
		Short: "Scaffold a new CODI repository",
		Example: `  codi init my-community --server-id 112233445566778899 --application-id 998877665544332211
  codi init . --server my-server --bot helper`,
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
			fmt.Fprintf(w, "\nNext steps:\n  cd %s\n  codi validate\n  codi dev --sim\n", dir)
			if o.serverID == "000000000000000000" || o.appID == "000000000000000000" {
				fmt.Fprintln(w, "\nReplace the placeholder discordId / applicationId with your real IDs before `codi plan`.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.project, "project", "", "project key (default: directory name)")
	f.StringVar(&o.server, "server", "", "server key (default: project key)")
	f.StringVar(&o.serverID, "server-id", "000000000000000000", "Discord server ID")
	f.StringVar(&o.bot, "bot", "bot", "bot key")
	f.StringVar(&o.appID, "application-id", "000000000000000000", "Discord application ID of the bot")
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
		out = "codi-" + out
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Create and manage plugins",
	}
	var force bool
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
			for _, f := range files {
				rel, _ := filepath.Rel(abs, f)
				fmt.Fprintln(cmd.OutOrStdout(), "  created", rel)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  created .codi/codi-sdk.d.ts")
			fmt.Fprintf(cmd.OutOrStdout(), "\nAdd it to a bot:\n  plugins:\n    - name: %s\n      source: <relative path to %s>\n      grants: [discord.messages.send]\n", name, args[0])
			return nil
		},
	}
	newCmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	sdkCmd := &cobra.Command{
		Use:   "sdk-types <plugin-dir>",
		Short: "Write the @codi/sdk type definitions into <plugin-dir>/.codi for editors and tsc",
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

// pluginSubcommands lets other files add `codi plugin …` commands (build, test, typegen…).
var pluginSubcommands []func() *cobra.Command

func writeSDKTypes(pluginDir string) error {
	dir := filepath.Join(pluginDir, ".codi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "codi-sdk.d.ts"), []byte(spec.SDKTypes), 0o644)
}
