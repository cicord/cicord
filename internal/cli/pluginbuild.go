package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/npjonath/codi/internal/api"
	"github.com/npjonath/codi/internal/auth"
	"github.com/npjonath/codi/internal/build"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/plugin"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/typegen"
)

func init() {
	pluginSubcommands = append(pluginSubcommands, newPluginBuildCmd, newPluginTypegenCmd, newPluginTestCmd)
}

// readManifest decodes <dir>/plugin.yaml.
func readManifest(dir string) (*config.Plugin, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a plugin directory: %w", dir, err)
	}
	var m config.Plugin
	if err := config.DecodeYAML(data, &m); err != nil {
		return nil, fmt.Errorf("plugin.yaml: %w", err)
	}
	if m.Kind != "Plugin" {
		return nil, fmt.Errorf("plugin.yaml: expected kind Plugin, got %q", m.Kind)
	}
	if m.Spec.Entry == "" {
		return nil, errors.New("plugin.yaml: spec.entry is required")
	}
	return &m, nil
}

// ───────────────────────────────────────────────────────────── build

type optionJSON struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Choices     []any    `json:"choices,omitempty"`
	MinValue    *float64 `json:"minValue,omitempty"`
	MaxValue    *float64 `json:"maxValue,omitempty"`
}

type commandJSON struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Ephemeral   bool         `json:"ephemeral,omitempty"`
	Permissions []string     `json:"permissions,omitempty"`
	Options     []optionJSON `json:"options"`
}

type scheduleJSON struct {
	Name string `json:"name"`
	Cron string `json:"cron"`
}

type routeJSON struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Access string `json:"access"`
}

// pluginReport is the output of `codi plugin build`.
type pluginReport struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Entry           string         `json:"entry"`
	BundleBytes     int            `json:"bundleBytes"`
	Commands        []commandJSON  `json:"commands"`
	Events          []string       `json:"events"`
	Components      []string       `json:"components"`
	Modals          []string       `json:"modals"`
	Routes          []routeJSON    `json:"routes"`
	Schedules       []scheduleJSON `json:"schedules"`
	Tasks           []string       `json:"tasks"`
	Subscriptions   []string       `json:"subscriptions"`
	OnStart         bool           `json:"onStart"`
	OnStop          bool           `json:"onStop"`
	RequiredIntents []string       `json:"requiredIntents"`
	Capabilities    []string       `json:"capabilities"`
	Warnings        []string       `json:"warnings"`
	Errors          []string       `json:"errors"`

	code string
	def  *plugin.Definition
}

var optionTypeNames = map[discord.CommandOptionType]string{
	discord.OptionString: "string", discord.OptionInteger: "integer", discord.OptionBoolean: "boolean",
	discord.OptionUser: "user", discord.OptionChannel: "channel", discord.OptionRole: "role",
	discord.OptionMentionable: "mentionable", discord.OptionNumber: "number", discord.OptionAttachment: "attachment",
}

// buildPlugin bundles and inspects a plugin and checks its definition against its
// manifest, like the runtime does when it builds a revision.
func buildPluginReport(dir string) (*pluginReport, error) {
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	r := &pluginReport{
		Name: m.Metadata.Name, Version: m.Metadata.Version, Entry: m.Spec.Entry,
		Commands: []commandJSON{}, Events: []string{}, Components: []string{}, Modals: []string{},
		Routes: []routeJSON{}, Schedules: []scheduleJSON{}, Tasks: []string{}, Subscriptions: []string{},
		RequiredIntents: []string{}, Capabilities: append([]string{}, m.Spec.Capabilities...),
		Warnings: []string{}, Errors: []string{},
	}
	res, err := build.Bundle(dir, m.Spec.Entry)
	if err != nil {
		var be *build.Error
		if errors.As(err, &be) {
			for _, msg := range be.Messages {
				r.Errors = append(r.Errors, msg.String())
			}
			return r, nil
		}
		r.Errors = append(r.Errors, err.Error())
		return r, nil
	}
	r.Warnings = append(r.Warnings, res.Warnings...)
	r.BundleBytes = len(res.Code)
	r.code = res.Code
	def, err := plugin.Inspect(res.Code)
	if err != nil {
		r.Errors = append(r.Errors, "inspect: "+err.Error())
		return r, nil
	}
	r.def = def
	for _, c := range def.Commands {
		cj := commandJSON{Name: c.Name, Description: c.Description, Ephemeral: c.Ephemeral, Permissions: c.Permissions, Options: []optionJSON{}}
		for _, o := range c.Options {
			oj := optionJSON{Name: o.Name, Type: optionTypeNames[o.Type], Description: o.Description, Required: o.Required, MinValue: o.MinValue, MaxValue: o.MaxValue}
			for _, ch := range o.Choices {
				oj.Choices = append(oj.Choices, map[string]any{"name": ch.Name, "value": ch.Value})
			}
			cj.Options = append(cj.Options, oj)
		}
		r.Commands = append(r.Commands, cj)
	}
	r.Events = append(r.Events, def.Events...)
	r.Components = append(r.Components, def.Components...)
	r.Modals = append(r.Modals, def.Modals...)
	for _, rt := range def.Routes {
		r.Routes = append(r.Routes, routeJSON{Method: rt.Method, Path: rt.Path, Access: rt.Access})
	}
	for _, s := range def.Schedules {
		r.Schedules = append(r.Schedules, scheduleJSON{Name: s.Name, Cron: s.Cron})
	}
	r.Tasks = append(r.Tasks, def.Tasks...)
	r.Subscriptions = append(r.Subscriptions, def.Subscriptions...)
	r.OnStart, r.OnStop = def.HasOnStart, def.HasOnStop
	r.RequiredIntents = append(r.RequiredIntents, def.RequiredIntents()...)

	// The same manifest checks as the runtime (internal/runtime buildPlugin).
	for _, in := range def.RequiredIntents() {
		if !slices.Contains(m.Spec.Intents, in) {
			r.Errors = append(r.Errors, fmt.Sprintf("a handler needs the %s intent, which plugin.yaml does not declare (spec.intents)", in))
		}
	}
	need := func(cond bool, capability, what string) {
		if cond && !slices.Contains(m.Spec.Capabilities, capability) {
			r.Errors = append(r.Errors, fmt.Sprintf("%s requires the %s capability in plugin.yaml", what, capability))
		}
	}
	need(len(def.Schedules) > 0 || len(def.Tasks) > 0, "scheduler", "schedules/tasks")
	need(len(def.Routes) > 0, "portal.routes", "routes")
	need(len(def.Subscriptions) > 0, "events.subscribe", "subscriptions")
	for _, in := range m.Spec.Intents {
		// MessageContent is never required: it fills message.content in message events and
		// in channel.messages() history.
		readsContent := in == "MessageContent" && (def.HasEvent("messageCreate") || def.HasEvent("messageUpdate") ||
			slices.Contains(m.Spec.Capabilities, "discord.messages.read"))
		if !slices.Contains(def.RequiredIntents(), in) && !readsContent {
			r.Warnings = append(r.Warnings, fmt.Sprintf("plugin.yaml declares the %s intent, which no handler needs", in))
		}
	}
	if slices.Contains(m.Spec.Capabilities, "http.fetch") && (m.Spec.Network == nil || len(m.Spec.Network.Allow) == 0) {
		r.Errors = append(r.Errors, "http.fetch requires spec.network.allow")
	}
	// The route contract is copied into the portals (codi portal typegen).
	if _, err := readRouteContract(dir, m); err != nil {
		r.Errors = append(r.Errors, err.Error())
	} else if m.Spec.Routes != nil && m.Spec.Routes.Types != "" && len(def.Routes) == 0 {
		r.Warnings = append(r.Warnings, "plugin.yaml declares spec.routes.types but the plugin has no routes")
	}
	return r, nil
}

func printPluginReport(w io.Writer, r *pluginReport) {
	fmt.Fprintf(w, "Plugin %s %s (%s", r.Name, r.Version, r.Entry)
	if r.BundleBytes > 0 {
		fmt.Fprintf(w, ", bundle %.1f KiB", float64(r.BundleBytes)/1024)
	}
	fmt.Fprintln(w, ")")
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(w, "%s:\n", title)
		for _, it := range items {
			fmt.Fprintf(w, "  %s\n", it)
		}
	}
	var cmds []string
	for _, c := range r.Commands {
		line := "/" + c.Name
		for _, o := range c.Options {
			opt := o.Name + ":" + o.Type
			if !o.Required {
				opt = "[" + opt + "]"
			}
			line += " " + opt
		}
		if c.Description != "" {
			line += "  " + c.Description
		}
		var flags []string
		if c.Ephemeral {
			flags = append(flags, "ephemeral")
		}
		if len(c.Permissions) > 0 {
			flags = append(flags, "requires "+strings.Join(c.Permissions, ", "))
		}
		if len(flags) > 0 {
			line += " (" + strings.Join(flags, "; ") + ")"
		}
		cmds = append(cmds, line)
	}
	section("Commands", cmds)
	section("Events", r.Events)
	section("Components", r.Components)
	section("Modals", r.Modals)
	var routes []string
	for _, rt := range r.Routes {
		routes = append(routes, fmt.Sprintf("%s %s (%s)", rt.Method, rt.Path, rt.Access))
	}
	section("Routes", routes)
	var sch []string
	for _, s := range r.Schedules {
		sch = append(sch, fmt.Sprintf("%s: %s", s.Name, s.Cron))
	}
	section("Schedules", sch)
	section("Tasks", r.Tasks)
	section("Subscriptions", r.Subscriptions)
	var hooks []string
	if r.OnStart {
		hooks = append(hooks, "onStart")
	}
	if r.OnStop {
		hooks = append(hooks, "onStop")
	}
	section("Lifecycle", hooks)
	if len(r.RequiredIntents) > 0 {
		fmt.Fprintf(w, "Required intents: %s\n", strings.Join(r.RequiredIntents, ", "))
	}
	if len(r.Capabilities) > 0 {
		fmt.Fprintf(w, "Capabilities: %s\n", strings.Join(r.Capabilities, ", "))
	}
	for _, x := range r.Warnings {
		fmt.Fprintf(w, "warning: %s\n", x)
	}
	for _, x := range r.Errors {
		fmt.Fprintf(w, "error: %s\n", x)
	}
}

func newPluginBuildCmd() *cobra.Command {
	var output, out string
	cmd := &cobra.Command{
		Use:   "build <plugin-dir>",
		Short: "Bundle a plugin and print its definition (commands, events, routes…)",
		Long: `Bundle the plugin with esbuild (no Node.js needed), load it once to extract its static
definition and check it against plugin.yaml like the runtime does: handlers that need an
intent missing from spec.intents, schedules/routes/subscriptions without their
capability, http.fetch without spec.network.allow. Exit 1 when the plugin would be
rejected.`,
		Example: `  codi plugin build plugins/welcome
  codi plugin build plugins/welcome -o json
  codi plugin build plugins/welcome --out dist/welcome.js`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			r, err := buildPluginReport(args[0])
			if err != nil {
				return err
			}
			if output == "json" {
				if err := writeJSONOut(cmd.OutOrStdout(), r); err != nil {
					return err
				}
			} else {
				printPluginReport(cmd.OutOrStdout(), r)
			}
			if len(r.Errors) > 0 {
				return exitError{1}
			}
			if out != "" {
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(out, []byte(r.code), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "bundle written to %s\n", out)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	cmd.Flags().StringVar(&out, "out", "", "also write the bundle to this file")
	return cmd
}

// ───────────────────────────────────────────────────────────── typegen

func newPluginTypegenCmd() *cobra.Command {
	var out string
	var check bool
	cmd := &cobra.Command{
		Use:   "typegen <plugin-dir>",
		Short: "Generate the typed PluginConfig (src/codi-env.d.ts) from config.schema.json",
		Long: `Generate a TypeScript module augmentation of @codi/sdk's PluginConfig from the plugin's
config schema (plugin.yaml spec.config.schema), so that ctx.config is typed.

Fields that are required, or have a default the runtime applies (top level, nested
objects, object elements of arrays and local $refs; not additionalProperties maps nor
oneOf/anyOf alternatives), are non-optional. x-codi-ref fields are keys (string), enums
become literal unions.

--check compares with the existing file instead of writing it (exit 1 when it is out
of date), for CI.`,
		Example: `  codi plugin typegen plugins/welcome
  codi plugin typegen plugins/welcome --check`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			m, err := readManifest(dir)
			if err != nil {
				return err
			}
			var schema []byte
			if m.Spec.Config != nil && m.Spec.Config.Schema != "" {
				if schema, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(m.Spec.Config.Schema))); err != nil {
					return err
				}
			}
			content, err := typegen.Generate(schema)
			if err != nil {
				return err
			}
			if out == "" {
				out = filepath.Join(dir, "src", "codi-env.d.ts")
			}
			if check {
				cur, err := os.ReadFile(out)
				if err != nil || string(cur) != content {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s is out of date: run `codi plugin typegen %s`\n", out, dir)
					return exitError{1}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date\n", out)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "output file (default <plugin-dir>/src/codi-env.d.ts)")
	cmd.Flags().BoolVar(&check, "check", false, "fail when the file is out of date instead of writing it")
	return cmd
}

// ───────────────────────────────────────────────────────────── test

const (
	testServerID = "110000000000000001"
	testAppID    = "110000000000000002"
)

func newPluginTestCmd() *cobra.Command {
	var configFile string
	var verbose, script bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test <plugin-dir>",
		Short: "Run a plugin in the Discord simulator (build, start, smoke test)",
		Long: `Build the plugin, then deploy it in a throw-away repository on the Discord simulator
with the real runtime: a test server (with the channels, roles and categories its
config references), a bot granted every requested capability, one instance of the
plugin. The test fails when the plugin does not build, its config does not validate,
or the instance does not start.

Unless --script=false, a short scripted simulation follows: a member joins (when the
plugin handles memberJoin) and every slash command without required options is invoked
once; the messages, DMs and replies are printed, and a failed interaction fails the test.

--config is the plugin config (YAML or JSON), as in bot.yaml. Required x-codi-ref fields
without a value are filled with test resources; other required fields must be given.`,
		Example: `  codi plugin test plugins/welcome
  codi plugin test plugins/onboarding --config plugins/onboarding/test/config.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginTest(cmd, args[0], configFile, verbose, script, timeout)
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "plugin config file (YAML or JSON)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show the runtime and plugin logs")
	cmd.Flags().BoolVar(&script, "script", true, "run the scripted simulation after the start")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "overall timeout")
	return cmd
}

// refsOf collects the x-codi-ref values of a config (kind → keys).
func refsOf(schema map[string]any, v any, out map[string]map[string]bool) {
	if schema == nil {
		return
	}
	if kind, ok := schema["x-codi-ref"].(string); ok {
		add := func(x any) {
			if s, ok := x.(string); ok && s != "" {
				if out[kind] == nil {
					out[kind] = map[string]bool{}
				}
				out[kind][s] = true
			}
		}
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				add(e)
			}
		} else {
			add(v)
		}
	}
	switch tv := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for k, sub := range props {
			if sm, ok := sub.(map[string]any); ok {
				if val, ok := tv[k]; ok {
					refsOf(sm, val, out)
				}
			}
		}
	case []any:
		if _, isRef := schema["x-codi-ref"]; !isRef {
			if items, ok := schema["items"].(map[string]any); ok {
				for _, e := range tv {
					refsOf(items, e, out)
				}
			}
		}
	}
}

func runPluginTest(cmd *cobra.Command, pluginDir, configFile string, verbose, script bool, timeout time.Duration) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	absPlugin, err := filepath.Abs(pluginDir)
	if err != nil {
		return err
	}
	rep, err := buildPluginReport(absPlugin)
	if err != nil {
		return err
	}
	printPluginReport(stdout, rep)
	if len(rep.Errors) > 0 {
		return exitError{1}
	}
	m, _ := readManifest(absPlugin)

	// config: --config file, required refs filled with test resources
	cfg := map[string]any{}
	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		var raw map[string]any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("--config: %w", err)
		}
		if cfg, err = config.NormalizeConfig(raw); err != nil {
			return fmt.Errorf("--config: %w", err)
		}
	}
	var schema map[string]any
	if m.Spec.Config != nil && m.Spec.Config.Schema != "" {
		data, err := os.ReadFile(filepath.Join(absPlugin, filepath.FromSlash(m.Spec.Config.Schema)))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			return fmt.Errorf("config schema: %w", err)
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		req, _ := schema["required"].([]any)
		for _, r := range req {
			k, _ := r.(string)
			sub, _ := props[k].(map[string]any)
			if _, set := cfg[k]; set || sub == nil {
				continue
			}
			if kind, ok := sub["x-codi-ref"].(string); ok && sub["type"] != "array" {
				cfg[k] = "test-" + kind
			}
		}
	}
	effective := config.CloneConfig(cfg)
	if schema != nil {
		config.ApplyDefaults(schema, effective)
	}
	refs := map[string]map[string]bool{}
	refsOf(schema, effective, refs)

	// throw-away repository
	repoDir, err := os.MkdirTemp("", "codi-plugin-test-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(repoDir)
	name := m.Metadata.Name
	if err := copyRepoTree(absPlugin, filepath.Join(repoDir, "plugins", name)); err != nil {
		return err
	}
	if err := writeTestRepo(repoDir, name, m, cfg, refs); err != nil {
		return err
	}
	repo, ds := config.Load(repoDir, config.LoadOptions{})
	printDiagnosticsRel(stderr, ds.Errors(), "plugins/"+name+"/", absPlugin)
	if repo == nil || ds.HasErrors() {
		fmt.Fprintln(stderr, "The plugin config does not validate: pass a config with --config.")
		return exitError{1}
	}

	world := sim.NewWorld()
	seed, err := api.SeedSimWorld(world, repo, "tester")
	if err != nil {
		return err
	}
	src, err := source.NewLocal(repoDir, filepath.Join(repoDir, ".codi", "snapshots"))
	if err != nil {
		return err
	}
	defer src.Close()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		return err
	}
	defer store.Close()
	level := slog.LevelError + 4
	if verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	rt := runtime.New(runtime.Options{
		Source: src, Connector: world, Store: store, Mode: "dev", Simulator: true, ResyncInterval: -1,
		Secrets: func(n string) (string, bool) { v, ok := seed.Secrets[n]; return v, ok },
		Signer:  auth.NewSigner(auth.RandomKey()), PortalURL: "http://localhost:7420",
		AllowPrivateNetworks: true, Logger: log,
	})
	rctx, rcancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(rctx); close(done) }()
	defer func() { rcancel(); <-done }()

	fmt.Fprintln(stdout, "\nStarting the plugin on the simulator…")
	for !rt.Ready() {
		if st := rt.Status(); st.LastError != "" {
			fmt.Fprintf(stderr, "error: %s\n", st.LastError)
			return exitError{1}
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for the plugin to start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	inst := func() runtime.PluginStatus {
		for _, b := range rt.Status().Bots {
			for _, p := range b.Plugins {
				return p
			}
		}
		return runtime.PluginStatus{}
	}
	if p := inst(); p.Health != runtime.HealthHealthy {
		fmt.Fprintf(stderr, "error: the plugin instance is %s: %s\n", p.Health, p.Message)
		return exitError{1}
	}
	fmt.Fprintf(stdout, "ok   instance %s started\n", name)
	bot := seed.Bots["test"]
	if cmds := world.Commands(testAppID, testServerID); len(cmds) > 0 {
		var names []string
		for _, c := range cmds {
			names = append(names, "/"+c.Name)
		}
		fmt.Fprintf(stdout, "ok   commands registered: %s\n", strings.Join(names, " "))
	}
	if !script {
		return nil
	}

	failed := false
	tester := seed.Users[0]
	general, _ := world.ChannelByName(testServerID, "general")
	seen := map[string]bool{}
	printNew := func() {
		var lines []string
		for _, ch := range world.Channels(testServerID) {
			for _, msg := range world.Messages(ch.ID) {
				if !seen[msg.ID] {
					seen[msg.ID] = true
					lines = append(lines, fmt.Sprintf("       #%s %s: %s", ch.Name, msg.Author.Username, describeMessage(msg)))
				}
			}
		}
		for _, msg := range world.DMs(tester.ID, bot.ID) {
			if !seen[msg.ID] {
				seen[msg.ID] = true
				lines = append(lines, fmt.Sprintf("       DM to %s: %s", tester.Username, describeMessage(msg)))
			}
		}
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
	settle := func() {
		world.WaitIdle()
		time.Sleep(300 * time.Millisecond)
		world.WaitIdle()
	}
	if rep.def != nil && rep.def.HasEvent("memberJoin") {
		if err := world.AddMember(testServerID, tester.ID); err != nil {
			return err
		}
		settle()
		fmt.Fprintf(stdout, "ok   memberJoin: %s joined\n", tester.Username)
		printNew()
	} else if err := world.AddMember(testServerID, tester.ID); err != nil {
		return err
	}
	if rep.def != nil {
		for _, c := range rep.def.Commands {
			required := false
			for _, o := range c.Options {
				required = required || o.Required
			}
			if required {
				fmt.Fprintf(stdout, "skip /%s (required options)\n", c.Name)
				continue
			}
			// the server owner may use every command (default member permissions)
			h := world.InvokeCommand(seed.Owner.ID, testServerID, general.ID, testAppID, c.Name, nil)
			if h.Err() != nil {
				fmt.Fprintf(stdout, "FAIL /%s: %v\n", c.Name, h.Err())
				failed = true
				continue
			}
			s := h.Wait()
			if s.Deferred {
				s, _ = h.WaitUntil(5*time.Second, func(s sim.InteractionState) bool { return s.Edited || len(s.Followups) > 0 })
			}
			switch {
			case s.Failed:
				fmt.Fprintf(stdout, "FAIL /%s: the interaction failed (no answer)\n", c.Name)
				failed = true
			case s.Modal != nil:
				fmt.Fprintf(stdout, "ok   /%s: modal %q\n", c.Name, s.Modal.Title)
			case s.Message != nil:
				fmt.Fprintf(stdout, "ok   /%s: %s\n", c.Name, describeMessage(*s.Message))
				if s.Message.Content == plugin.GenericErrorMessage {
					failed = true
				}
			default:
				fmt.Fprintf(stdout, "ok   /%s: acknowledged\n", c.Name)
			}
			seen[messageID(s.Message)] = true
			settle()
			printNew()
		}
	}
	if p := inst(); p.Health != runtime.HealthHealthy {
		fmt.Fprintf(stdout, "FAIL instance is %s: %s\n", p.Health, p.Message)
		failed = true
	}
	if failed {
		return exitError{1}
	}
	fmt.Fprintln(stdout, "PASS")
	return nil
}

func messageID(m *discord.Message) string {
	if m == nil {
		return ""
	}
	return m.ID
}

func describeMessage(m discord.Message) string {
	parts := []string{}
	if m.Content != "" {
		parts = append(parts, fmt.Sprintf("%q", m.Content))
	}
	for _, e := range m.Embeds {
		t := e.Title
		if t == "" {
			t = e.Description
		}
		parts = append(parts, fmt.Sprintf("[embed %q]", t))
	}
	for _, row := range m.Components {
		for _, c := range row.Components {
			label := c.Label
			if label == "" {
				label = c.Placeholder
			}
			parts = append(parts, fmt.Sprintf("[%s %q]", componentName(c.Type), label))
		}
	}
	if m.Flags&discord.MessageFlagEphemeral != 0 {
		parts = append(parts, "(ephemeral)")
	}
	if len(parts) == 0 {
		return "(empty message)"
	}
	return strings.Join(parts, " ")
}

func componentName(t discord.ComponentType) string {
	switch t {
	case discord.ComponentButton:
		return "button"
	case discord.ComponentStringSelect:
		return "select"
	case discord.ComponentTextInput:
		return "input"
	}
	return "component"
}

// printDiagnosticsRel prints diagnostics of the throw-away repository with plugin
// paths shown relative to the real plugin directory.
func printDiagnosticsRel(w io.Writer, ds config.Diagnostics, prefix, real string) {
	for _, d := range ds {
		if strings.HasPrefix(d.File, prefix) {
			d.File = filepath.Join(real, strings.TrimPrefix(d.File, prefix))
		} else if d.File == "bots/test/bot.yaml" {
			d.File = "(plugin config)"
		}
		fmt.Fprintln(w, d.String())
	}
}

func writeTestRepo(dir, name string, m *config.Plugin, cfg map[string]any, refs map[string]map[string]bool) error {
	write := func(rel string, v any) error {
		data, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}
	sorted := func(kind string) []string {
		var out []string
		for k := range refs[kind] {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	roles := map[string]any{}
	for _, k := range sorted("role") {
		roles[k] = map[string]any{"name": k}
	}
	categories := map[string]any{}
	for _, k := range sorted("category") {
		categories[k] = map[string]any{"name": k}
	}
	channels := map[string]any{"general": map[string]any{"name": "general", "type": "text"}}
	for _, k := range sorted("channel") {
		if _, isCat := categories[k]; !isCat {
			channels[k] = map[string]any{"name": k, "type": "text"}
		}
	}
	project := map[string]any{"apiVersion": config.APIVersion, "kind": "Project",
		"metadata": map[string]any{"name": "plugin-test"}, "spec": map[string]any{"engine": engineRange()}}
	srvSpec := map[string]any{"discordId": testServerID, "profile": map[string]any{"name": "Plugin test"}, "channels": channels}
	if len(roles) > 0 {
		srvSpec["roles"] = roles
	}
	if len(categories) > 0 {
		srvSpec["categories"] = categories
	}
	server := map[string]any{"apiVersion": config.APIVersion, "kind": "Server", "metadata": map[string]any{"name": "test"}, "spec": srvSpec}
	inst := map[string]any{"name": name, "source": "../../plugins/" + name, "grants": m.Spec.Capabilities}
	if len(cfg) > 0 {
		inst["config"] = cfg
	}
	bot := map[string]any{"apiVersion": config.APIVersion, "kind": "Bot", "metadata": map[string]any{"name": "test"},
		"spec": map[string]any{"applicationId": testAppID, "token": map[string]any{"secretRef": "CODI_PLUGIN_TEST_TOKEN"},
			"profile": map[string]any{"username": "test-bot"}, "servers": []string{"test"}, "plugins": []any{inst}}}
	for rel, v := range map[string]any{"codi.yaml": project, "servers/test.yaml": server, "bots/test/bot.yaml": bot} {
		if err := write(rel, v); err != nil {
			return err
		}
	}
	return nil
}
