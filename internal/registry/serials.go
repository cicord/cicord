package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/cicord/cicord/internal/state"
)

// Anti-rollback: the highest signature serial seen for the official registry. Trust on
// first use: the first verified index sets it; afterwards a lower serial is refused (an
// older, validly signed index replayed by a mirror or an attacker).
//
// Every official URL (OfficialURLs: aliases and mirrors of one logical registry, signed by
// the same keys) shares ONE serial, under OfficialSerialKey: an old envelope signed for
// alias B cannot be replayed after a higher serial was seen through alias A.

// OfficialSerialKey is the serial store key of the official registry.
const OfficialSerialKey = "official"

// SerialStore remembers the highest serial per registry identity.
type SerialStore interface {
	Highest(ctx context.Context, registry string) (uint64, error) // 0 when none
	Record(ctx context.Context, registry string, serial uint64) error
}

var (
	serialsMu sync.Mutex  // also serializes check-and-record across fetches
	serials   SerialStore = &memorySerials{m: map[string]uint64{}}
)

// SetSerialStore sets where serials are kept (the runtime: its state store; the CLI: a
// file in the cache directory). The default is in memory (process lifetime).
func SetSerialStore(s SerialStore) { UseSerialStore(s) }

// UseSerialStore is SetSerialStore returning a function that restores the previous store
// (for a store that is closed when its command returns).
func UseSerialStore(s SerialStore) (restore func()) {
	serialsMu.Lock()
	defer serialsMu.Unlock()
	prev := serials
	serials = s
	return func() {
		serialsMu.Lock()
		defer serialsMu.Unlock()
		serials = prev
	}
}

// checkSerial runs verify (an official index: its envelope names one of the official
// URLs), then refuses the envelope when its serial is lower than the highest one seen for
// the official registry, whatever URL it came from or names, and records it otherwise.
func checkSerial(ctx context.Context, verify func() (Envelope, error)) error {
	env, err := verify()
	if err != nil {
		return err
	}
	serialsMu.Lock()
	defer serialsMu.Unlock()
	key := OfficialSerialKey
	min, err := serials.Highest(ctx, key)
	if err != nil {
		return fmt.Errorf("%w (cannot read the last serial seen: %w)", ErrBadSignature, err)
	}
	if env.Serial < min {
		return fmt.Errorf("%w (serial %d is older than serial %d already seen for the official registry: refusing an older index)",
			ErrBadSignature, env.Serial, min)
	}
	if env.Serial > min {
		if err := serials.Record(ctx, key, env.Serial); err != nil {
			return fmt.Errorf("record the registry serial: %w", err)
		}
	}
	return nil
}

type memorySerials struct{ m map[string]uint64 }

func (s *memorySerials) Highest(_ context.Context, r string) (uint64, error) { return s.m[r], nil }
func (s *memorySerials) Record(_ context.Context, r string, n uint64) error {
	if n > s.m[r] {
		s.m[r] = n
	}
	return nil
}

// KVSerialStore keeps serials in a state KV space (the runtime's state store, so they
// survive restarts).
func KVSerialStore(kv state.KV) SerialStore { return kvSerials{kv} }

type kvSerials struct{ kv state.KV }

func (s kvSerials) Highest(ctx context.Context, r string) (uint64, error) {
	raw, err := s.kv.Get(ctx, r)
	if errors.Is(err, state.ErrNotFound) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	var n uint64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("stored serial of %s: %w", Redact(r), err)
	}
	return n, nil
}

func (s kvSerials) Record(ctx context.Context, r string, n uint64) error {
	if cur, err := s.Highest(ctx, r); err != nil {
		return err
	} else if n <= cur {
		return nil
	}
	return s.kv.Set(ctx, r, json.RawMessage(strconv.FormatUint(n, 10)), 0)
}

// SerialKVScope is the state KV scope of SerialStore.
const SerialKVScope = "cicord:registry-serials"

// FileSerialStore keeps serials in a JSON file (the CLI, which has no state store).
func FileSerialStore(path string) SerialStore { return fileSerials{path} }

type fileSerials struct{ path string }

func (s fileSerials) read() (map[string]uint64, error) {
	m := map[string]uint64{}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return m, nil
}

func (s fileSerials) Highest(_ context.Context, r string) (uint64, error) {
	m, err := s.read()
	if err != nil {
		return 0, err
	}
	return m[r], nil
}

func (s fileSerials) Record(_ context.Context, r string, n uint64) error {
	m, err := s.read()
	if err != nil {
		return err
	}
	if n <= m[r] {
		return nil
	}
	m[r] = n
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// DefaultSerialFile is the CLI's serial file, next to the plugin cache.
func DefaultSerialFile() string {
	return filepath.Join(filepath.Dir(DefaultCacheDir()), "registry-serials.json")
}
