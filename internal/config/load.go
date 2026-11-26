package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cicord/cicord/internal/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/locales"
	"github.com/cicord/cicord/internal/version"
)

// Repo is a loaded Cicord repository.
type Repo struct {
	Root    string // absolute path
	Project *Project
	Servers map[string]*Server // by key
	Bots    map[string]*Bot    // by key
	// Plugins by resolution key (absolute dir for local plugins, "<registry>/<name>@<version>" for registry ones).
	Plugins map[string]*LoadedPlugin
	// Instances: one per (bot, plugin instance, server), sorted by bot, instance, server.
	Instances []Instance
}

// LoadedPlugin is a plugin manifest plus its compiled config schema.
type LoadedPlugin struct {
	Locales      locales.Catalog
	Registry     string
	Contracts    map[string]Contract
	Verified     bool
	Key          string
	Dir          string // absolute
	Source       string // as written in bot.yaml (first reference)
	Manifest     *Plugin
	ConfigSchema *jsonschema.Schema // nil when the plugin has no config schema
	SchemaDoc    map[string]any     // raw schema (for defaults, x-cicord-* and the admin UI)
}

// Instance is a plugin running for one (bot, server) pair (ADR-0010).
type Instance struct {
	Files        map[string][]byte `json:"-"`
	SecretRefs   map[string]TokenRef
	NetworkAllow []string
	Bot          string
	Name         string // instance name, unique within the bot
	Server       string // server key
	Plugin       *LoadedPlugin
	Grants       []string
	Features     map[string]string // feature -> reason disabled (empty when enabled)
	Interop      InstanceInterop
	// Config is the effective config: base config ⊕ server override (merge patch) with
	// schema defaults applied. JSON-compatible values (numbers are float64).
	Config map[string]any
}

// ID returns "<bot>/<instance>/<server>", the instance identity used for state scopes.
func (i Instance) ID() string { return i.Bot + "/" + i.Name + "/" + i.Server }

// PluginResolver resolves registry plugin references to a directory on disk.
type PluginResolver interface {
	Resolve(source, versionRange string) (dir, key string, err error)
}

type LoadOptions struct {
	// EngineVersion is the running Cicord version, checked against cicord.yaml spec.engine.
	// Empty skips the check.
	EngineVersion string
	// Resolver for registry plugins; nil makes registry references an error.
	Resolver PluginResolver
	// PrivilegedIntents reports the privileged intents enabled for a bot's application
	// when they are known (the runtime reads the live application flags). When nil or
	// unknown, each privileged intent a plugin uses gets a reminder warning.
	PrivilegedIntents func(bot string) (discord.Intents, bool)
}

// Load reads and validates the repository rooted at dir. The returned Repo is non-nil
// whenever cicord.yaml could be parsed, even if diagnostics contain errors.
func Load(dir string, opts LoadOptions) (*Repo, Diagnostics) {
	l := &loader{opts: opts}
	repo := l.load(dir)
	l.sink.ds.Sort()
	return repo, l.sink.ds
}

type loader struct {
	opts LoadOptions
	sink diagSink
	root string
	docs map[string]*document // by relative file
}

func (l *loader) rel(p string) string {
	if r, err := filepath.Rel(l.root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

func (l *loader) readDoc(abs string) *document {
	file := l.rel(abs)
	data, err := os.ReadFile(abs)
	if err != nil {
		l.sink.errorf(file, 0, "", "%v", err)
		return nil
	}
	return l.parseDoc(file, data)
}
func (l *loader) parseDoc(file string, data []byte) *document {
	doc, err := parseDocument(file, data)
	if err != nil {
		l.sink.errorf(file, 0, "", "invalid YAML: %v", err)
		return nil
	}
	l.docs[file] = doc
	for _, sc := range doc.suspiciousComments() {
		line, _ := strconv.Atoi(sc[0])
		l.sink.warnf(file, line, "", "%q is followed by %q, which YAML reads as a comment: quote the value if the text continues", sc[1], sc[2])
	}
	return doc
}

// decodeKind validates doc against the schema of kind and decodes it into out.
func (l *loader) decodeKind(doc *document, kind string, out any) bool {
	m, _ := doc.value.(map[string]any)
	if got, _ := m["kind"].(string); got != kind {
		l.sink.errorf(doc.file, doc.lineOf("/kind"), "/kind", "expected kind %q, got %q", kind, got)
		return false
	}
	schemas, err := kindSchemas()
	if err != nil {
		l.sink.errorf(doc.file, 0, "", "internal error: %v", err)
		return false
	}
	if !validateValue(&l.sink, doc, schemas[kind], doc.value, "") {
		return false
	}
	if err := doc.root.Decode(out); err != nil {
		l.sink.errorf(doc.file, 0, "", "decode: %v", err)
		return false
	}
	return true
}

func (l *loader) load(dir string) *Repo {
	abs, err := filepath.Abs(dir)
	if err != nil {
		l.sink.errorf("", 0, "", "%v", err)
		return nil
	}
	l.root = abs
	l.docs = map[string]*document{}

	repo := &Repo{
		Root:    abs,
		Servers: map[string]*Server{},
		Bots:    map[string]*Bot{},
		Plugins: map[string]*LoadedPlugin{},
	}

	// cicord.yaml
	pdoc := l.readDoc(filepath.Join(abs, "cicord.yaml"))
	if pdoc == nil {
		return nil
	}
	var p Project
	if l.decodeKind(pdoc, "Project", &p) {
		repo.Project = &p
		l.checkEngine(pdoc, p.Spec.Engine)
		l.checkRegistries(pdoc, p.Spec.Registries)
	} else {
		repo.Project = &p
	}

	// servers/*.yaml
	for _, f := range l.glob(filepath.Join(abs, "servers"), func(e fs.DirEntry) (string, bool) {
		n := e.Name()
		if e.IsDir() || (!strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml")) {
			return "", false
		}
		return filepath.Join(abs, "servers", n), true
	}) {
		doc := l.readDoc(f)
		if doc == nil {
			continue
		}
		var s Server
		if !l.decodeKind(doc, "Server", &s) {
			continue
		}
		s.File = doc.file
		want := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".yaml"), ".yml")
		if s.Metadata.Name != want {
			l.sink.errorf(doc.file, doc.lineOf("/metadata/name"), "/metadata/name",
				"server key %q must match the file name %q", s.Metadata.Name, want)
			continue
		}
		repo.Servers[s.Metadata.Name] = &s
	}

	// bots/<key>/bot.yaml
	for _, f := range l.glob(filepath.Join(abs, "bots"), func(e fs.DirEntry) (string, bool) {
		if !e.IsDir() {
			return "", false
		}
		p := filepath.Join(abs, "bots", e.Name(), "bot.yaml")
		if _, err := os.Stat(p); err != nil {
			return "", false
		}
		return p, true
	}) {
		doc := l.readDoc(f)
		if doc == nil {
			continue
		}
		var b Bot
		if !l.decodeKind(doc, "Bot", &b) {
			continue
		}
		b.File = doc.file
		want := filepath.Base(filepath.Dir(f))
		if b.Metadata.Name != want {
			l.sink.errorf(doc.file, doc.lineOf("/metadata/name"), "/metadata/name",
				"bot key %q must match the directory name %q", b.Metadata.Name, want)
			continue
		}
		repo.Bots[b.Metadata.Name] = &b
	}

	l.checkServers(repo)
	l.checkBots(repo)
	l.checkInterop(repo)
	l.checkUniqueIDs(repo)
	l.checkPortal(repo)
	return repo
}

func (l *loader) glob(dir string, pick func(fs.DirEntry) (string, bool)) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			l.sink.errorf(l.rel(dir), 0, "", "%v", err)
		}
		return nil
	}
	var out []string
	for _, e := range entries {
		if p, ok := pick(e); ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (l *loader) checkEngine(doc *document, rng string) {
	if l.opts.EngineVersion == "" || rng == "" {
		return
	}
	c, err := version.EngineConstraint(rng)
	if err != nil {
		l.sink.errorf(doc.file, doc.lineOf("/spec/engine"), "/spec/engine", "invalid semver range %q: %v", rng, err)
		return
	}
	v, err := semver.NewVersion(l.opts.EngineVersion)
	if err != nil {
		return // development builds ("dev") skip the check
	}
	if !c.Check(v) {
		l.sink.errorf(doc.file, doc.lineOf("/spec/engine"), "/spec/engine",
			"this repository requires Cicord %s, running %s", rng, l.opts.EngineVersion)
	}
}

// checkRegistries rejects duplicate registry names or URLs and the reserved name of the
// official source (an unofficial registry named "cicord" would make its plugins look official).
func (l *loader) checkRegistries(doc *document, regs []Registry) {
	names, urls := map[string]bool{}, map[string]bool{}
	for i, r := range regs {
		at := fmt.Sprintf("/spec/registries/%d", i)
		switch {
		case r.Name == OfficialRegistryName:
			l.sink.errorf(doc.file, doc.lineOf(at+"/name"), at+"/name",
				"the registry name %q is reserved for the official Cicord plugins", r.Name)
		case names[r.Name]:
			l.sink.errorf(doc.file, doc.lineOf(at+"/name"), at+"/name", "duplicate registry name %q", r.Name)
		}
		names[r.Name] = true
		if r.SecretRef != "" && r.TokenRef() == "" {
			l.sink.errorf(doc.file, doc.lineOf(at+"/secretRef"), at+"/secretRef",
				"the secretRef of registry %q must be %s (a registry token cannot reuse another secret)", r.Name, RegistryTokenRef(r.Name))
		}
		if u := NormalizeRegistryURL(r.URL); urls[u] {
			l.sink.errorf(doc.file, doc.lineOf(at+"/url"), at+"/url", "registry %q repeats the URL of another registry", r.Name)
		} else {
			urls[u] = true
		}
	}
}

// ───────────────────────────────────────────────────────────── server checks

func (l *loader) checkServers(repo *Repo) {
	for _, key := range sortedKeys(repo.Servers) {
		s := repo.Servers[key]
		doc := l.docs[s.File]
		sp := s.Spec
		ref := func(path, kind, k string, exists bool) {
			if !exists {
				l.sink.errorf(s.File, doc.lineOf(path), path, "unknown %s %q", kind, k)
			}
		}
		roleExists := func(k string) bool { _, ok := sp.Roles[k]; return ok }
		checkOverwrites := func(base string, ows map[string]OverwriteSpec) {
			for target := range ows {
				if target == "@everyone" || target == "@bot" || strings.HasPrefix(target, "member:") {
					continue
				}
				ref(base+jsonPointer(target), "role", target, roleExists(target))
			}
		}
		if p := sp.Profile; p != nil && p.RulesChannel != "" {
			_, ok := sp.Channels[p.RulesChannel]
			ref("/spec/profile/rulesChannel", "channel", p.RulesChannel, ok)
		}
		l.checkServerParity(s, doc)
		for i, r := range sp.Hierarchy {
			ref(fmt.Sprintf("/spec/hierarchy/%d", i), "role", r, roleExists(r))
		}
		for _, ck := range sortedKeys(sp.Categories) {
			checkOverwrites(jsonPointer("spec", "categories", ck, "overwrites"), sp.Categories[ck].Overwrites)
		}
		for _, ck := range sortedKeys(sp.Channels) {
			c := sp.Channels[ck]
			if c.Category != "" {
				_, ok := sp.Categories[c.Category]
				ref(jsonPointer("spec", "channels", ck, "category"), "category", c.Category, ok)
			}
			checkOverwrites(jsonPointer("spec", "channels", ck, "overwrites"), c.Overwrites)
			if c.SyncWithCategory != nil && *c.SyncWithCategory && len(c.Overwrites) > 0 {
				l.sink.errorf(s.File, doc.lineOf(jsonPointer("spec", "channels", ck)), jsonPointer("spec", "channels", ck),
					"syncWithCategory: true cannot be combined with overwrites")
			}
			if c.Category == "" && c.SyncWithCategory != nil && *c.SyncWithCategory {
				l.sink.errorf(s.File, doc.lineOf(jsonPointer("spec", "channels", ck)), jsonPointer("spec", "channels", ck),
					"syncWithCategory requires a category")
			}
		}
		for k := range sp.Categories {
			if _, dup := sp.Channels[k]; dup {
				l.sink.errorf(s.File, doc.lineOf(jsonPointer("spec", "channels", k)), jsonPointer("spec", "channels", k),
					"key %q is used by both a category and a channel", k)
			}
		}
		if a := sp.Access; a != nil {
			for name, pr := range map[string]*Principals{"admins": a.Admins, "moderators": a.Moderators} {
				if pr == nil {
					continue
				}
				for i, r := range pr.Roles {
					ref(fmt.Sprintf("/spec/access/%s/roles/%d", name, i), "role", r, roleExists(r))
				}
			}
		}
		if sp.SyncPolicy.Prune() && !sp.SyncPolicy.DeletionProtection() {
			l.sink.warnf(s.File, doc.lineOf("/spec/syncPolicy"), "/spec/syncPolicy",
				"prune is enabled without deletion protection: resources removed from this file will be deleted")
		}
	}
}

func (l *loader) checkUniqueIDs(repo *Repo) {
	seen := map[string]string{}
	for _, k := range sortedKeys(repo.Servers) {
		s := repo.Servers[k]
		if other, dup := seen[s.Spec.DiscordID]; dup {
			l.sink.errorf(s.File, l.docs[s.File].lineOf("/spec/discordId"), "/spec/discordId",
				"discordId already used by server %q", other)
		}
		seen[s.Spec.DiscordID] = k
	}
	apps := map[string]string{}
	for _, k := range sortedKeys(repo.Bots) {
		b := repo.Bots[k]
		if other, dup := apps[b.Spec.ApplicationID]; dup {
			l.sink.errorf(b.File, l.docs[b.File].lineOf("/spec/applicationId"), "/spec/applicationId",
				"applicationId already used by bot %q", other)
		}
		apps[b.Spec.ApplicationID] = k
	}
}

// ───────────────────────────────────────────────────────────── bot checks

// checkPrivilegedIntents reports the privileged intents a plugin instance uses: a
// reminder when the application flags are unknown (offline validation), nothing when
// they are enabled, a warning when the live flags say one is not enabled.
func (l *loader) checkPrivilegedIntents(b *Bot, botKey, path string, line int, lp *LoadedPlugin) {
	var enabled discord.Intents
	known := false
	if l.opts.PrivilegedIntents != nil {
		enabled, known = l.opts.PrivilegedIntents(botKey)
	}
	for _, intent := range lp.Manifest.Spec.Intents {
		in, err := discord.ParseIntents([]string{intent})
		if err != nil || in&discord.PrivilegedIntents == 0 {
			continue
		}
		var msg string
		switch {
		case !known:
			msg = fmt.Sprintf("plugin %q uses the privileged intent %s: make sure it is enabled in the Discord developer portal (Bot → Privileged Gateway Intents)",
				lp.Manifest.Metadata.Name, intent)
		case !enabled.Has(in):
			msg = fmt.Sprintf("plugin %q uses the privileged intent %s, which is not enabled for the application: turn it on in the Discord developer portal (%s → Privileged Gateway Intents)",
				lp.Manifest.Metadata.Name, intent, discord.PortalBotURL(b.Spec.ApplicationID))
		default:
			continue
		}
		l.sink.ds = append(l.sink.ds, Diagnostic{Severity: SeverityWarning, File: b.File, Line: line, Path: path, Code: CodePrivilegedIntent, Message: msg})
	}
}

func (l *loader) checkBots(repo *Repo) {
	for _, bk := range sortedKeys(repo.Bots) {
		b := repo.Bots[bk]
		doc := l.docs[b.File]
		botServers := map[string]bool{}
		for i, sk := range b.Spec.ServerKeys() {
			if _, ok := repo.Servers[sk]; !ok {
				p := fmt.Sprintf("/spec/servers/%d", i)
				l.sink.errorf(b.File, doc.lineOf(p), p, "unknown server %q", sk)
				continue
			}
			if botServers[sk] {
				p := fmt.Sprintf("/spec/servers/%d", i)
				l.sink.errorf(b.File, doc.lineOf(p), p, "duplicate server %q", sk)
			}
			if profile := b.Spec.Servers[i].Profile; profile != nil {
				for field, image := range map[string]*string{"avatar": profile.Avatar, "banner": profile.Banner} {
					if image == nil || *image == "" {
						continue
					}
					p := fmt.Sprintf("/spec/servers/%d/profile/%s", i, field)
					rel, ok := ResolveRepoPath(b.File, *image)
					if !ok {
						l.sink.errorf(b.File, doc.lineOf(p), p, "image escapes repository")
					} else if err := LocalPathError(repo.Root, rel); err != nil {
						l.sink.errorf(b.File, doc.lineOf(p), p, "profile image %s: %v", rel, err)
					}
				}
			}
			botServers[sk] = true
		}
		names := map[string]bool{}
		for pi, inst := range b.Spec.Plugins {
			base := fmt.Sprintf("/spec/plugins/%d", pi)
			if names[inst.Name] {
				l.sink.errorf(b.File, doc.lineOf(base+"/name"), base+"/name", "duplicate plugin instance name %q", inst.Name)
				continue
			}
			names[inst.Name] = true

			lp := l.loadPlugin(repo, b, base, inst)
			if lp == nil {
				continue
			}

			var files map[string][]byte
			network := l.instanceResources(repo, lp.Manifest.Spec, inst, b.File, base)

			checkedActive := false
			// servers of this instance
			servers := b.Spec.ServerKeys()
			if len(inst.Servers) > 0 {
				servers = inst.Servers
				for i, sk := range inst.Servers {
					if !botServers[sk] {
						p := fmt.Sprintf("%s/servers/%d", base, i)
						l.sink.errorf(b.File, doc.lineOf(p), p, "server %q is not one of the bot's servers", sk)
					}
				}
			}
			inServers := map[string]bool{}
			for _, sk := range servers {
				inServers[sk] = true
			}
			for sk := range inst.Overrides {
				if !inServers[sk] {
					p := base + "/overrides/" + sk
					l.sink.errorf(b.File, doc.lineOf(p), p, "override for server %q, which this plugin instance does not run on", sk)
				}
			}

			baseCfg, err := normalizeMap(inst.Config)
			if err != nil {
				l.sink.errorf(b.File, doc.lineOf(base+"/config"), base+"/config", "%v", err)
				continue
			}
			for _, sk := range servers {
				srv, ok := repo.Servers[sk]
				if !ok || !botServers[sk] {
					continue
				}
				cfg := baseCfg
				if ov, ok := inst.Overrides[sk]; ok && ov.Config != nil {
					patch, err := normalizeMap(ov.Config)
					if err != nil {
						l.sink.errorf(b.File, doc.lineOf(base+"/overrides/"+sk), base+"/overrides/"+sk, "%v", err)
						continue
					}
					cfg, _ = MergePatch(cloneValue(baseCfg), patch).(map[string]any)
					if cfg == nil {
						cfg = map[string]any{}
					}
				}
				cfg = EffectiveConfig(lp, cfg, nil)
				if !Disabled(cfg) && !checkedActive {
					if len(inst.Files) > 0 && slices.Contains(inst.Grants, "files.read") {
						files, err = LoadInstanceFiles(l.root, inst.Files)
						if err != nil {
							l.sink.errorf(b.File, doc.lineOf(base+"/files"), base+"/files", "%v", err)
						}
					}

					var missing []string
					for _, cap := range lp.Manifest.Spec.Capabilities {
						if !HasGrant(inst.Grants, cap) {
							missing = append(missing, cap)
						}
					}
					if len(missing) > 0 {
						l.sink.errorf(b.File, doc.lineOf(base+"/grants"), base+"/grants", "plugin %q requests capabilities that are not granted: %s", lp.Manifest.Metadata.Name, strings.Join(missing, ", "))
					}
					l.checkPrivilegedIntents(b, bk, base, doc.lineOf(base), lp)
					checkedActive = true
				}
				cfgPath := base + "/config"
				ds := ValidateInstanceConfig(lp, cfg, sk, &srv.Spec, b.File, cfgPath, network)
				for _, diagnostic := range ds {
					diagnostic.Line = doc.lineOf(diagnostic.Path)
					l.sink.ds = append(l.sink.ds, diagnostic)
				}
				instanceFiles := files
				if Disabled(cfg) {
					instanceFiles = nil
				}
				repo.Instances = append(repo.Instances, Instance{
					Bot: bk, Name: inst.Name, Server: sk, Plugin: lp, Grants: effectiveGrants(lp.Manifest.Spec, inst.Grants), Config: cfg, Features: l.features(lp.Manifest.Spec, inst.Grants, bk, cfg), Interop: inst.Interop,
					NetworkAllow: network, SecretRefs: inst.Secrets, Files: instanceFiles,
				})
			}
		}
		if route := b.Spec.DirectMessages; route != nil && !names[route.Instance] {
			const path = "/spec/directMessages/instance"
			l.sink.errorf(b.File, doc.lineOf(path), path, "unknown DM receiver instance %q", route.Instance)
		}
	}
	sort.Slice(repo.Instances, func(i, j int) bool {
		a, b := repo.Instances[i], repo.Instances[j]
		if a.Bot != b.Bot {
			return a.Bot < b.Bot
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Server < b.Server
	})
}

func (l *loader) loadPlugin(repo *Repo, b *Bot, base string, inst PluginInstance) *LoadedPlugin {
	doc := l.docs[b.File]
	var dir, key string
	if inst.IsLocal() {
		dir = filepath.Clean(filepath.Join(l.root, filepath.Dir(b.File), filepath.FromSlash(inst.Source)))
		if !strings.HasPrefix(dir, l.root+string(filepath.Separator)) && dir != l.root {
			l.sink.errorf(b.File, doc.lineOf(base+"/source"), base+"/source", "plugin path %q escapes the repository", inst.Source)
			return nil
		}
		if err := LocalPathError(l.root, l.rel(dir)); err != nil {
			l.sink.errorf(b.File, doc.lineOf(base+"/source"), base+"/source", "plugin source: %v", err)
			return nil
		}
		key = dir
	} else {
		if l.opts.Resolver == nil {
			l.sink.errorf(b.File, doc.lineOf(base+"/source"), base+"/source",
				"registry plugin %q cannot be resolved (run `cicord plugin add` to lock it)", inst.Source)
			return nil
		}
		var err error
		dir, key, err = l.opts.Resolver.Resolve(inst.Source, inst.Version)
		if err != nil {
			l.sink.errorf(b.File, doc.lineOf(base+"/source"), base+"/source", "%v", err)
			return nil
		}
	}
	if lp, ok := repo.Plugins[key]; ok {
		return lp
	}
	manifestFile := l.rel(filepath.Join(dir, "plugin.yaml"))
	manifest, err := build.ReadPackageFile(dir, "plugin.yaml")
	if err != nil {
		l.sink.errorf(manifestFile, 0, "", "%v", err)
		return nil
	}
	mdoc := l.parseDoc(manifestFile, manifest)
	if mdoc == nil {
		return nil
	}
	var m Plugin
	if !l.decodeKind(mdoc, "Plugin", &m) {
		return nil
	}
	l.checkEngineFor(mdoc, m.Spec.Engine)
	lp := &LoadedPlugin{Key: key, Dir: dir, Source: inst.Source, Manifest: &m}
	catalog, err := locales.Load(dir, m.Spec.Locales)
	lp.Locales = catalog
	if err != nil {
		l.sink.errorf(b.File, 0, base+"/source", "plugin %q locales: %v", key, err)
		return nil
	}
	if err := build.CheckPackageFile(dir, m.Spec.Entry); err != nil {
		l.sink.errorf(mdoc.file, mdoc.lineOf("/spec/entry"), "/spec/entry", "entry %q not found", m.Spec.Entry)
		return nil
	}
	if m.Spec.Assets != "" {
		if _, err := build.ReadAssets(dir, m.Spec.Assets); err != nil {
			l.sink.errorf(mdoc.file, mdoc.lineOf("/spec/assets"), "/spec/assets", "%v", err)
			return nil
		}
	}
	if m.Spec.Config != nil {
		sp := filepath.Join(dir, filepath.FromSlash(m.Spec.Config.Schema))
		raw, err := build.ReadPackageFile(dir, m.Spec.Config.Schema)
		if err != nil {
			l.sink.errorf(mdoc.file, mdoc.lineOf("/spec/config/schema"), "/spec/config/schema", "%v", err)
			return nil
		}
		s, sdoc, err := compilePluginConfigSchema("file://"+filepath.ToSlash(sp), raw)
		if err != nil {
			l.sink.errorf(l.rel(sp), 0, "", "invalid config schema: %v", err)
			return nil
		}
		lp.ConfigSchema, lp.SchemaDoc = s, sdoc
	}
	if !inst.IsLocal() && m.Spec.Interop != nil {
		registry, _, _ := strings.Cut(inst.Source, "/")
		if registry != "cicord" {
			lp.Registry = registry
		}
		if p, ok := l.opts.Resolver.(interface{ Provenance(string) (string, bool) }); ok {
			lp.Registry, lp.Verified = p.Provenance(inst.Source)
		}
	}
	if err := lp.LoadContracts(); err != nil {
		l.sink.errorf(mdoc.file, mdoc.lineOf("/spec/interop"), "/spec/interop", "%v", err)
		return nil
	}
	repo.Plugins[key] = lp
	return lp
}

func (l *loader) checkEngineFor(doc *document, rng string) {
	if l.opts.EngineVersion == "" {
		return
	}
	v, err := semver.NewVersion(l.opts.EngineVersion)
	if err != nil {
		return
	}
	c, err := version.EngineConstraint(rng)
	if err != nil {
		l.sink.errorf(doc.file, doc.lineOf("/spec/engine"), "/spec/engine", "invalid semver range %q", rng)
		return
	}
	if !c.Check(v) {
		l.sink.errorf(doc.file, doc.lineOf("/spec/engine"), "/spec/engine",
			"plugin requires Cicord %s, running %s", rng, l.opts.EngineVersion)
	}
}

// ───────────────────────────────────────────────────────────── x-cicord-ref

type refProblem struct{ path, msg string }

// checkRefs walks the schema and the config together, checking x-cicord-ref values
// against the server's resource keys.
func checkRefs(schema map[string]any, v any, srv *ServerSpec, path string) []refProblem {
	if schema == nil {
		return nil
	}
	var out []refProblem
	if kind, ok := schema["x-cicord-ref"].(string); ok {
		check := func(p string, s any) {
			k, ok := s.(string)
			if !ok {
				return
			}
			var exists bool
			switch kind {
			case "channel":
				_, exists = srv.Channels[k]
			case "category":
				_, exists = srv.Categories[k]
			case "role":
				_, exists = srv.Roles[k]
			default:
				out = append(out, refProblem{p, fmt.Sprintf("unknown x-cicord-ref kind %q in config schema", kind)})
				return
			}
			if !exists {
				out = append(out, refProblem{p, fmt.Sprintf("unknown %s %q", kind, k)})
			}
		}
		switch tv := v.(type) {
		case []any:
			for i, e := range tv {
				check(fmt.Sprintf("%s/%d", path, i), e)
			}
		default:
			check(path, tv)
		}
	}
	switch tv := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for k, sub := range props {
			if val, ok := tv[k]; ok {
				if sm, ok := sub.(map[string]any); ok {
					out = append(out, checkRefs(sm, val, srv, path+jsonPointer(k))...)
				}
			}
		}
	case []any:
		// Arrays: either the array itself carries x-cicord-ref (handled above) or its
		// items schema does / contains nested refs.
		if _, isRef := schema["x-cicord-ref"]; !isRef {
			if items, ok := schema["items"].(map[string]any); ok {
				for i, e := range tv {
					out = append(out, checkRefs(items, e, srv, fmt.Sprintf("%s/%d", path, i))...)
				}
			}
		}
	}
	return dedupeProblems(out)
}

func dedupeProblems(in []refProblem) []refProblem {
	seen := map[refProblem]bool{}
	var out []refProblem
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// ───────────────────────────────────────────────────────────── helpers

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizeMap converts YAML-decoded values into JSON-compatible ones (float64 numbers).
func normalizeMap(m map[string]any) (map[string]any, error) {
	if m == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DecodeYAML is a small helper for callers that need to decode a typed resource from bytes.
func DecodeYAML(data []byte, out any) error { return yaml.Unmarshal(data, out) }
