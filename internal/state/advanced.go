package state

import (
	"context"
	"encoding/json"
	"time"
)

// AdvancedKV extends the original KV interface implemented by both SQL backends.
type AdvancedKV interface {
	KV
	Entries(context.Context, []string) ([]*KVEntry, error)
	Atomic(context.Context, []KVCheck, []KVMutation) (KVCommit, error)
	Rank(context.Context, RankOp) (any, error)
}

// AtomicScope makes all writes, including ranking changes and action receipts,
// visible together. The callback must use the supplied KV and finish before ctx expires.
type AtomicScope interface {
	WithAtomicScope(ctx context.Context, scope string, fn func(KV) error) error
}

type KVEntry struct {
	Value   json.RawMessage `json:"value"`
	Version string          `json:"version"`
}
type KVCheck struct {
	Key     string  `json:"key"`
	Version *string `json:"version"`
}
type KVMutation struct {
	VersionToken string          `json:"-"` // host-generated stable token for optimistic action previews
	Op           string          `json:"op"`
	Name         string          `json:"name"`
	Score        float64         `json:"score"`
	Key          string          `json:"key"`
	Value        json.RawMessage `json:"value"`
	TTL          time.Duration   `json:"-"`
	By           float64         `json:"by"`
}
type KVCommit struct {
	OK     bool    `json:"ok"`
	Number float64 `json:"-"`
}
type RankOp struct {
	Name   string  `json:"name"`
	Method string  `json:"method"`
	Member string  `json:"member"`
	Score  float64 `json:"score"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
	Radius int     `json:"radius"`
}
type RankedMember struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
}
