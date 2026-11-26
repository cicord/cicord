package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

var memberDigits = regexp.MustCompile(`[0-9]+`)
var validMemberID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func ValidPrivacyMember(id string) bool { return validMemberID.MatchString(id) }

type PrivacyItem struct {
	Instance string                 `json:"instance"`
	Status   string                 `json:"status"`
	Records  []config.PrivacyRecord `json:"records,omitempty"`
	Data     json.RawMessage        `json:"data,omitempty"`
}
type PrivacyReport struct {
	Complete bool          `json:"complete"`
	Plugins  []PrivacyItem `json:"plugins"`
	Tasks    int           `json:"tasksErased"`
	Interop  int           `json:"interopErased"`
}

type privacyStore interface {
	VisitPrivacy(context.Context, func(string, json.RawMessage) error) error
	PurgeTasks(context.Context, string, bool, func(string, json.RawMessage) bool) (int, error)
}

func (r *Runtime) privacyFence(server string) *plugin.PrivacyFence {
	if v, ok := r.privacyFences.Load(server); ok {
		return v.(*plugin.PrivacyFence)
	}
	v, _ := r.privacyFences.LoadOrStore(server, new(plugin.PrivacyFence))
	return v.(*plugin.PrivacyFence)
}

var errPrivacyRetry = errors.New("privacy hook needs retry")

func (r *Runtime) privacyKV(server string) state.KV { return r.opts.Store.KV("privacy:v1:" + server) }
func (r *Runtime) fingerprint(server, user string) string {
	return r.opts.PrivacySigner.PrivacyDigest("erasure", server, user)
}
func (r *Runtime) Pseudonym(server, user string) string {
	return "deleted-user-" + r.opts.PrivacySigner.PrivacyDigest("pseudonym", server, user)[:16]
}

// markerDirectory is outside SQL backups. Only a server hash, subject HMAC and
// date are retained. Database restores must preserve the newest directory and key.
func (r *Runtime) markerDirectory(server string) string {
	return filepath.Join(r.opts.PrivacyDir, r.opts.PrivacySigner.PrivacyDigest("directory", server, ""))
}
func (r *Runtime) remember(ctx context.Context, server, user string) error {
	mark := r.fingerprint(server, user)
	stamp, _ := json.Marshal(time.Now().UTC())
	if r.opts.PrivacyDir != "" {
		dir := r.markerDirectory(server)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		// Write to a new file and rename atomically. Flush file and directory before
		// returning success, even if a crash follows the database erase.
		f, err := os.CreateTemp(dir, ".pending-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(stamp); err == nil {
			err = f.Sync()
		}
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		if err = os.Rename(f.Name(), filepath.Join(dir, mark)); err != nil {
			return err
		}
		for _, d := range []string{dir, r.opts.PrivacyDir, filepath.Dir(r.opts.PrivacyDir)} {
			df, err := os.Open(d)
			if err != nil {
				return err
			}
			err = df.Sync()
			df.Close()
			if err != nil {
				return err
			}
		}
	}
	return r.privacyKV(server).Set(ctx, mark, stamp, 0)
}

func (r *Runtime) markers(ctx context.Context, server string) (map[string]bool, error) {
	marks := map[string]bool{}
	for cursor := ""; ; {
		entries, next, err := r.privacyKV(server).List(ctx, "", 1000, cursor)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			marks[e.Key] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if r.opts.PrivacyDir != "" {
		files, err := os.ReadDir(r.markerDirectory(server))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, f := range files {
			if len(f.Name()) == 64 {
				marks[f.Name()] = true
			}
		}
	}
	return marks, nil
}

// Walk decoded JSON strings and keys; a replay may contain mentions, compound
// storage keys, escaped characters, or a numeric ID in old plugin records.
func subjects(key string, payload json.RawMessage, visit func(string)) {
	scan := func(s string) {
		for _, id := range memberDigits.FindAllString(s, -1) {
			if ValidPrivacyMember(id) {
				visit(id)
			}
		}
	}
	scan(key)
	if len(payload) == 0 {
		return
	}
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.UseNumber()
	for {
		t, err := dec.Token()
		if err != nil {
			return
		}
		switch v := t.(type) {
		case string:
			scan(v)
		case json.Number:
			scan(string(v))
		}
	}
}

func (r *Runtime) restoreMemberData(ctx context.Context, server, scope string, inst *plugin.Instance) error {
	if !inst.Definition().HasPrivacy {
		return nil
	}
	marks, err := r.markers(ctx, server)
	if err != nil || len(marks) == 0 {
		return err
	}
	fence := r.privacyFence(server)
	fence.LockErase()
	defer fence.UnlockErase()
	kv, ok := r.opts.Store.KV(scope).(privacyStore)
	if !ok {
		return errors.New("privacy inventory scan unavailable")
	}
	ids := map[string]bool{}
	err = kv.VisitPrivacy(ctx, func(key string, raw json.RawMessage) error {
		subjects(key, raw, func(id string) {
			if marks[r.fingerprint(server, id)] {
				ids[id] = true
			}
		})
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	for _, id := range sortedKeys(ids) {
		if _, err := inst.Privacy(plugin.WithPrivacyGuard(ctx), "erase", id); err != nil {
			return fmt.Errorf("restore erase hook failed: %w", err)
		}
		if err := r.eraseTaggedIndex(ctx, scope, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) eraseTaggedIndex(ctx context.Context, scope, user string) error {
	if index, ok := r.opts.Store.Index(scope).(state.SubjectIndex); ok {
		return index.EraseSubject(ctx, user, "", "")
	}
	return nil
}

func (r *Runtime) memberReport(ctx context.Context, server, user, action string) (PrivacyReport, error) {
	rep := PrivacyReport{Complete: true, Plugins: []PrivacyItem{}}
	active := r.Active()
	if active == nil || active.servers[server] == nil {
		return rep, errors.New("server unavailable")
	}
	held := map[string]bool{}
	for _, i := range active.instances {
		if i.cfg.Server != server {
			continue
		}
		item := PrivacyItem{Instance: i.cfg.Bot + "/" + i.cfg.Name}
		inst := i.inst()
		inventory := i.cfg.Plugin.Manifest.Spec.Privacy
		if inventory != nil {
			item.Records = inventory.Records
		}
		switch {
		case i.plugin == nil || (inst == nil && inventory != nil && len(inventory.Records) > 0):
			item.Status = "unavailable"
			rep.Complete = false
		case !i.plugin.def.HasPrivacy:
			if inventory != nil && len(inventory.Records) == 0 {
				item.Status = "no-member-data"
			} else {
				item.Status = "undeclared"
				rep.Complete = false
			}
		case inst == nil:
			item.Status = "unavailable"
			rep.Complete = false
		default:
			data, err := inst.Privacy(plugin.WithPrivacyGuard(ctx), action, user)
			if err == nil && action == "erase" {
				err = r.eraseTaggedIndex(ctx, instanceScope(i.cfg), user)
			}
			if err != nil {
				item.Status = "failed"
				rep.Complete = false
			} else {
				item.Status = "ok"
				item.Data = data
			}
		}
		if item.Status == "failed" || item.Status == "unavailable" {
			held[instanceScope(i.cfg)] = true
		}
		rep.Plugins = append(rep.Plugins, item)
	}
	sort.Slice(rep.Plugins, func(a, b int) bool { return rep.Plugins[a].Instance < rep.Plugins[b].Instance })
	if action == "erase" {
		storage, ok := r.privacyKV(server).(privacyStore)
		if !ok {
			return rep, errors.New("privacy task purge unavailable")
		}
		interop, hasInterop := any(r).(interface {
			PurgeInteropMember(context.Context, string, string) (int, error)
		})
		n, err := storage.PurgeTasks(ctx, server, hasInterop, func(scope string, raw json.RawMessage) bool {
			if held[scope] {
				return false
			}
			match := false
			subjects("", raw, func(id string) {
				if id == user {
					match = true
				}
			})
			return match
		})
		if err != nil {
			return rep, err
		}
		rep.Tasks = n
		if hasInterop {
			n, err := interop.PurgeInteropMember(ctx, server, user)
			if err != nil {
				return rep, err
			}
			rep.Interop = n
		}
	}
	return rep, nil
}

func (r *Runtime) MemberData(ctx context.Context, server, user, action string) (PrivacyReport, error) {
	if !ValidPrivacyMember(user) || (action != "export" && action != "erase") {
		return PrivacyReport{}, errors.New("invalid privacy request")
	}
	if action == "erase" {
		r.privacyFence(server).LockErase()
		defer r.privacyFence(server).UnlockErase()
		if err := r.remember(ctx, server, user); err != nil {
			return PrivacyReport{}, err
		}
	} else {
		r.privacyFence(server).RLock()
		defer r.privacyFence(server).RUnlock()
	}
	return r.memberReport(ctx, server, user, action)
}

// ReplayPrivacy suppresses delivery of restored work containing an erased subject
// and reapplies plugin cleanup. A failed cleanup is retriable.
func (r *Runtime) ReplayPrivacy(ctx context.Context, server string, payload json.RawMessage) (bool, error) {
	return r.WithReplayPrivacy(ctx, server, payload, nil)
}

func (r *Runtime) WithReplayPrivacy(ctx context.Context, server string, payload json.RawMessage, deliver func() error) (bool, error) {
	r.privacyFence(server).RLock()
	ids, err := r.erasedIn(ctx, server, payload)
	if err != nil {
		r.privacyFence(server).RUnlock()
		return false, err
	}
	if len(ids) == 0 {
		defer r.privacyFence(server).RUnlock()
		if deliver != nil {
			return false, deliver()
		}
		return false, nil
	}
	r.privacyFence(server).RUnlock()
	r.privacyFence(server).LockErase()
	defer r.privacyFence(server).UnlockErase()
	for _, id := range ids {
		rep, err := r.memberReport(ctx, server, id, "erase")
		if err != nil {
			return true, err
		}
		for _, p := range rep.Plugins {
			if p.Status == "failed" || p.Status == "unavailable" {
				return true, errPrivacyRetry
			}
		}
	}
	return true, nil
}
func (r *Runtime) erasedIn(ctx context.Context, server string, raw json.RawMessage) ([]string, error) {
	candidates := map[string]bool{}
	subjects("", raw, func(id string) { candidates[id] = true })
	if len(candidates) == 0 {
		return nil, nil
	}
	// Batch membership lookups rather than enumerating historical erasures on every delivery.
	var marked []string
	if len(candidates) > 64 {
		marks, err := r.markers(ctx, server)
		if err != nil {
			return nil, err
		}
		for _, id := range sortedKeys(candidates) {
			if marks[r.fingerprint(server, id)] {
				marked = append(marked, id)
			}
		}
		return marked, nil
	}
	kv := r.privacyKV(server)
	batch := sortedKeys(candidates)
	hashes := make([]string, len(batch))
	for n, id := range batch {
		hashes[n] = r.fingerprint(server, id)
	}
	entries, err := kv.(state.AdvancedKV).Entries(ctx, hashes)
	if err != nil {
		return nil, err
	}
	for n, id := range batch {
		found := entries[n] != nil
		if !found && r.opts.PrivacyDir != "" {
			_, err := os.Stat(filepath.Join(r.markerDirectory(server), hashes[n]))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			found = err == nil
		}
		if found {
			marked = append(marked, id)
		}
	}

	return marked, nil
}

// ErasedPayload checks a cached value inside a caller's existing delivery guard,
// without acquiring the guard recursively or dispatching another plugin hook.
func (r *Runtime) ErasedPayload(ctx context.Context, server string, payload json.RawMessage) (bool, error) {
	ids, err := r.erasedIn(ctx, server, payload)
	return len(ids) > 0, err
}
