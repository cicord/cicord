// Package storeutil holds the validation and encoding rules shared by the state
// backends (state/sqlite, state/postgres), so both implement the state contract
// with exactly the same semantics: time range and encoding, JSON validation,
// list limits, KV prefix ranges and the Increment arithmetic.
//
// Errors returned here carry no backend prefix; callers wrap them.
package storeutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/state"
)

var (
	minTime = time.Unix(0, math.MinInt64)
	maxTime = time.Unix(0, math.MaxInt64)
)

// ToNanos converts t to unix nanoseconds, rejecting times that do not fit
// (roughly before 1678 or after 2262), including the zero time.
func ToNanos(field string, t time.Time) (int64, error) {
	if t.Before(minTime) || t.After(maxTime) {
		return 0, fmt.Errorf("%s %v out of range", field, t)
	}
	return t.UnixNano(), nil
}

// FromNanos converts unix nanoseconds back to a UTC time.
func FromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }

// OrNow returns t, or now() when t is the zero time.
func OrNow(t time.Time, now func() time.Time) time.Time {
	if t.IsZero() {
		return now()
	}
	return t
}

// DueCutoff converts the now of TaskQueue.Due to unix nanoseconds, clamping a
// time after the representable range. ok is false when now is before the range
// (nothing can be due).
func DueCutoff(now time.Time) (cutoff int64, ok bool) {
	switch {
	case now.Before(minTime):
		return 0, false
	case now.After(maxTime):
		return math.MaxInt64, true
	default:
		return now.UnixNano(), true
	}
}

// CheckJSON validates an optional JSON payload: empty means absent (stored as
// NULL, returned as nil), anything else must be valid JSON.
func CheckJSON(field string, v []byte) error {
	if len(v) > 0 && !json.Valid(v) {
		return fmt.Errorf("%s is not valid JSON", field)
	}
	return nil
}

// ClampLimit returns def for n <= 0 and caps n at state.MaxListLimit.
func ClampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	return min(n, state.MaxListLimit)
}

// NotFound returns an error wrapping state.ErrNotFound.
func NotFound(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{state.ErrNotFound}, args...)...)
}

// PrefixEnd returns the smallest string greater than every string starting
// with prefix, or false when there is none (empty or all-0xFF prefix, where
// key >= prefix alone is exact). KV prefix listing scans [prefix, PrefixEnd)
// in byte order rather than using LIKE, which treats % and _ as wildcards and
// may fold case.
func PrefixEnd(prefix string) (string, bool) {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// CheckKVSet validates the arguments of KV.Set.
func CheckKVSet(key string, value []byte, ttl time.Duration) error {
	switch {
	case key == "":
		return errors.New("empty key")
	case !json.Valid(value):
		return errors.New("value is not valid JSON")
	case ttl < 0:
		return fmt.Errorf("negative ttl %v", ttl)
	}
	return nil
}

// CheckIncrement validates the arguments of KV.Increment.
func CheckIncrement(key string, by float64) error {
	switch {
	case key == "":
		return errors.New("empty key")
	case math.IsNaN(by) || math.IsInf(by, 0):
		return fmt.Errorf("invalid delta %v", by)
	}
	return nil
}

// AddNumber implements the arithmetic of KV.Increment: it adds by to the JSON
// number stored in raw (nil = missing or expired, counted as 0) and returns the
// result with its JSON encoding. A stored value that is not a JSON number, or a
// non-finite result, is an error.
func AddNumber(raw []byte, by float64) (float64, []byte, error) {
	current := 0.0
	if raw != nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, nil, fmt.Errorf("stored value: %w", err)
		}
		f, ok := v.(float64)
		if !ok {
			return 0, nil, fmt.Errorf("stored value %s is not a number", raw)
		}
		current = f
	}
	result := current + by
	encoded, err := json.Marshal(result) // rejects ±Inf
	if err != nil {
		return 0, nil, err
	}
	return result, encoded, nil
}

// Migration is one embedded schema migration, named NNNN_description.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations reads the *.sql files of dir in fsys, ordered by version.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var ms []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %q: invalid version prefix", name)
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", other, name, v)
		}
		seen[v] = name
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		ms = append(ms, Migration{Version: v, Name: name, SQL: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// Latest returns the highest version of ms (0 when empty).
func Latest(ms []Migration) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].Version
}
