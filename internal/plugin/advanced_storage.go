package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func randomValues(_ *Instance, _ *invocation, args json.RawMessage) (any, error) {
	n, err := decode[int](args)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > 65536 {
		return nil, argErr("crypto: byte length must be 0–65536")
	}
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b), nil
}

func advancedOp(method string) asyncOp {
	return func(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require(capKV); err != nil {
			return nil, err
		}
		scope := i.env.KV
		if routed, ok := any(i).(interface{ invocationKV(*invocation) state.KV }); ok {
			scope = routed.invocationKV(inv)
		}
		kv, ok := scope.(state.AdvancedKV)
		if !ok {
			return nil, errors.New("advanced storage unavailable")
		}
		switch method {
		case "entries":
			keys, err := decode[[]string](args)
			if err != nil {
				return nil, err
			}
			for _, k := range keys {
				if k == "" || len(k) > maxKVKey {
					return nil, argErr("kv: invalid key length")
				}
			}
			return func(ctx context.Context) (any, error) {
				entries, err := kv.Entries(ctx, keys)
				if err != nil {
					return nil, err
				}
				size := 0
				for _, e := range entries {
					if e != nil {
						size += len(e.Value)
					}
				}
				if size > maxKVListBytes {
					return nil, argErr("kv.getMany: result exceeds 4 MiB")
				}
				return entries, nil
			}, nil
		case "rank":
			op, err := decode[state.RankOp](args)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { return kv.Rank(ctx, op) }, nil
		default:
			type wireOp struct {
				state.KVMutation
				TTLSeconds float64 `json:"ttlSeconds"`
			}
			a, err := decode[struct {
				Checks []state.KVCheck `json:"checks"`
				Ops    []wireOp        `json:"ops"`
			}](args)
			if err != nil {
				return nil, err
			}
			for _, c := range a.Checks {
				if c.Key == "" || len(c.Key) > maxKVKey {
					return nil, argErr("kv.atomic: invalid check key")
				}
			}
			ops := make([]state.KVMutation, len(a.Ops))
			for n, o := range a.Ops {
				if o.Key == "" || len(o.Key) > maxKVKey || len(o.Value) > maxKVValue || o.TTLSeconds < 0 || o.TTLSeconds >= float64(math.MaxInt64)/float64(time.Second) {
					return nil, argErr("kv.atomic: invalid mutation")
				}
				ops[n] = o.KVMutation
				ops[n].TTL = time.Duration(o.TTLSeconds * float64(time.Second))
			}
			return func(ctx context.Context) (any, error) { return kv.Atomic(ctx, a.Checks, ops) }, nil
		}
	}
}
