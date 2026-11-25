package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/importer"
	"github.com/cicord/cicord/internal/state"
)

func init() {
	extraCommands = append(extraCommands, newMvCmd)
}

func newMvCmd() *cobra.Command {
	var lf localFlags
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "mv <server> <role|category|channel> <old-key> <new-key> [dir]",
		Short: "Rename a resource key without deleting and recreating it",
		Long: `Rename the key of a role, category or channel of a server: the server YAML is rewritten
(the key and every reference to it: channel categories, hierarchy, access roles,
overwrite targets, profile system/rules channels) as well as the plugin configurations
that reference it through x-cicord-ref (base config and the server's override), and the
local state database mapping (--state, default .cicord/state.db) is moved to the new key.

Only the renamed tokens are changed in the files: comments and formatting are kept.
When a plugin instance runs on several servers and its base config references the key,
the command stops before writing anything (add an override for this server first, or
pass --force to rename anyway and fix the config by hand).

A runtime (cicord serve) keeps its own resource map: when the renamed file reaches it, the
reconciler sees the old key removed and the new key added with the same display name, and
moves the mapping (plan: "adopt" + "move … mapping"): the Discord resource is kept, never
duplicated. Changing the display name in the same commit defeats this matching, so rename
the key first, then the display name.`,
		Example: `  cicord mv my-community channel general discussions
  cicord mv my-community role moderator mods --dry-run`,
		Args: cobra.RangeArgs(4, 5),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := absDir(args[4:])
			if err != nil {
				return err
			}
			kind, err := parseKind(args[1])
			if err != nil {
				return err
			}
			return runMv(cmd, dir, args[0], kind, args[2], args[3], dryRun, force, &lf)
		},
	}
	lf.register(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the changes without writing anything")
	cmd.Flags().BoolVar(&force, "force", false, "rename even when a shared plugin base config references the key (it is left unchanged)")
	return cmd
}

func parseKind(s string) (state.ResourceKind, error) {
	switch strings.TrimSuffix(strings.ToLower(s), "s") {
	case "role":
		return state.KindRole, nil
	case "categorie", "category":
		return state.KindCategory, nil
	case "channel":
		return state.KindChannel, nil
	}
	return "", fmt.Errorf("unknown kind %q (role, category or channel)", s)
}

// fileEdit is the rewrite of one file.
type fileEdit struct {
	rel     string
	edits   []spanEdit
	notes   []string
	newData []byte
}

func runMv(cmd *cobra.Command, dir, server string, kind state.ResourceKind, oldKey, newKey string, dryRun, force bool, lf *localFlags) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if oldKey == newKey {
		return errors.New("the old and new keys are the same")
	}
	if !importer.KeyPattern(newKey) {
		return fmt.Errorf("%q is not a valid key (^[a-z][a-z0-9-]{0,62}$)", newKey)
	}
	repo, err := loadValidRepo(stderr, dir)
	if err != nil {
		return err
	}
	srv, ok := repo.Servers[server]
	if !ok {
		return fmt.Errorf("unknown server %q", server)
	}
	sp := srv.Spec
	exists := func(k string) bool {
		switch kind {
		case state.KindRole:
			_, ok := sp.Roles[k]
			return ok
		case state.KindCategory:
			_, ok := sp.Categories[k]
			return ok
		}
		_, ok := sp.Channels[k]
		return ok
	}
	if !exists(oldKey) {
		return fmt.Errorf("server %s has no %s %q", server, kind, oldKey)
	}
	if exists(newKey) {
		return fmt.Errorf("server %s already has a %s %q", server, kind, newKey)
	}
	if kind == state.KindChannel {
		if _, ok := sp.Categories[newKey]; ok {
			return fmt.Errorf("key %q is already used by a category (categories and channels share keys)", newKey)
		}
	}
	if kind == state.KindCategory {
		if _, ok := sp.Channels[newKey]; ok {
			return fmt.Errorf("key %q is already used by a channel (categories and channels share keys)", newKey)
		}
	}

	var files []*fileEdit
	se, err := serverEdits(repo.Root, srv.File, kind, oldKey, newKey)
	if err != nil {
		return err
	}
	files = append(files, se)
	be, warnings, err := botEdits(repo, server, kind, oldKey, newKey)
	if err != nil {
		return err
	}
	files = append(files, be...)

	for _, f := range files {
		if len(f.edits) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "%s: %d change(s)\n", f.rel, len(f.edits))
		for _, n := range f.notes {
			fmt.Fprintf(stdout, "  %s\n", n)
		}
	}
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}

	// state database
	statePath := lf.path(dir)
	var mapped *state.Resource
	var store interface {
		Resources() state.ResourceMap
		Close() error
	}
	if _, err := os.Stat(statePath); err == nil {
		s, _, err := lf.openState(dir, false)
		if err != nil {
			return err
		}
		store = s
		defer store.Close()
		r, err := s.Resources().Get(ctx, server, kind, oldKey)
		switch {
		case err == nil:
			if _, err := s.Resources().Get(ctx, server, kind, newKey); err == nil {
				return fmt.Errorf("the state database already maps %s %s/%s", kind, server, newKey)
			}
			mapped = &r
			fmt.Fprintf(stdout, "%s: %s %s/%s → %s (Discord ID %s)\n", statePath, kind, server, oldKey, newKey, r.ID)
		case errors.Is(err, state.ErrNotFound):
			fmt.Fprintf(stdout, "%s: no mapping for %s %s/%s\n", statePath, kind, server, oldKey)
		default:
			return err
		}
	} else {
		fmt.Fprintf(stdout, "no local state database (%s): nothing to move\n", statePath)
	}
	if dryRun {
		fmt.Fprintln(stdout, "Dry run: nothing written.")
		return nil
	}
	if len(warnings) > 0 && !force {
		return errors.New("nothing written: fix the plugin configs listed above first, or pass --force")
	}
	for _, f := range files {
		if len(f.edits) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(repo.Root, filepath.FromSlash(f.rel)), f.newData, 0o644); err != nil {
			return err
		}
	}
	if mapped != nil {
		if err := store.Resources().Put(ctx, state.Resource{Server: server, Kind: kind, Key: newKey, ID: mapped.ID, Owner: mapped.Owner}); err != nil {
			return err
		}
		if err := store.Resources().Delete(ctx, server, kind, oldKey); err != nil {
			return err
		}
	}
	_, ds := loadRepo(dir)
	printDiagnostics(stderr, ds)
	if ds.HasErrors() {
		return fmt.Errorf("the repository does not validate after the rename (please report this bug)")
	}
	fmt.Fprintf(stdout, "Renamed %s %s → %s on server %s.\n", kind, oldKey, newKey, server)
	return nil
}

// ───────────────────────────────────────────────────────────── server YAML

func serverEdits(root, rel string, kind state.ResourceKind, oldKey, newKey string) (*fileEdit, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	fe := &fileEdit{rel: rel}
	doc, err := parseYAMLRoot(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	spec := mapGet(doc, "spec")
	add := func(n *yaml.Node, what string) {
		fe.edits = append(fe.edits, spanEdit{line: n.Line, col: n.Column, old: oldKey, new: newKey})
		fe.notes = append(fe.notes, fmt.Sprintf("line %d: %s", n.Line, what))
	}
	renameKey := func(m *yaml.Node, what string) {
		if k := mapKeyNode(m, oldKey); k != nil {
			add(k, what)
		}
	}
	replaceIn := func(seq *yaml.Node, what string) {
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return
		}
		for _, it := range seq.Content {
			if it.Kind == yaml.ScalarNode && it.Value == oldKey {
				add(it, what)
			}
		}
	}
	replaceValue := func(n *yaml.Node, what string) {
		if n != nil && n.Kind == yaml.ScalarNode && n.Value == oldKey {
			add(n, what)
		}
	}
	switch kind {
	case state.KindRole:
		renameKey(mapGet(spec, "roles"), "roles."+oldKey)
		replaceIn(mapGet(spec, "hierarchy"), "hierarchy")
		for _, who := range []string{"admins", "moderators"} {
			replaceIn(mapGet(mapGet(mapGet(spec, "access"), who), "roles"), "access."+who+".roles")
		}
		for _, section := range []string{"categories", "channels"} {
			forEachEntry(mapGet(spec, section), func(k string, v *yaml.Node) {
				renameKey(mapGet(v, "overwrites"), section+"."+k+".overwrites")
			})
		}
		forEachEntry(mapGet(spec, "automod"), func(k string, v *yaml.Node) {
			replaceIn(mapGet(v, "exemptRoles"), "automod."+k+".exemptRoles")
		})
		forEachEntry(mapGet(spec, "emojis"), func(k string, v *yaml.Node) {
			replaceIn(mapGet(v, "roles"), "emojis."+k+".roles")
		})
		forEachOption(spec, func(where string, o *yaml.Node) { replaceIn(mapGet(o, "roles"), where+".roles") })
	case state.KindCategory:
		renameKey(mapGet(spec, "categories"), "categories."+oldKey)
		forEachEntry(mapGet(spec, "channels"), func(k string, v *yaml.Node) {
			replaceValue(mapGet(v, "category"), "channels."+k+".category")
		})
	case state.KindChannel:
		renameKey(mapGet(spec, "channels"), "channels."+oldKey)
		for _, f := range []string{"systemChannel", "rulesChannel"} {
			replaceValue(mapGet(mapGet(spec, "profile"), f), "profile."+f)
		}
		for _, f := range []string{"rulesChannel", "updatesChannel", "safetyAlertsChannel"} {
			replaceValue(mapGet(mapGet(spec, "community"), f), "community."+f)
		}
		replaceValue(mapGet(mapGet(spec, "afk"), "channel"), "afk.channel")
		replaceValue(mapGet(mapGet(spec, "widget"), "channel"), "widget.channel")
		forEachEntry(mapGet(spec, "automod"), func(k string, v *yaml.Node) {
			replaceIn(mapGet(v, "exemptChannels"), "automod."+k+".exemptChannels")
			replaceValue(mapGet(mapGet(v, "actions"), "alert"), "automod."+k+".actions.alert")
		})
		replaceIn(mapGet(mapGet(spec, "onboarding"), "defaultChannels"), "onboarding.defaultChannels")
		forEachOption(spec, func(where string, o *yaml.Node) { replaceIn(mapGet(o, "channels"), where+".channels") })
		if ws := mapGet(mapGet(spec, "welcomeScreen"), "channels"); ws != nil && ws.Kind == yaml.SequenceNode {
			for i, it := range ws.Content {
				replaceValue(mapGet(it, "channel"), fmt.Sprintf("welcomeScreen.channels[%d].channel", i))
			}
		}
	}
	if fe.newData, err = applySpanEdits(data, fe.edits); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return fe, nil
}

// ───────────────────────────────────────────────────────────── plugin configs

func botEdits(repo *config.Repo, server string, kind state.ResourceKind, oldKey, newKey string) ([]*fileEdit, []string, error) {
	var out []*fileEdit
	var warnings []string
	keys := make([]string, 0, len(repo.Bots))
	for k := range repo.Bots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, bk := range keys {
		b := repo.Bots[bk]
		data, err := os.ReadFile(filepath.Join(repo.Root, filepath.FromSlash(b.File)))
		if err != nil {
			return nil, nil, err
		}
		doc, err := parseYAMLRoot(data)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", b.File, err)
		}
		fe := &fileEdit{rel: b.File}
		plugins := mapGet(mapGet(doc, "spec"), "plugins")
		for i, inst := range b.Spec.Plugins {
			servers := inst.Servers
			if len(servers) == 0 {
				servers = b.Spec.Servers
			}
			if !contains(servers, server) {
				continue
			}
			var lp *config.LoadedPlugin
			for _, in := range repo.Instances {
				if in.Bot == bk && in.Name == inst.Name && in.Server == server {
					lp = in.Plugin
				}
			}
			if lp == nil || lp.SchemaDoc == nil || plugins == nil || i >= len(plugins.Content) {
				continue
			}
			node := plugins.Content[i]
			collect := func(n *yaml.Node, where string) []spanEdit {
				var edits []spanEdit
				rewriteRefs(lp.SchemaDoc, n, string(kind), oldKey, func(v *yaml.Node, path string) {
					edits = append(edits, spanEdit{line: v.Line, col: v.Column, old: oldKey, new: newKey})
					fe.notes = append(fe.notes, fmt.Sprintf("line %d: plugin %s %s%s", v.Line, inst.Name, where, path))
				}, "")
				return edits
			}
			// the server's override
			if ov := mapGet(mapGet(mapGet(node, "overrides"), server), "config"); ov != nil {
				fe.edits = append(fe.edits, collect(ov, "overrides."+server+".config")...)
			}
			// the base config: shared by every server of the instance
			if base := mapGet(node, "config"); base != nil {
				var others []string
				for _, s := range servers {
					if s != server {
						others = append(others, s)
					}
				}
				var probe []spanEdit
				rewriteRefs(lp.SchemaDoc, base, string(kind), oldKey, func(v *yaml.Node, _ string) {
					probe = append(probe, spanEdit{})
				}, "")
				switch {
				case len(probe) == 0:
				case len(others) == 0:
					fe.edits = append(fe.edits, collect(base, "config")...)
				default:
					warnings = append(warnings, fmt.Sprintf("%s: plugin %s also runs on %s, its base config references %s %q: "+
						"add an override for %s (overrides.%s.config) with the new key %q",
						b.File, inst.Name, strings.Join(others, ", "), kind, oldKey, server, server, newKey))
				}
			}
		}
		if len(fe.edits) == 0 {
			continue
		}
		if fe.newData, err = applySpanEdits(data, fe.edits); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", b.File, err)
		}
		out = append(out, fe)
	}
	return out, warnings, nil
}

// rewriteRefs walks a YAML value with its JSON schema (like config checkRefs) and calls
// found for every scalar that is an x-cicord-ref of kind equal to old.
func rewriteRefs(schema map[string]any, n *yaml.Node, kind, old string, found func(*yaml.Node, string), path string) {
	if schema == nil || n == nil {
		return
	}
	if n.Kind == yaml.AliasNode {
		return
	}
	ref, isRef := schema["x-cicord-ref"].(string)
	if isRef && ref == kind {
		switch n.Kind {
		case yaml.ScalarNode:
			if n.Value == old {
				found(n, path)
			}
		case yaml.SequenceNode:
			for i, it := range n.Content {
				if it.Kind == yaml.ScalarNode && it.Value == old {
					found(it, fmt.Sprintf("%s[%d]", path, i))
				}
			}
		}
	}
	switch n.Kind {
	case yaml.MappingNode:
		props, _ := schema["properties"].(map[string]any)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if sub, ok := props[n.Content[i].Value].(map[string]any); ok {
				rewriteRefs(sub, n.Content[i+1], kind, old, found, path+"."+n.Content[i].Value)
			}
		}
	case yaml.SequenceNode:
		if !isRef {
			if items, ok := schema["items"].(map[string]any); ok {
				for i, it := range n.Content {
					rewriteRefs(items, it, kind, old, found, fmt.Sprintf("%s[%d]", path, i))
				}
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// ───────────────────────────────────────────────────────────── YAML helpers

func parseYAMLRoot(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, errors.New("expected a single YAML document")
	}
	return doc.Content[0], nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapKeyNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}

func forEachEntry(m *yaml.Node, fn func(key string, v *yaml.Node)) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		fn(m.Content[i].Value, m.Content[i+1])
	}
}

// spanEdit replaces the scalar old (plain or quoted) starting at line:col (1-based,
// as reported by yaml.v3) with new.
type spanEdit struct {
	line, col int
	old, new  string
}

// applySpanEdits rewrites only the edited tokens of src, so that comments, blank lines,
// quoting and layout are kept byte for byte.
func applySpanEdits(src []byte, edits []spanEdit) ([]byte, error) {
	if len(edits) == 0 {
		return src, nil
	}
	lineStarts := []int{0}
	for i, c := range src {
		if c == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	type rep struct {
		at, n int
		with  string
	}
	var reps []rep
	seen := map[int]bool{}
	for _, e := range edits {
		if e.line < 1 || e.line > len(lineStarts) {
			return nil, fmt.Errorf("line %d out of range", e.line)
		}
		start := lineStarts[e.line-1]
		// yaml.v3 columns count characters; try that first, then bytes.
		cands := []int{runeOffset(src, start, e.col-1), start + e.col - 1}
		found := false
		for _, off := range cands {
			if off < 0 || off > len(src) {
				continue
			}
			rest := src[off:]
			for _, q := range []string{"", `"`, `'`} {
				tok := q + e.old + q
				if bytes.HasPrefix(rest, []byte(tok)) && tokenEnds(rest[len(tok):], q != "") {
					if !seen[off] {
						reps = append(reps, rep{at: off + len(q), n: len(e.old), with: e.new})
						seen[off] = true
					}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("line %d: cannot find %q at column %d", e.line, e.old, e.col)
		}
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i].at > reps[j].at })
	out := append([]byte(nil), src...)
	for _, r := range reps {
		out = append(out[:r.at], append([]byte(r.with), out[r.at+r.n:]...)...)
	}
	// sanity check: still a single valid YAML document
	if _, err := parseYAMLRoot(out); err != nil {
		return nil, fmt.Errorf("rewrite produced invalid YAML: %w", err)
	}
	return out, nil
}

func runeOffset(src []byte, start, runes int) int {
	off := start
	for i := 0; i < runes; i++ {
		if off >= len(src) || src[off] == '\n' {
			return -1
		}
		_, size := utf8.DecodeRune(src[off:])
		off += size
	}
	return off
}

// tokenEnds reports whether a plain scalar ends here (or a quoted one was closed).
func tokenEnds(rest []byte, quoted bool) bool {
	if quoted || len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case ':', ' ', '\t', '\n', '\r', ',', ']', '}', '#':
		return true
	}
	return false
}

// forEachOption calls fn for every onboarding answer (spec.onboarding.prompts[].options[]).
func forEachOption(spec *yaml.Node, fn func(where string, o *yaml.Node)) {
	prompts := mapGet(mapGet(spec, "onboarding"), "prompts")
	if prompts == nil || prompts.Kind != yaml.SequenceNode {
		return
	}
	for i, p := range prompts.Content {
		opts := mapGet(p, "options")
		if opts == nil || opts.Kind != yaml.SequenceNode {
			continue
		}
		for j, o := range opts.Content {
			fn(fmt.Sprintf("onboarding.prompts[%d].options[%d]", i, j), o)
		}
	}
}
