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

// CheckApplied compares the migrations recorded in a database (version → file
// name) with the embedded ones. A recorded version must name the same file this
// binary embeds for that version. Otherwise the database was migrated by a
// build whose numbering differs (a renumbered or renamed migration, or a patch
// release that used a number this binary assigns to another migration), and
// skipping the embedded migration by version would silently leave the schema
// incomplete. Versions newer than every embedded migration are reported by the
// caller's downgrade check. File contents are not compared: names are stable
// across releases, while a checksum would reject databases created by earlier
// builds of a release candidate whose migration text was later amended.
func CheckApplied(applied map[int]string, ms []Migration) error {
	embedded := make(map[int]string, len(ms))
	for _, m := range ms {
		embedded[m.Version] = m.Name
	}
	latest := Latest(ms)
	versions := make([]int, 0, len(applied))
	for v := range applied {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		if v > latest {
			continue
		}
		want, ok := embedded[v]
		if !ok {
			return fmt.Errorf("database records migration %d (%q), which this binary does not have: the database was migrated by an incompatible build; restore a backup taken before that upgrade", v, applied[v])
		}
		if applied[v] != want {
			return fmt.Errorf("database records migration %d as %q, but this binary's migration %d is %q: the database was migrated by an incompatible build; restore a backup taken before that upgrade", v, applied[v], v, want)
		}
	}
	return nil
}

// Latest returns the highest version of ms (0 when empty).
func Latest(ms []Migration) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].Version
}

// ───────────────────────────────────────────────────────────── analytics

// AnalyticsRow is a validated analytics point: Bucket is unix seconds of the
// start of a UTC hour.
type AnalyticsRow struct {
	Server, Metric, Dim string
	Bucket              int64
	Value               float64
}

// CheckAnalyticsKind validates the merge kind of Analytics.Record.
func CheckAnalyticsKind(k state.AnalyticsKind) error {
	switch k {
	case state.AnalyticsCounter, state.AnalyticsGaugeMax, state.AnalyticsGaugeLast:
		return nil
	}
	return fmt.Errorf("unknown analytics kind %q", k)
}

// AnalyticsRows validates the points of Analytics.Record and converts them to
// rows: a zero Bucket is now(), buckets are truncated to the UTC hour, values
// must be finite. Rows are sorted by (server, metric, dim, bucket), stably so that
// points of the same row keep their order (a "last" merge keeps the last point):
// every writer locks rows in the same order, which rules out deadlocks between
// concurrent Records.
func AnalyticsRows(kind state.AnalyticsKind, points []state.AnalyticsPoint, now func() time.Time) ([]AnalyticsRow, error) {
	if err := CheckAnalyticsKind(kind); err != nil {
		return nil, err
	}
	rows := make([]AnalyticsRow, 0, len(points))
	for _, p := range points {
		switch {
		case p.Server == "":
			return nil, errors.New("analytics point: empty server")
		case p.Metric == "":
			return nil, errors.New("analytics point: empty metric")
		case math.IsNaN(p.Value) || math.IsInf(p.Value, 0):
			return nil, fmt.Errorf("analytics point %s: invalid value %v", p.Metric, p.Value)
		}
		b := OrNow(p.Bucket, now)
		if _, err := ToNanos("analytics bucket", b); err != nil {
			return nil, err
		}
		rows = append(rows, AnalyticsRow{Server: p.Server, Metric: p.Metric, Dim: p.Dim,
			Bucket: b.UTC().Truncate(time.Hour).Unix(), Value: p.Value})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Dim != b.Dim {
			return a.Dim < b.Dim
		}
		return a.Bucket < b.Bucket
	})
	return rows, nil
}

// ceilUnix returns the smallest unix second s with time.Unix(s, 0) >= t.
func ceilUnix(t time.Time) int64 {
	s := t.Unix()
	if t.Nanosecond() > 0 {
		s++
	}
	return s
}

// AnalyticsRange converts the [from, to) range of an analytics read to bucket
// bounds in unix seconds (lo <= bucket < hi). A zero from has no lower bound and
// a zero to no upper bound.
func AnalyticsRange(from, to time.Time) (lo, hi int64) {
	lo, hi = math.MinInt64, math.MaxInt64
	if !from.IsZero() {
		lo = ceilUnix(from)
	}
	if !to.IsZero() {
		hi = ceilUnix(to)
	}
	return lo, hi
}

// AnalyticsBefore converts the before of Analytics.Purge to unix seconds: buckets
// strictly before it are deleted.
func AnalyticsBefore(before time.Time) int64 { return ceilUnix(before) }

// FromUnix converts unix seconds back to a UTC time.
func FromUnix(s int64) time.Time { return time.Unix(s, 0).UTC() }

// CheckDay validates a "YYYY-MM-DD" day of the active-member sets (days compare as
// strings, so the format must be exact).
func CheckDay(day string) error {
	if len(day) != len("2006-01-02") {
		return fmt.Errorf("invalid day %q (want YYYY-MM-DD)", day)
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return fmt.Errorf("invalid day %q (want YYYY-MM-DD)", day)
	}
	return nil
}

// CheckActive validates the arguments of Analytics.MarkActive.
func CheckActive(server, set, day, token string) error {
	switch {
	case server == "":
		return errors.New("empty server")
	case set == "":
		return errors.New("empty set")
	case token == "":
		return errors.New("empty token")
	}
	return CheckDay(day)
}

// AnalyticsDimsLimit is the limit of Analytics.Dims: <= 0 means 100, capped at
// state.MaxListLimit.
func AnalyticsDimsLimit(n int) int { return ClampLimit(n, 100) }

// CheckAnalyticsDims validates the Dims of an analytics query.
func CheckAnalyticsDims(dims []string) error {
	if len(dims) > state.MaxListLimit {
		return fmt.Errorf("at most %d dimensions per analytics query, got %d", state.MaxListLimit, len(dims))
	}
	return nil
}

// LabelRow is a validated analytics label: SeenAt is unix nanoseconds.
type LabelRow struct {
	Server, Dim, ID, Name, Type, Color, Owner string
	Deleted                                   bool
	SeenAt                                    int64
}

// LabelRows validates the labels of AnalyticsLabels.PutLabels and converts them to
// rows: a zero SeenAt is now(). Rows are sorted by (server, dim, id) and a key given
// twice keeps its last label (one write per row, rows locked in the same order by
// every writer).
func LabelRows(labels []state.AnalyticsLabel, now func() time.Time) ([]LabelRow, error) {
	byKey := make(map[[3]string]int, len(labels))
	rows := make([]LabelRow, 0, len(labels))
	for _, l := range labels {
		switch {
		case l.Server == "":
			return nil, errors.New("analytics label: empty server")
		case l.Dim == "":
			return nil, errors.New("analytics label: empty dim")
		case l.ID == "":
			return nil, errors.New("analytics label: empty id")
		}
		seen, err := ToNanos("analytics label seen", OrNow(l.SeenAt, now))
		if err != nil {
			return nil, err
		}
		r := LabelRow{Server: l.Server, Dim: l.Dim, ID: l.ID, Name: l.Name, Type: l.Type, Color: l.Color,
			Owner: l.Owner, Deleted: l.Deleted, SeenAt: seen}
		k := [3]string{l.Server, l.Dim, l.ID}
		if i, ok := byKey[k]; ok {
			if r.Owner == "" {
				r.Owner = rows[i].Owner
			}
			rows[i] = r
			continue
		}
		byKey[k] = len(rows)
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.Dim != b.Dim {
			return a.Dim < b.Dim
		}
		return a.ID < b.ID
	})
	return rows, nil
}

// CheckLabelIDs validates the ids of AnalyticsLabels.GetLabels.
func CheckLabelIDs(ids []string) error {
	if len(ids) > state.MaxListLimit {
		return fmt.Errorf("at most %d ids per call, got %d", state.MaxListLimit, len(ids))
	}
	return nil
}
