// Package locales loads a plugin's immutable message catalogues.
package locales

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// MaxText bounds the substituted text of one message. A template of 8 KiB can repeat
// a placeholder thousands of times, so the output is checked while it is built.
const MaxText = 64 << 10

// ErrTextTooLong reports a substitution whose result would exceed MaxText.
var ErrTextTooLong = fmt.Errorf("translated text exceeds %d bytes", MaxText)

// Text checks the exact tag then its base language for each preferred locale.
// Substitutions run in one pass so a member name cannot introduce another variable.
func (c Catalog) Text(key string, vars map[string]any, preferences ...string) (string, error) {
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
	if len(value) > MaxText {
		return "", ErrTextTooLong
	}
	var out strings.Builder
	last := 0
	for _, m := range placeholder.FindAllStringIndex(value, -1) {
		k := value[m[0]:m[1]]
		sub := k
		if v, ok := vars[k[1:len(k)-1]]; ok {
			switch n := v.(type) {
			case string:
				sub = n
			case bool:
				sub = fmt.Sprint(n)
			case float64:
				sub = jsNumber(n)
			}
		}
		if out.Len()+(m[0]-last)+len(sub) > MaxText {
			return "", ErrTextTooLong
		}
		out.WriteString(value[last:m[0]])
		out.WriteString(sub)
		last = m[1]
	}
	if out.Len()+len(value)-last > MaxText {
		return "", ErrTextTooLong
	}
	out.WriteString(value[last:])
	return out.String(), nil
}

// jsNumber formats n as JavaScript's String(n) does, so a plugin's {count: 1234567} reads
// "1234567", not Go's "1.234567e+06": plain decimals for 1e-6 <= |n| < 1e21, otherwise the
// shortest exponent form ("1e+21", "1.5e-7").
func jsNumber(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	case n == 0:
		return "0"
	}
	if a := math.Abs(n); a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	s := strconv.FormatFloat(n, 'e', -1, 64) // 1.5e-07
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	exp = strings.TrimLeft(exp[1:], "0")
	return mant + "e" + sign + exp
}
