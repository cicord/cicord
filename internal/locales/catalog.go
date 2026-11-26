// Package locales loads a plugin's immutable message catalogues.
package locales

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/language"
)

type Catalog map[string]map[string]string

const maxBytes = 1 << 20

var tagPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
var placeholder = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

// Load confines locale files to the selected plugin and snapshots their contents.
func Load(dir, rel string) (Catalog, error) {
	if rel == "" {
		return nil, nil
	}
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("invalid locales directory %q", rel)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	sub, err := root.OpenRoot(filepath.Clean(rel))
	if err != nil {
		return nil, err
	}
	defer sub.Close()
	entries, err := sub.Open(".")
	if err != nil {
		return nil, err
	}
	defer entries.Close()
	names, err := entries.ReadDir(65)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > 64 {
		return nil, fmt.Errorf("at most 64 locales are supported")
	}
	catalog := Catalog{}
	remaining := maxBytes
	for _, f := range names {
		if f.Type()&os.ModeSymlink != 0 || !f.Type().IsRegular() || filepath.Ext(f.Name()) != ".json" {
			return nil, fmt.Errorf("expected a regular locale JSON file, got %q", f.Name())
		}
		name := strings.TrimSuffix(f.Name(), ".json")
		tag, err := language.Parse(name)
		if !tagPattern.MatchString(name) || err != nil {
			return nil, fmt.Errorf("invalid locale %q", name)
		}
		if _, duplicate := catalog[tag.String()]; duplicate {
			return nil, fmt.Errorf("duplicate locale %s", tag)
		}
		data, err := readFile(sub, f.Name(), remaining)
		if err != nil {
			return nil, err
		}
		remaining -= len(data)
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil || m == nil {
			return nil, fmt.Errorf("%s: expected a flat object of string messages", f.Name())
		}
		for k, v := range m {
			if k == "" || len(k) > 256 || len(v) > 8192 {
				return nil, fmt.Errorf("%s: oversized or empty translation key or oversized value", f.Name())
			}
		}
		catalog[tag.String()] = m
	}
	if _, ok := catalog["en"]; !ok {
		return nil, fmt.Errorf("locales requires en.json as the fallback")
	}
	return catalog, nil
}
func readFile(root *os.Root, name string, remaining int) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(remaining)+1))
	if len(b) > remaining {
		return nil, fmt.Errorf("locale catalog exceeds 1 MiB")
	}
	return b, err
}

// Text checks the exact tag then its base language for each preferred locale.
// Substitutions run in one pass so a member name cannot introduce another variable.
func (c Catalog) Text(key string, vars map[string]any, preferences ...string) string {
	value := key
	found := false
	for _, name := range append(preferences, "en") {
		if name == "" || !tagPattern.MatchString(name) {
			continue
		}
		tag, err := language.Parse(name)
		if err != nil {
			continue
		}
		base, _ := tag.Base()
		for _, l := range []string{tag.String(), base.String()} {
			if v, ok := c[l][key]; ok {
				value = v
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return placeholder.ReplaceAllStringFunc(value, func(k string) string {
		v, ok := vars[k[1:len(k)-1]]
		if !ok {
			return k
		}
		switch n := v.(type) {
		case string:
			return n
		case bool, float64:
			return fmt.Sprint(n)
		default:
			return k
		}
	})
}
