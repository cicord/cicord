package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// Collect the provider's read/write set. Commit verifies the live inputs, applies
// the resulting values, and acknowledges the action in the same transaction.
type actionBuffer struct {
	mu   sync.Mutex
	base state.KV
	seen map[string]*state.KVWrite
}

func newActionBuffer(base state.KV) *actionBuffer {
	return &actionBuffer{base: base, seen: map[string]*state.KVWrite{}}
}
func (b *actionBuffer) entry(ctx context.Context, k string) (*state.KVWrite, error) {
	if v := b.seen[k]; v != nil {
		return v, nil
	}
	if len(b.seen) >= 4096 {
		return nil, errors.New("action KV limit exceeded")
	}
	v, err := b.base.Get(ctx, k)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	e := &state.KVWrite{Key: k, Expected: v, Value: v, KeepTTL: true}
	b.seen[k] = e
	return e, nil
}
func (b *actionBuffer) Get(ctx context.Context, k string) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, err := b.entry(ctx, k)
	if err != nil {
		return nil, err
	}
	if e.Value == nil {
		return nil, state.ErrNotFound
	}
	return e.Value, nil
}
func (b *actionBuffer) Set(ctx context.Context, k string, v json.RawMessage, ttl time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, err := b.entry(ctx, k)
	if err != nil {
		return err
	}
	e.Value, e.Changed, e.TTL, e.KeepTTL = v, true, ttl, false
	return nil
}
func (b *actionBuffer) Delete(ctx context.Context, k string) error { return b.Set(ctx, k, nil, 0) }
func (b *actionBuffer) Increment(ctx context.Context, k string, by float64) (float64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, err := b.entry(ctx, k)
	if err != nil {
		return 0, err
	}
	var n float64
	if e.Value != nil && (string(e.Value) == "null" || json.Unmarshal(e.Value, &n) != nil) {
		return 0, errors.New("increment requires a number")
	}
	n += by
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, errors.New("nonfinite increment")
	}
	e.Value, err = json.Marshal(n)
	e.Changed = true
	return n, err
}
func (b *actionBuffer) List(ctx context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit = min(max(limit, 1), state.MaxListLimit)
	keys := map[string]json.RawMessage{}
	for k, e := range b.seen {
		if k > cursor && strings.HasPrefix(k, prefix) && e.Value != nil {
			keys[k] = e.Value
		}
	}
	after := cursor
	for {
		page, next, err := b.base.List(ctx, prefix, limit+1, after)
		if err != nil {
			return nil, "", err
		}
		for _, item := range page {
			if e, ok := b.seen[item.Key]; ok && e.Changed {
				continue
			}
			if len(b.seen) >= 4096 {
				return nil, "", errors.New("action KV limit exceeded")
			}
			keys[item.Key] = item.Value
			b.seen[item.Key] = &state.KVWrite{Key: item.Key, Value: item.Value, Expected: item.Value, KeepTTL: true}
		}
		if next == "" {
			break
		}
		count := 0
		for k := range keys {
			if k <= next {
				count++
			}
		}
		if count > limit {
			break
		}
		after = next
	}
	ordered := sortedKeys(keys)
	next := ""
	if len(ordered) > limit {
		ordered = ordered[:limit]
		next = ordered[limit-1]
	}
	out := make([]state.KVItem, 0, len(ordered))
	for _, k := range ordered {
		out = append(out, state.KVItem{Key: k, Value: keys[k]})
	}
	return out, next, nil
}
func (b *actionBuffer) writes() []state.KVWrite {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]state.KVWrite, 0, len(b.seen))
	for _, k := range sortedKeys(b.seen) {
		v := *b.seen[k]
		v.Value = slices.Clone(v.Value)
		out = append(out, v)
	}
	return out
}
