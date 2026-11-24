package analytics

import (
	"sort"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// buffer accumulates points between flushes, merged per (server, metric, dim, hour)
// according to their kind.
type buffer struct {
	kinds map[state.AnalyticsKind]map[bufKey]float64
	n     int
}

type bufKey struct {
	server, metric, dim string
	bucket              int64 // unix seconds of the hour
}

func newBuffer() buffer {
	return buffer{kinds: map[state.AnalyticsKind]map[bufKey]float64{
		state.AnalyticsCounter: {}, state.AnalyticsGaugeMax: {}, state.AnalyticsGaugeLast: {},
	}}
}

// add merges a point; it returns false when the buffer is full and the point is new.
func (b *buffer) add(kind state.AnalyticsKind, server, metric, dim string, at time.Time, v float64) bool {
	k := bufKey{server: server, metric: metric, dim: dim, bucket: hourOf(at).Unix()}
	m := b.kinds[kind]
	old, ok := m[k]
	if !ok {
		if b.n >= maxBufferedPoints {
			return false
		}
		b.n++
		m[k] = v
		return true
	}
	m[k] = merge(kind, old, v)
	return true
}

func merge(kind state.AnalyticsKind, old, v float64) float64 {
	switch kind {
	case state.AnalyticsCounter:
		return old + v
	case state.AnalyticsGaugeMax:
		return max(old, v)
	}
	return v
}

// take empties the buffer and returns its points per kind, the counter dimensions
// capped per (server, metric, hour).
func (b *buffer) take() map[state.AnalyticsKind][]state.AnalyticsPoint {
	out := make(map[state.AnalyticsKind][]state.AnalyticsPoint, len(b.kinds))
	for kind, m := range b.kinds {
		if len(m) == 0 {
			continue
		}
		if kind == state.AnalyticsCounter {
			m = capDims(m)
		}
		pts := make([]state.AnalyticsPoint, 0, len(m))
		for k, v := range m {
			pts = append(pts, state.AnalyticsPoint{Server: k.server, Metric: k.metric, Dim: k.dim, Bucket: time.Unix(k.bucket, 0).UTC(), Value: v})
		}
		sort.Slice(pts, func(i, j int) bool { return pointLess(pts[i], pts[j]) })
		out[kind] = pts
		b.kinds[kind] = map[bufKey]float64{}
	}
	b.n = 0
	return out
}

// restore merges points that failed to be written back into the buffer. A gauge-last
// value written since is newer and wins.
func (b *buffer) restore(kind state.AnalyticsKind, pts []state.AnalyticsPoint) {
	m := b.kinds[kind]
	for _, p := range pts {
		k := bufKey{server: p.Server, metric: p.Metric, dim: p.Dim, bucket: p.Bucket.Unix()}
		old, ok := m[k]
		switch {
		case !ok:
			if b.n >= maxBufferedPoints {
				continue
			}
			b.n++
			m[k] = p.Value
		case kind == state.AnalyticsGaugeLast:
			// keep the newer value
		default:
			m[k] = merge(kind, old, p.Value)
		}
	}
}

// capDims keeps, per (server, metric, hour), the maxDimsPerFlush-1 largest dimensions
// and sums the others into OtherDim.
func capDims(m map[bufKey]float64) map[bufKey]float64 {
	type group struct {
		server, metric string
		bucket         int64
	}
	groups := map[group][]bufKey{}
	for k := range m {
		if k.dim == "" {
			continue
		}
		g := group{k.server, k.metric, k.bucket}
		groups[g] = append(groups[g], k)
	}
	for g, keys := range groups {
		if len(keys) <= maxDimsPerFlush {
			continue
		}
		sort.Slice(keys, func(i, j int) bool {
			if m[keys[i]] != m[keys[j]] {
				return m[keys[i]] > m[keys[j]]
			}
			return keys[i].dim < keys[j].dim
		})
		other := bufKey{server: g.server, metric: g.metric, dim: OtherDim, bucket: g.bucket}
		for _, k := range keys[maxDimsPerFlush-1:] {
			if k == other {
				continue
			}
			m[other] += m[k]
			delete(m, k)
		}
	}
	return m
}

func pointLess(a, b state.AnalyticsPoint) bool {
	if a.Server != b.Server {
		return a.Server < b.Server
	}
	if a.Metric != b.Metric {
		return a.Metric < b.Metric
	}
	if a.Dim != b.Dim {
		return a.Dim < b.Dim
	}
	return a.Bucket.Before(b.Bucket)
}
