package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/planfmt"
	"github.com/npjonath/codi/internal/reconcile"
)

func init() {
	extraCommands = append(extraCommands, newPlanCmd, newApplyCmd)
}

type planOpts struct {
	remoteFlags
	local    localFlags
	output   string
	comment  bool
	pr       int
	detailed bool
}

func newPlanCmd() *cobra.Command {
	var o planOpts
	cmd := &cobra.Command{
		Use:   "plan [dir]",
		Short: "Show what a sync would change in Discord",
		Long: `Compute the diff between the repository and the live Discord servers. Nothing is applied.

Remote (CI): with --remote, the repository is uploaded (.tar.gz, without .git,
node_modules, .codi, dist, .env files and the root .gitignore patterns) to the runtime,
which validates it and plans it with its own state and bots (POST /api/v1/plan).
The runtime URL is --remote=URL, else CODI_RUNTIME_URL (https, or http on localhost);
the bearer token is CODI_RUNTIME_TOKEN. codi.yaml spec.runtime.url is never used to
send the token (a pull request controls that file): set CODI_RUNTIME_URL in CI.

Local: without --remote, the repository is validated, then planned against Discord
directly with the bot tokens from the environment (token.secretRef) and the local
state database (--state, default .codi/state.db; read only, never created).

--comment posts the plan on the pull request when running in GitHub Actions
(GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_PATH; GITHUB_API_URL for GitHub
Enterprise), updating the previous CODI comment instead of adding a new one.

Symbols: + create, ~ update, - delete, > reorder, = adopt (existing resource with the
same name), # stop managing (left in Discord), ! blocked (with the reason).

Exit codes: 0 success, 1 error or invalid configuration, 2 changes pending (only with
--detailed-exitcode).`,
		Example: `  codi plan                                   # local, bot tokens from the environment
  codi plan --remote                          # runtime from CODI_RUNTIME_URL
  codi plan --remote=https://codi.example.com --comment
  codi plan --remote -o json
  codi plan --detailed-exitcode; echo $?      # 0 no changes, 1 error, 2 changes pending`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			args = o.splitRemoteArgs(args)
			if len(args) > 1 {
				return fmt.Errorf("too many arguments: %v", args)
			}
			if err := checkOutput(o.output); err != nil {
				return err
			}
			dir, err := absDir(args)
			if err != nil {
				return err
			}
			return runPlan(cmd, dir, &o)
		},
	}
	o.remoteFlags.register(cmd, false)
	o.local.register(cmd)
	o.local.registerServers(cmd)
	f := cmd.Flags()
	f.StringVarP(&o.output, "output", "o", "text", "output format: text | json")
	f.BoolVar(&o.comment, "comment", false, "post/update the plan as a pull-request comment (GitHub Actions)")
	f.IntVar(&o.pr, "pr", 0, "pull request number for --comment (default: from GITHUB_EVENT_PATH)")
	f.BoolVar(&o.detailed, "detailed-exitcode", false, "exit 2 when changes are pending")
	return cmd
}

func runPlan(cmd *cobra.Command, dir string, o *planOpts) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	var (
		res     planfmt.Result
		failed  error
		project string
		where   string
	)
	if p, err := readProject(dir); err == nil {
		project = p.Metadata.Name
	}
	if o.remote != "" {
		if len(o.local.servers) > 0 {
			return errors.New("--server is not supported with --remote")
		}
		base, err := o.resolveURL(dir)
		if err != nil {
			return err
		}
		res, err = remotePlan(ctx, stderr, base, dir)
		if err != nil {
			return err
		}
		where = "the runtime"
	} else {
		var err error
		res, failed, err = localPlan(ctx, stderr, dir, &o.local)
		if err != nil {
			return err
		}
		where = "Discord (local)"
	}

	if o.output == "json" {
		if err := writeJSONOut(stdout, res); err != nil {
			return err
		}
	} else {
		printDiagnostics(stderr, res.Diagnostics)
		if !res.Valid {
			fmt.Fprintln(stderr, "The configuration is invalid: nothing was planned.")
		} else {
			planfmt.Text(stdout, res.Plans)
		}
	}

	if o.comment {
		if err := postPlanComment(ctx, stderr, project, res, o.pr, where); err != nil {
			return err
		}
	}
	switch {
	case !res.Valid:
		return exitError{1}
	case failed != nil:
		fmt.Fprintln(stderr, "error:", failed)
		return exitError{1}
	case o.detailed && planfmt.Pending(res.Plans):
		return exitError{2}
	}
	return nil
}

// remotePlan uploads the repository to POST /api/v1/plan.
func remotePlan(ctx context.Context, stderr io.Writer, base, dir string) (planfmt.Result, error) {
	var res planfmt.Result
	c, err := newAPIClient(base)
	if err != nil {
		return res, err
	}
	archive, files, err := archiveRepo(dir)
	if err != nil {
		return res, err
	}
	fmt.Fprintf(stderr, "Planning %d file(s) on %s…\n", files, base)
	if err := c.do(ctx, http.MethodPost, "/api/v1/plan", bytes.NewReader(archive), "application/gzip", &res); err != nil {
		return res, err
	}
	if res.Diagnostics == nil {
		res.Diagnostics = []config.Diagnostic{}
	}
	if res.Plans == nil {
		res.Plans = []planfmt.Plan{}
	}
	return res, nil
}

// localPlan validates dir and plans every (selected) server against Discord. failed
// reports servers that could not be planned (their plan carries a blocked entry).
func localPlan(ctx context.Context, stderr io.Writer, dir string, lf *localFlags) (planfmt.Result, error, error) {
	res := planfmt.Result{Diagnostics: []config.Diagnostic{}, Plans: []planfmt.Plan{}}
	repo, ds := loadRepo(dir)
	if ds != nil {
		res.Diagnostics = ds
	}
	if repo == nil || ds.HasErrors() {
		return res, nil, nil
	}
	res.Valid = true
	if err := lf.checkServers(repo); err != nil {
		return res, nil, err
	}
	store, _, err := lf.openState(dir, true)
	if err != nil {
		return res, nil, err
	}
	defer store.Close()
	b := newBots(repo)
	defer b.close(context.WithoutCancel(ctx))
	var errs []error
	for _, sr := range lf.reconcilers(ctx, repo, b, store, true) {
		var p reconcile.Plan
		err := sr.err
		if err == nil {
			p, err = sr.rec.Plan(ctx)
			sr.rec.Close()
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sr.key, err))
			p = reconcile.Plan{Server: sr.key, Blocked: []reconcile.Blocked{{Action: "sync", Kind: reconcile.KindServer, Key: sr.key, Reason: firstLineOf(err.Error())}}}
		}
		fp, err := planfmt.FromReconcile(p)
		if err != nil {
			return res, nil, err
		}
		res.Plans = append(res.Plans, fp...)
	}
	return res, errors.Join(errs...), nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// postPlanComment publishes the plan on the pull request (GitHub Actions).
func postPlanComment(ctx context.Context, stderr io.Writer, project string, res planfmt.Result, pr int, where string) error {
	gh, err := githubFromEnv(pr)
	if errors.Is(err, errNotPullRequest) {
		fmt.Fprintln(stderr, "--comment: not running for a pull request, no comment posted")
		return nil
	}
	if err != nil {
		return fmt.Errorf("--comment: %w", err)
	}
	marker := "<!-- codi-plan -->"
	title := "CODI plan"
	if project != "" {
		marker = fmt.Sprintf("<!-- codi-plan:%s -->", project)
		title = fmt.Sprintf("CODI plan for `%s`", project)
	}
	footer := "<sub>Planned against " + where
	if sha := os.Getenv("GITHUB_SHA"); sha != "" {
		if len(sha) > 7 {
			sha = sha[:7]
		}
		footer += " at " + sha
	}
	footer += " by `codi plan`. Nothing is applied until the change is merged.</sub>"
	body := planfmt.Markdown(res, planfmt.MarkdownOptions{Marker: marker, Title: title, Footer: footer})
	url, created, err := gh.upsert(ctx, marker, body)
	if err != nil {
		return fmt.Errorf("--comment: %w", err)
	}
	verb := "updated"
	if created {
		verb = "posted"
	}
	fmt.Fprintf(stderr, "Plan %s on pull request #%d: %s\n", verb, gh.number, url)
	return nil
}

// ───────────────────────────────────────────────────────────── apply

func newApplyCmd() *cobra.Command {
	var lf localFlags
	var yes bool
	var output string
	cmd := &cobra.Command{
		Use:   "apply [dir]",
		Short: "Reconcile Discord with the repository directly (dev / bootstrap)",
		Long: `Validate the repository, plan every server against Discord with the bot tokens from the
environment (token.secretRef), show the plan, ask for confirmation, then apply it and
record the created/adopted resources in the local state database (--state, default
.codi/state.db).

This is for development and bootstrapping: in production the runtime (codi serve)
applies merged changes and keeps its own state. Applying with the CLI and with a runtime
on the same servers requires sharing the state database, otherwise the runtime adopts the
resources by name on its next sync.

Without a terminal, --yes is required.`,
		Example: `  DISCORD_TOKEN_MODERATOR=… codi apply
  codi apply --server ma-commu --yes
  codi apply ./infra --state /var/lib/codi/state.db`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			dir, err := absDir(args)
			if err != nil {
				return err
			}
			return runApply(cmd, dir, &lf, yes, output)
		},
	}
	lf.register(cmd)
	lf.registerServers(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking for confirmation")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format of the result: text | json")
	return cmd
}

type applyResult struct {
	Server  string             `json:"server"`
	Plan    planfmt.Plan       `json:"plan"`
	Applied []planfmt.Change   `json:"applied"`
	Failed  []reconcileFailure `json:"failed"`
	Error   string             `json:"error,omitempty"`
}

type reconcileFailure struct {
	Change planfmt.Change `json:"change"`
	Error  string         `json:"error"`
}

func runApply(cmd *cobra.Command, dir string, lf *localFlags, yes bool, output string) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	repo, err := loadValidRepo(stderr, dir)
	if err != nil {
		return err
	}
	if err := lf.checkServers(repo); err != nil {
		return err
	}
	store, statePath, err := lf.openState(dir, false)
	if err != nil {
		return err
	}
	defer store.Close()
	b := newBots(repo)
	defer b.close(context.WithoutCancel(ctx))

	type planned struct {
		sr   serverReconciler
		plan reconcile.Plan
	}
	var todo []planned
	var shown []planfmt.Plan
	var errs []error
	for _, sr := range lf.reconcilers(ctx, repo, b, store, false) {
		if sr.err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sr.key, sr.err))
			continue
		}
		p, err := sr.rec.Plan(ctx)
		if err != nil {
			sr.rec.Close()
			errs = append(errs, fmt.Errorf("server %s: %w", sr.key, err))
			continue
		}
		fp, _ := planfmt.FromReconcile(p)
		shown = append(shown, fp...)
		todo = append(todo, planned{sr, p})
	}
	defer func() {
		for _, t := range todo {
			t.sr.rec.Close()
		}
	}()
	planOut := stdout
	if output == "json" {
		planOut = stderr
	}
	planfmt.Text(planOut, shown)
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitError{1}
	}
	if !planfmt.Pending(shown) {
		if output == "json" {
			return writeJSONOut(stdout, []applyResult{})
		}
		return nil
	}
	if !yes {
		ok, err := confirm(cmd, "\nApply these changes to Discord?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(stderr, "Apply cancelled.")
			return exitError{1}
		}
	}
	var results []applyResult
	failed := false
	for _, t := range todo {
		if t.plan.Empty() {
			continue
		}
		res, err := t.sr.rec.Apply(ctx, t.plan)
		fp, _ := planfmt.FromReconcile(t.plan)
		ar := applyResult{Server: t.sr.key, Plan: fp[0], Applied: []planfmt.Change{}, Failed: []reconcileFailure{}}
		if applied, e := planfmt.FromReconcile(reconcile.Plan{Changes: res.Applied}); e == nil {
			ar.Applied = applied[0].Changes
		}
		for _, f := range res.Failed {
			fc, _ := planfmt.FromReconcile(reconcile.Plan{Changes: []reconcile.Change{f.Change}})
			ar.Failed = append(ar.Failed, reconcileFailure{Change: fc[0].Changes[0], Error: f.Error})
		}
		if err != nil {
			ar.Error = err.Error()
			failed = true
		}
		results = append(results, ar)
	}
	if output == "json" {
		if err := writeJSONOut(stdout, results); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			fmt.Fprintf(stdout, "\nServer %s: %d change(s) applied, %d failed\n", r.Server, len(r.Applied), len(r.Failed))
			for _, f := range r.Failed {
				fmt.Fprintf(stdout, "  ! %s: %s\n", f.Change.Summary, f.Error)
			}
		}
		if statePath != "" {
			fmt.Fprintf(stdout, "\nResource map saved in %s\n", statePath)
		}
	}
	if failed {
		return exitError{1}
	}
	return nil
}
