package config

import (
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func (l *loader) instanceResources(spec PluginSpec, inst PluginInstance, file, base string) ([]string, map[string][]byte) {
	report := func(field, msg string, args ...any) {
		p := base + "/" + field
		l.sink.errorf(file, l.docs[file].lineOf(p), p, msg, args...)
	}
	var allow []string
	if spec.Network != nil {
		allow = slices.Clone(spec.Network.Allow)
	}
	if inst.Network != nil {
		if spec.Network == nil || !spec.Network.Configurable {
			report("network", "plugin does not allow configurable network hosts")
		} else {
			allow = append(allow, inst.Network.Allow...)
		}
	}
	declared := map[string]bool{}
	for _, d := range spec.Secrets {
		if declared[d.Name] {
			report("secrets", "duplicate secret declaration %q", d.Name)
		}
		if d.Budget != nil && (d.Budget.Provider != "anthropic-messages" || len(d.Hosts) != 1 || d.Hosts[0] != "api.anthropic.com") {
			report("secrets", "a metered Anthropic secret must be restricted to api.anthropic.com")
		}
		declared[d.Name] = true
	}
	for n := range inst.Secrets {
		if !declared[n] {
			report("secrets", "secret %q is not declared by the plugin", n)
		}
	}
	if len(inst.Files) == 0 {
		return allow, nil
	}
	if !slices.Contains(inst.Grants, "files.read") {
		report("files", "instance files require files.read")
		return allow, nil
	}
	files, err := LoadInstanceFiles(l.root, inst.Files)
	if err != nil {
		report("files", "%v", err)
	}
	return allow, files
}

func (d PluginSecret) BudgetLimit(cfg map[string]any) (int, bool) {
	if d.Budget == nil {
		return 0, false
	}
	n, ok := cfg[d.Budget.DailyLimitConfig].(float64)
	if !ok || math.Trunc(n) != n || n < 1 || n > 10000000 {
		return 0, false
	}
	return int(n), true
}

func checkProviderLimits(spec PluginSpec, cfg map[string]any) []refProblem {
	var out []refProblem
	if cfg["enabled"] == false {
		return out
	}
	for _, d := range spec.Secrets {
		if d.Budget != nil {
			if _, ok := d.BudgetLimit(cfg); !ok {
				out = append(out, refProblem{path: "/" + d.Budget.DailyLimitConfig, msg: "provider daily token budget must be an integer from 1 to 10000000"})
			}
		}
	}
	return out
}

// MatchFileGlob matches slash-separated repository paths. A ** segment spans zero or more directories.
func MatchFileGlob(pattern, name string) (bool, error) {
	if !fs.ValidPath(pattern) || pattern == "." || strings.Contains(pattern, "\\") {
		return false, fmt.Errorf("invalid repository glob %q", pattern)
	}
	segs := strings.Split(pattern, "/")
	parts := strings.Split(name, "/")
	for _, s := range segs {
		if strings.HasPrefix(s, ".") {
			return false, fmt.Errorf("hidden repository path in %q", pattern)
		}
		if _, e := path.Match(s, ""); e != nil {
			return false, e
		}
	}
	// Dynamic programming keeps consecutive ** segments bounded.
	matches := make([]bool, len(parts)+1)
	matches[0] = true
	for _, s := range segs {
		next := make([]bool, len(matches))
		if s == "**" {
			next[0] = matches[0]
			for j := range parts {
				next[j+1] = matches[j+1] || next[j]
			}
		} else {
			for j, p := range parts {
				ok, _ := path.Match(s, p)
				next[j+1] = matches[j] && ok
			}
		}
		matches = next
	}
	return matches[len(parts)], nil
}

func LoadInstanceFiles(dir string, patterns []string) (map[string][]byte, error) {
	const quota = 20 << 20
	var size int64
	files := map[string][]byte{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, pattern := range patterns {
		if _, err := MatchFileGlob(pattern, ""); err != nil {
			return nil, err
		}
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		for _, pat := range patterns {
			ok, _ := MatchFileGlob(pat, name)
			if !ok {
				continue
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("instance file %q must be a regular file", name)
			}
			if len(files) >= 10000 {
				return fmt.Errorf("instance exceeds 10000 files")
			}
			f, err := root.Open(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("invalid file %q", name)
			}
			if info.Size() > quota-size {
				return fmt.Errorf("instance files exceed 20 MiB")
			}
			data, err := io.ReadAll(io.LimitReader(f, quota-size+1))
			if err != nil {
				return err
			}
			size += int64(len(data))
			if size > quota {
				return fmt.Errorf("instance files exceed 20 MiB")
			}
			files[name] = data
			break
		}
		return nil
	})
	return files, err
}

func networkAllows(raw string, allow []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, h := range allow {
		s, p, ok := strings.Cut(h, ":")
		if !ok {
			p = "443"
		}
		if p != port {
			continue
		}
		if s == host || strings.HasPrefix(s, "*.") && strings.HasSuffix(host, s[1:]) {
			return true
		}
	}
	return false
}

func checkNetworkURLs(root, schema map[string]any, v any, allow []string, p string, depth int) []refProblem {
	if schema == nil || depth > 40 {
		return nil
	}
	schema = ResolveSchemaRef(root, schema)
	var problems []refProblem
	switch v := v.(type) {
	case string:
		if schema["x-cicord-network-url"] == true && !networkAllows(v, allow) {
			return []refProblem{{path: p, msg: "HTTPS URL host must appear in network.allow"}}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for k, val := range v {
			sub, _ := props[k].(map[string]any)
			problems = append(problems, checkNetworkURLs(root, sub, val, allow, p+jsonPointer(k), depth+1)...)
		}
	case []any:
		sub, _ := schema["items"].(map[string]any)
		for i, val := range v {
			problems = append(problems, checkNetworkURLs(root, sub, val, allow, fmt.Sprintf("%s/%d", p, i), depth+1)...)
		}
	}
	return problems
}
