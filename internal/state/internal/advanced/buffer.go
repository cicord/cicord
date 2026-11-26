package advanced

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// Buffer evaluates short action programs without keeping a SQL transaction across a JS
// await. Each observation is verified again during flush, under the scope's writer lock.
// Previews replay earlier writes inside a transaction that is always rolled back.
type bufferStep struct {
	run    func(context.Context, state.AdvancedKV) (any, error)
	digest [32]byte
	write  bool
}
type buffer struct {
	base  Space
	mu    sync.Mutex
	steps []bufferStep
	bytes int
}

var errPreview = errors.New("discard action preview")
var errConflict = errors.New("action storage changed before commit")

func (s Space) Buffer() (state.KV, func(context.Context, state.KV) error) {
	b := &buffer{base: s}
	return b, func(ctx context.Context, target state.KV) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, step := range b.steps {
			if err := step.verify(ctx, target.(state.AdvancedKV)); err != nil {
				return err
			}
		}
		return nil
	}
}

func observed(value any) ([]byte, error) {
	if c, ok := value.(state.KVCommit); ok {
		return json.Marshal([]any{c.OK, c.Number})
	}
	return json.Marshal(value)
}
func (st bufferStep) verify(ctx context.Context, kv state.AdvancedKV) error {
	value, err := st.run(ctx, kv)
	if err != nil {
		return err
	}
	raw, err := observed(value)
	if err != nil {
		return err
	}
	if sha256.Sum256(raw) != st.digest {
		return errConflict
	}
	return nil
}

func buffered[T any](ctx context.Context, b *buffer, write bool, requestBytes int, fn func(context.Context, state.AdvancedKV) (T, error)) (T, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result T
	if len(b.steps) >= 128 || b.bytes+requestBytes > 4<<20 {
		return result, errors.New("action storage operation budget exceeded")
	}
	preview := func(k state.KV) error {
		kv := k.(state.AdvancedKV)
		for _, step := range b.steps {
			if step.write {
				if err := step.verify(ctx, kv); err != nil {
					return err
				}
			}
		}
		var err error
		result, err = fn(ctx, kv)
		if err != nil {
			return err
		}
		return errPreview
	}
	err := b.base.Transaction(ctx, preview)
	if !errors.Is(err, errPreview) {
		return result, err
	}
	raw, err := observed(result)
	if err != nil {
		return result, err
	}
	b.steps = append(b.steps, bufferStep{write: write, digest: sha256.Sum256(raw), run: func(ctx context.Context, kv state.AdvancedKV) (any, error) { return fn(ctx, kv) }})
	b.bytes += requestBytes
	return result, nil
}

func (b *buffer) Entries(ctx context.Context, keys []string) ([]*state.KVEntry, error) {
	n := 0
	for _, k := range keys {
		n += len(k)
	}
	return buffered(ctx, b, false, n, func(ctx context.Context, k state.AdvancedKV) ([]*state.KVEntry, error) { return k.Entries(ctx, keys) })
}
func (b *buffer) Get(ctx context.Context, key string) (json.RawMessage, error) {
	entries, err := b.Entries(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	if entries[0] == nil {
		return nil, state.ErrNotFound
	}
	return entries[0].Value, nil
}
func (b *buffer) Atomic(ctx context.Context, checks []state.KVCheck, ops []state.KVMutation) (state.KVCommit, error) {
	if len(checks)+len(ops) > 128 {
		return state.KVCommit{}, errors.New("kv.atomic: too many operations")
	}
	n := 0
	for _, c := range checks {
		n += len(c.Key)
		if c.Version != nil {
			n += len(*c.Version)
		}
	}
	stable := append([]state.KVMutation(nil), ops...)
	for i, op := range stable {
		var token [16]byte
		rand.Read(token[:])
		stable[i].VersionToken = hex.EncodeToString(token[:])
		n += len(op.Key) + len(op.Name) + len(op.Value)
	}
	return buffered(ctx, b, true, n, func(ctx context.Context, k state.AdvancedKV) (state.KVCommit, error) {
		return k.Atomic(ctx, checks, stable)
	})
}
func (b *buffer) Set(ctx context.Context, key string, v json.RawMessage, ttl time.Duration) error {
	_, err := b.Atomic(ctx, nil, []state.KVMutation{{Op: "set", Key: key, Value: v, TTL: ttl}})
	return err
}
func (b *buffer) Delete(ctx context.Context, key string) error {
	_, err := b.Atomic(ctx, nil, []state.KVMutation{{Op: "delete", Key: key}})
	return err
}
func (b *buffer) Increment(ctx context.Context, key string, by float64) (float64, error) {
	r, err := b.Atomic(ctx, nil, []state.KVMutation{{Op: "increment", Key: key, By: by}})
	return r.Number, err
}
func (b *buffer) Rank(ctx context.Context, op state.RankOp) (any, error) {
	write := op.Method == "set" || op.Method == "add" || op.Method == "remove"
	return buffered(ctx, b, write, len(op.Name)+len(op.Member), func(ctx context.Context, k state.AdvancedKV) (any, error) { return k.Rank(ctx, op) })
}
func (b *buffer) List(ctx context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	type page struct {
		Items []state.KVItem
		Next  string
	}
	p, err := buffered(ctx, b, false, len(prefix)+len(cursor), func(ctx context.Context, k state.AdvancedKV) (page, error) {
		items, next, err := k.List(ctx, prefix, limit, cursor)
		return page{items, next}, err
	})
	return p.Items, p.Next, err
}

// CompleteInterop is deliberately available only to the host on a scoped SQL transaction.
func (s Space) CompleteInterop(ctx context.Context, id, owner string, result json.RawMessage) error {
	if s.tx == nil {
		return errors.New("action receipt requires an active transaction")
	}
	res, err := s.tx.ExecContext(ctx, s.query("UPDATE interop_actions SET status='done', result=?1, owner='' WHERE id=?2 AND owner=?3 AND status='running'"), s.bytes(string(result)), id, owner)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("action receipt ownership changed")
	}
	return nil
}
