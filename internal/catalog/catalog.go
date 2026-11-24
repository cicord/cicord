// Package catalog is the bundled plugin catalog: the official plugins embedded in the
// binary (examples.Plugins) until the plugin registry exists. The setup wizard installs a
// first plugin from it and the future Store page will list it next to registry sources.
//
// An entry describes what a plugin needs before it is installed (capabilities, gateway
// intents, network egress, config schema, slash commands) so the admin can review it;
// Files returns the plugin sources to copy into a repository under plugins/<name>/.
package catalog

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/cicord/cicord/examples"
	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
)

// Command is a slash command a plugin registers.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Entry is one installable plugin.
type Entry struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Keywords    []string `json:"keywords"`
	// Source is the catalog source ("official" for the bundled plugins).
	Source       string   `json:"source"`
	Capabilities []string `json:"capabilities"`
	Intents      []string `json:"intents"`
	// PrivilegedIntents are the intents that must be enabled in the developer portal.
	PrivilegedIntents []string       `json:"privilegedIntents"`
	Network           []string       `json:"network"`
	Schema            map[string]any `json:"schema"`
	// Commands is nil when unknown (the plugin's code was not inspected).
	Commands []Command `json:"commands"`

	dir string // directory inside the embedded FS
}

// Catalog is safe for concurrent use.
type Catalog struct {
	fsys fs.FS
	dirs []string

	once    sync.Once
	entries []Entry
	err     error
}

// Official returns the catalog of the plugins bundled in the binary.
func Official() *Catalog { return New(examples.Plugins, examples.Official) }

// New builds a catalog from plugin directories of fsys (tests).
func New(fsys fs.FS, dirs []string) *Catalog { return &Catalog{fsys: fsys, dirs: dirs} }

// List returns the entries (built and inspected once, lazily).
func (c *Catalog) List() ([]Entry, error) {
	c.once.Do(func() { c.entries, c.err = c.load() })
	return c.entries, c.err
}

// Get returns an entry by plugin name.
func (c *Catalog) Get(name string) (Entry, bool) {
	es, err := c.List()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range es {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

func (c *Catalog) load() ([]Entry, error) {
	tmp, err := os.MkdirTemp("", "cicord-catalog-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var out []Entry
	for _, dir := range c.dirs {
		e, err := c.loadOne(dir, tmp)
		if err != nil {
			return nil, fmt.Errorf("catalog %s: %w", dir, err)
		}
		out = append(out, e)
	}
	return out, nil
}

func (c *Catalog) loadOne(dir, tmp string) (Entry, error) {
	e, err := describe(c.fsys, dir, tmp, true)
	if err != nil {
		return Entry{}, err
	}
	e.Source, e.dir = "official", dir
	return e, nil
}

// Describe reads the plugin in a directory on disk (a registry plugin downloaded to the
// cache): manifest, config schema and, when inspect is true, its slash commands.
// Listing commands builds and evaluates the plugin's code (in the sandbox, without host
// APIs): callers only pass inspect for trusted sources. Commands is nil when not inspected.
func Describe(dir string, inspect bool) (Entry, error) {
	tmp, err := os.MkdirTemp("", "cicord-describe-*")
	if err != nil {
		return Entry{}, err
	}
	defer os.RemoveAll(tmp)
	// os.Root refuses paths that escape dir, through ".." or symlinks.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	return describe(root.FS(), ".", tmp, inspect)
}

func describe(fsys fs.FS, dir, tmp string, inspect bool) (Entry, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "plugin.yaml"))
	if err != nil {
		return Entry{}, err
	}
	var m config.Plugin
	if err := config.DecodeYAML(raw, &m); err != nil {
		return Entry{}, fmt.Errorf("plugin.yaml: %w", err)
	}
	e := Entry{Name: m.Metadata.Name, DisplayName: m.Metadata.DisplayName, Description: m.Metadata.Description,
		Version: m.Metadata.Version, Keywords: nonNil(m.Metadata.Keywords),
		Capabilities: nonNil(m.Spec.Capabilities), Intents: nonNil(m.Spec.Intents), Network: []string{},
		Schema: map[string]any{}, PrivilegedIntents: []string{}}
	if e.DisplayName == "" {
		e.DisplayName = e.Name
	}
	if m.Spec.Network != nil {
		e.Network = nonNil(m.Spec.Network.Allow)
	}
	if in, err := discord.ParseIntents(m.Spec.Intents); err == nil {
		e.PrivilegedIntents = nonNil((in & discord.PrivilegedIntents).Names())
	}
	if m.Spec.Config != nil && m.Spec.Config.Schema != "" {
		data, err := fs.ReadFile(fsys, path.Join(dir, path.Clean(m.Spec.Config.Schema)))
		if err != nil {
			return Entry{}, err
		}
		if err := json.Unmarshal(data, &e.Schema); err != nil {
			return Entry{}, fmt.Errorf("config schema: %w", err)
		}
	}
	if !inspect {
		return e, nil
	}
	// Build and inspect the plugin to list its slash commands.
	work := filepath.Join(tmp, e.Name)
	files, err := readTree(fsys, dir)
	if err != nil {
		return Entry{}, err
	}
	for rel, data := range files {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Entry{}, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return Entry{}, err
		}
	}
	res, err := build.Bundle(work, m.Spec.Entry)
	if err != nil {
		return Entry{}, fmt.Errorf("build: %w", err)
	}
	def, err := plugin.Inspect(res.Code)
	if err != nil {
		return Entry{}, fmt.Errorf("inspect: %w", err)
	}
	e.Commands = []Command{}
	for _, cmd := range def.Commands {
		e.Commands = append(e.Commands, Command{Name: cmd.Name, Description: cmd.Description})
	}
	return e, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

// files returns the plugin sources, relative to the plugin directory.
func (c *Catalog) files(dir string) (map[string][]byte, error) { return readTree(c.fsys, dir) }

func readTree(fsys fs.FS, dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := p
		if dir != "." {
			rel = strings.TrimPrefix(p, dir+"/")
		}
		if rel == "tsconfig.json" {
			data = portableTSConfig(data)
		}
		out[rel] = data
		return nil
	})
	return out, err
}

// Files returns the files to write into a repository for a plugin: repo-relative paths
// (plugins/<name>/…) → content.
func (c *Catalog) Files(name string) (map[string][]byte, error) {
	e, ok := c.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown catalog plugin %q", name)
	}
	fs, err := c.files(e.dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(fs))
	for rel, data := range fs {
		out[PluginDir(name)+"/"+rel] = data
	}
	return out, nil
}

// PluginDir is the repository directory a catalog plugin is installed to.
func PluginDir(name string) string { return "plugins/" + name }

// SortedPaths returns the keys of a file map, sorted.
func SortedPaths(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// portableTSConfig drops compilerOptions.paths: in the examples it points to the SDK
// types of the Cicord repository, which do not exist in a user's repository.
func portableTSConfig(data []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return data
	}
	co, _ := doc["compilerOptions"].(map[string]any)
	if co == nil {
		return data
	}
	if _, ok := co["paths"]; !ok {
		return data
	}
	delete(co, "paths")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return data
	}
	return append(out, '\n')
}
