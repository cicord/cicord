package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

var memberDigits = regexp.MustCompile(`[0-9]+`)

// validMemberID is the shape of a Discord user snowflake. Shorter digit runs
// (counts, votes, versions) are never treated as member IDs.
var validMemberID = regexp.MustCompile(`^[1-9][0-9]{16,19}$`)

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

// erasureMark is the SQL copy of an erasure marker. It says which work that
// mentions the member may still be pre-erasure data and must go back through
// erase instead of being delivered:
//   - settled: the erasure ran in this database. Everything that held the
//     member's data was erased then, except the Pending installations
//     (disabled, unavailable, failed or uninstalled ones). Only work of those is
//     gated. New activity of a returning member is delivered normally.
//   - unsettled: the erasure did not finish, or this database predates it (a
//     restore: the external marker is newer than the SQL one). All work that
//     mentions the member is gated, except in the installations listed in Done,
//     which were cleaned since ("" stands for the server-wide stores: private
//     replies, interop receipts and events). The first gated delivery reruns the
//     whole erasure, which settles the marker again.
type erasureMark struct {
	At      time.Time `json:"at"`
	Settled bool      `json:"settled,omitempty"`
	Pending []string  `json:"pending,omitempty"`
	Done    []string  `json:"done,omitempty"`
}

// gates reports whether work of scope that mentions the member must go back
// through erase. Scope "" is server-wide work (cached interop results, the dead
// letter event view).
func (m erasureMark) gates(scope string) bool {
	if m.Settled {
		return scope != "" && slices.Contains(m.Pending, scope)
	}
	return !slices.Contains(m.Done, scope)
}

// cleaned records that scope no longer holds pre-erasure data of the member.
func (m *erasureMark) cleaned(scope string) {
	if m.Settled {
		m.Pending = slices.DeleteFunc(m.Pending, func(s string) bool { return s == scope })
	} else if !slices.Contains(m.Done, scope) {
		m.Done = append(m.Done, scope)
		sort.Strings(m.Done)
	}
}

func parseStamp(raw []byte) (time.Time, bool) {
	var at time.Time
	if len(raw) == 0 || json.Unmarshal(raw, &at) != nil {
		return time.Time{}, false
	}
	return at, true
}

// combineMark merges the SQL marker (sqlRaw, nil when absent) with the date of
// the external one. The legacy SQL format (a bare date) is unsettled.
func combineMark(sqlRaw []byte, file time.Time, hasFile bool) (erasureMark, bool) {
	var m erasureMark
	found := false
	if len(sqlRaw) > 0 {
		if at, ok := parseStamp(sqlRaw); ok {
			m, found = erasureMark{At: at}, true
		} else if json.Unmarshal(sqlRaw, &m) == nil {
			found = true
		} else {
			m, found = erasureMark{}, true // unreadable: fail closed
		}
	}
	if hasFile && (!found || file.After(m.At)) {
		// The database predates the newest erasure of this member: a restore.
		return erasureMark{At: file}, true
	}
	return m, found
}

func (r *Runtime) saveMark(ctx context.Context, server, mark string, m erasureMark) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return r.privacyKV(server).Set(ctx, mark, raw, 0)
}

func (r *Runtime) remember(ctx context.Context, server, user string) (time.Time, error) {
	mark := r.fingerprint(server, user)
	now := time.Now().UTC()
	stamp, _ := json.Marshal(now)
	if r.opts.PrivacyDir != "" {
		dir := r.markerDirectory(server)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return now, err
		}
		// Write to a new file and rename atomically. Flush file and directory before
		// returning success, even if a crash follows the database erase.
		f, err := os.CreateTemp(dir, ".pending-")
		if err != nil {
			return now, err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(stamp); err == nil {
			err = f.Sync()
		}
		cerr := f.Close()
		if err != nil {
			return now, err
		}
		if cerr != nil {
			return now, cerr
		}
		if err = os.Rename(f.Name(), filepath.Join(dir, mark)); err != nil {
			return now, err
		}
		for _, d := range []string{dir, r.opts.PrivacyDir, filepath.Dir(r.opts.PrivacyDir)} {
			df, err := os.Open(d)
			if err != nil {
				return now, err
			}
			err = df.Sync()
			df.Close()
			if err != nil {
				return now, err
			}
		}
	}
	// Unsettled until the erasure completes: a crash in between keeps gating.
	return now, r.saveMark(ctx, server, mark, erasureMark{At: now})
}

// markers returns every erasure marker of the server, by subject HMAC.
func (r *Runtime) markers(ctx context.Context, server string) (map[string]erasureMark, error) {
	sqlMarks := map[string][]byte{}
	for cursor := ""; ; {
		entries, next, err := r.privacyKV(server).List(ctx, "", 1000, cursor)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			sqlMarks[e.Key] = e.Value
		}
		if next == "" {
			break
		}
		cursor = next
	}
	files := map[string]time.Time{}
	if r.opts.PrivacyDir != "" {
		dir := r.markerDirectory(server)
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, f := range entries {
			if len(f.Name()) != 64 {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				return nil, err
			}
			at, _ := parseStamp(raw)
			files[f.Name()] = at
		}
	}
	marks := map[string]erasureMark{}
	for key, raw := range sqlMarks {
		at, hasFile := files[key]
		if m, ok := combineMark(raw, at, hasFile); ok {
			marks[key] = m
		}
	}
	for key, at := range files {
		if _, ok := sqlMarks[key]; !ok {
			marks[key], _ = combineMark(nil, at, true)
		}
	}
	return marks, nil
}

// Walk decoded JSON strings and object keys; a replay may contain mentions,
// compound storage keys or escaped characters. Only digit runs shaped like a
// Discord snowflake count, and JSON numbers never do: a snowflake does not fit in
// a JavaScript number, so plugins always store member IDs as strings, and a
// count, vote or version that happens to equal a short ID is not a member.
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
		if v, ok := t.(string); ok {
			scan(v)
		}
	}
}

// restoreMemberData runs before an installation starts. It reapplies erase hooks
// to the installation's data for every member whose marker still gates it: the
// installation was disabled, unavailable, failed or uninstalled at erasure time,
// or the database was restored from a backup older than the erasure. Once the
// installation is clean, its scope is recorded so a later restart does not erase
// a returning member's new data again.
func (r *Runtime) restoreMemberData(ctx context.Context, server, scope string, inst *plugin.Instance) error {
	if !inst.Definition().HasPrivacy {
		return nil
	}
	marks, err := r.markers(ctx, server)
	if err != nil {
		return err
	}
	gating := map[string]erasureMark{}
	for key, m := range marks {
		if m.gates(scope) || m.gates("") {
			gating[key] = m
		}
	}
	if len(gating) == 0 {
		return nil
	}
	fence := r.privacyFence(server)
	fence.LockErase()
	defer fence.UnlockErase()
	// Members found under a marker that still gates the server-wide stores (a
	// restored database): their private replies, interop receipts and events are
	// erased here too, so the marker stops gating those stores.
	serverWide := map[string]bool{}
	// Host receipts are outside plugin KV; reapply external erasure markers to
	// restored credentials before routing any instance work.
	replies := r.opts.Store.KV("private-replies:" + server)
	for cursor := ""; ; {
		items, next, err := replies.List(ctx, "user:", 100, cursor)
		if err != nil {
			return err
		}
		for _, item := range items {
			erased := false
			subjects(item.Key, nil, func(id string) {
				if m, ok := gating[r.fingerprint(server, id)]; ok && m.gates("") {
					erased = true
					serverWide[id] = true
				}
			})
			if erased {
				if err := replies.Delete(ctx, item.Key); err != nil {
					return err
				}
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	kv, ok := r.opts.Store.KV(scope).(privacyStore)
	if !ok {
		return errors.New("privacy inventory scan unavailable")
	}
	ids := map[string]bool{}
	err = kv.VisitPrivacy(ctx, func(key string, raw json.RawMessage) error {
		subjects(key, raw, func(id string) {
			if m, ok := gating[r.fingerprint(server, id)]; ok {
				ids[id] = ids[id] || m.gates(scope)
				serverWide[id] = serverWide[id] || m.gates("")
			}
		})
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	for _, id := range sortedKeys(ids) {
		if !ids[id] {
			continue
		}
		if _, err := inst.Privacy(plugin.WithPrivacyGuard(ctx), "erase", id); err != nil {
			return fmt.Errorf("restore erase hook failed: %w", err)
		}
		if err := r.eraseTaggedIndex(ctx, scope, id); err != nil {
			return err
		}
		if _, err := kv.PurgeTasks(ctx, server, false, func(s string, raw json.RawMessage) bool {
			return s == scope && mentions(raw, id)
		}); err != nil {
			return err
		}
	}
	cleanedServer := map[string]bool{}
	for _, id := range sortedKeys(serverWide) {
		if !serverWide[id] {
			continue
		}
		for cursor := ""; ; {
			items, next, err := replies.List(ctx, "user:"+id+":", 100, cursor)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := replies.Delete(ctx, item.Key); err != nil {
					return err
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if _, err := r.PurgeInteropMember(ctx, server, id); err != nil {
			return err
		}
		cleanedServer[r.fingerprint(server, id)] = true
	}
	for key, m := range gating {
		changed := false
		if m.gates(scope) {
			m.cleaned(scope)
			changed = true
		}
		if cleanedServer[key] && m.gates("") {
			m.cleaned("")
			changed = true
		}
		if changed {
			if err := r.saveMark(ctx, server, key, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func mentions(raw json.RawMessage, user string) bool {
	match := false
	subjects("", raw, func(id string) {
		if id == user {
			match = true
		}
	})
	return match
}

func (r *Runtime) eraseTaggedIndex(ctx context.Context, scope, user string) error {
	if index, ok := r.opts.Store.Index(scope).(state.SubjectIndex); ok {
		return index.EraseSubject(ctx, user, "", "")
	}
	return nil
}

// orphanedScopes lists the plugin scopes of the server that still hold data but
// belong to no installation of the active revision (uninstalled instances).
func (r *Runtime) orphanedScopes(ctx context.Context, a *active, server string) ([]state.ScopeUsage, error) {
	purger, ok := r.privacyKV(server).(state.ScopePurger)
	if !ok {
		return nil, errors.New("storage inventory unavailable")
	}
	all, err := purger.PluginScopes(ctx, server)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	for _, i := range a.instances {
		if i.cfg.Server == server {
			installed[instanceScope(i.cfg)] = true
		}
	}
	var out []state.ScopeUsage
	for _, u := range all {
		if !installed[u.Scope] {
			out = append(out, u)
		}
	}
	return out, nil
}

// orphanName is "<bot>/<instance>" of a scope "plugin:<bot>/<instance>/<server>".
func orphanName(scope, server string) string {
	return strings.TrimSuffix(strings.TrimPrefix(scope, "plugin:"), "/"+server)
}

// memberReport runs the hooks of every installation of the server. pending
// lists the scopes that may still hold the member's data afterwards (see
// erasureMark). The partial report is returned with any error.
func (r *Runtime) memberReport(ctx context.Context, server, user, action string) (rep PrivacyReport, pending []string, err error) {
	rep = PrivacyReport{Complete: true, Plugins: []PrivacyItem{}}
	active := r.Active()
	if active == nil || active.servers[server] == nil {
		return rep, nil, errors.New("server unavailable")
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
		case i.cfg.Disabled() && (inventory == nil || len(inventory.Records) > 0):
			item.Status = "disabled"
			rep.Complete = false
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
		switch item.Status {
		case "failed", "unavailable":
			held[instanceScope(i.cfg)] = true
			pending = append(pending, instanceScope(i.cfg))
		case "disabled":
			pending = append(pending, instanceScope(i.cfg))
		}
		rep.Plugins = append(rep.Plugins, item)
	}
	// Uninstalled installations keep their data until an admin purges it. Their
	// hooks cannot run, so the report is incomplete while one mentions the member.
	orphans, err := r.orphanedScopes(ctx, active, server)
	if err != nil {
		return rep, pending, err
	}
	for _, o := range orphans {
		kv, ok := r.opts.Store.KV(o.Scope).(privacyStore)
		if !ok {
			return rep, pending, errors.New("privacy inventory scan unavailable")
		}
		found := false
		err := kv.VisitPrivacy(ctx, func(key string, raw json.RawMessage) error {
			if !found {
				subjects(key, raw, func(id string) { found = found || id == user })
			}
			return ctx.Err()
		})
		if err != nil {
			return rep, pending, err
		}
		if found {
			rep.Complete = false
			rep.Plugins = append(rep.Plugins, PrivacyItem{Instance: orphanName(o.Scope, server), Status: "orphaned"})
			pending = append(pending, o.Scope)
		}
	}
	sort.Slice(rep.Plugins, func(a, b int) bool { return rep.Plugins[a].Instance < rep.Plugins[b].Instance })
	sort.Strings(pending)
	if action == "erase" {
		// Interaction credentials live outside plugin storage and must be erased too.
		replies := r.opts.Store.KV("private-replies:" + server)
		for cursor := ""; ; {
			items, next, err := replies.List(ctx, "user:"+user+":", 100, cursor)
			if err != nil {
				return rep, pending, err
			}
			for _, item := range items {
				if err := replies.Delete(ctx, item.Key); err != nil {
					return rep, pending, err
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
		storage, ok := r.privacyKV(server).(privacyStore)
		if !ok {
			return rep, pending, errors.New("privacy task purge unavailable")
		}
		interop, hasInterop := any(r).(interface {
			PurgeInteropMember(context.Context, string, string) (int, error)
		})
		n, err := storage.PurgeTasks(ctx, server, hasInterop, func(scope string, raw json.RawMessage) bool {
			return !held[scope] && mentions(raw, user)
		})
		if err != nil {
			return rep, pending, err
		}
		rep.Tasks = n
		if hasInterop {
			n, err := interop.PurgeInteropMember(ctx, server, user)
			if err != nil {
				return rep, pending, err
			}
			rep.Interop = n
		}
	}
	return rep, pending, nil
}

// MemberData exports or erases one member's data on a server. On error, the
// partial report of what already ran is returned with it.
func (r *Runtime) MemberData(ctx context.Context, server, user, action string) (PrivacyReport, error) {
	if !ValidPrivacyMember(user) || (action != "export" && action != "erase") {
		return PrivacyReport{}, errors.New("invalid privacy request")
	}
	if action == "export" {
		r.privacyFence(server).RLock()
		defer r.privacyFence(server).RUnlock()
		rep, _, err := r.memberReport(ctx, server, user, action)
		return rep, err
	}
	r.privacyFence(server).LockErase()
	defer r.privacyFence(server).UnlockErase()
	at, err := r.remember(ctx, server, user)
	if err != nil {
		return PrivacyReport{}, err
	}
	rep, pending, err := r.memberReport(ctx, server, user, action)
	if err != nil {
		return rep, err
	}
	return rep, r.saveMark(ctx, server, r.fingerprint(server, user), erasureMark{At: at, Settled: true, Pending: pending})
}

// ReplayPrivacy suppresses delivery of restored work containing an erased subject
// and reapplies plugin cleanup. A failed cleanup is retriable.
func (r *Runtime) ReplayPrivacy(ctx context.Context, server, scope string, payload json.RawMessage) (bool, error) {
	return r.WithReplayPrivacy(ctx, server, scope, payload, nil)
}

// WithReplayPrivacy delivers stored work of scope unless it mentions a member
// whose erasure marker still gates that scope (see erasureMark). Gated work is
// not delivered: the whole erasure reruns, which settles the marker, and the work
// is dropped (erased=true), or kept for retry while scope still holds the data.
func (r *Runtime) WithReplayPrivacy(ctx context.Context, server, scope string, payload json.RawMessage, deliver func() error) (bool, error) {
	fence := r.privacyFence(server)
	for {
		fence.RLock()
		ids, _, err := r.erasedIn(ctx, server, scope, payload)
		if err != nil {
			fence.RUnlock()
			return false, err
		}
		if len(ids) == 0 {
			defer fence.RUnlock()
			if deliver != nil {
				return false, deliver()
			}
			return false, nil
		}
		fence.RUnlock()
		fence.LockErase()
		// Another delivery may have settled the marker in between.
		ids, marks, err := r.erasedIn(ctx, server, scope, payload)
		if err != nil || len(ids) == 0 {
			fence.UnlockErase()
			if err != nil {
				return false, err
			}
			continue
		}
		defer fence.UnlockErase()
		retry := false
		for _, id := range ids {
			rep, pending, err := r.memberReport(ctx, server, id, "erase")
			if err != nil {
				return true, err
			}
			key := r.fingerprint(server, id)
			if err := r.saveMark(ctx, server, key, erasureMark{At: marks[key].At, Settled: true, Pending: pending}); err != nil {
				return true, err
			}
			if scope != "" && slices.Contains(pending, scope) {
				retry = true
			}
			for _, p := range rep.Plugins {
				if scope == "" && (p.Status == "failed" || p.Status == "unavailable") {
					retry = true
				}
			}
		}
		if retry {
			return true, errPrivacyRetry
		}
		return true, nil
	}
}

// erasedIn returns the members mentioned in raw whose marker gates scope, and
// their markers by subject HMAC.
func (r *Runtime) erasedIn(ctx context.Context, server, scope string, raw json.RawMessage) ([]string, map[string]erasureMark, error) {
	candidates := map[string]bool{}
	subjects("", raw, func(id string) { candidates[id] = true })
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	batch := sortedKeys(candidates)
	marks := map[string]erasureMark{}
	// Batch membership lookups rather than enumerating historical erasures on every delivery.
	if len(candidates) > 64 {
		all, err := r.markers(ctx, server)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range batch {
			if m, ok := all[r.fingerprint(server, id)]; ok {
				marks[r.fingerprint(server, id)] = m
			}
		}
	} else {
		hashes := make([]string, len(batch))
		for n, id := range batch {
			hashes[n] = r.fingerprint(server, id)
		}
		entries, err := r.privacyKV(server).(state.AdvancedKV).Entries(ctx, hashes)
		if err != nil {
			return nil, nil, err
		}
		for n, key := range hashes {
			var file time.Time
			hasFile := false
			if r.opts.PrivacyDir != "" {
				data, err := os.ReadFile(filepath.Join(r.markerDirectory(server), key))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, nil, err
				}
				if err == nil {
					file, _ = parseStamp(data)
					hasFile = true
				}
			}
			var sqlRaw []byte
			if entries[n] != nil {
				sqlRaw = entries[n].Value
			}
			if m, ok := combineMark(sqlRaw, file, hasFile); ok {
				marks[key] = m
			}
		}
	}
	var gated []string
	for _, id := range batch {
		if m, ok := marks[r.fingerprint(server, id)]; ok && m.gates(scope) {
			gated = append(gated, id)
		}
	}
	return gated, marks, nil
}

// ErasedPayload checks a stored value of scope inside a caller's existing
// delivery guard, without acquiring the guard recursively or dispatching another
// plugin hook. Scope "" is server-wide data (cached interop results, dead events).
func (r *Runtime) ErasedPayload(ctx context.Context, server, scope string, payload json.RawMessage) (bool, error) {
	ids, _, err := r.erasedIn(ctx, server, scope, payload)
	return len(ids) > 0, err
}

// OrphanedInstance is the data an uninstalled installation left on a server.
type OrphanedInstance struct {
	Instance string `json:"instance"` // <bot>/<instance>
	state.ScopeUsage
}

// OrphanedData lists the installations of the server that are no longer in the
// active revision but still hold data.
func (r *Runtime) OrphanedData(ctx context.Context, server string) ([]OrphanedInstance, error) {
	a := r.Active()
	if a == nil || a.servers[server] == nil {
		return nil, errors.New("server unavailable")
	}
	scopes, err := r.orphanedScopes(ctx, a, server)
	if err != nil {
		return nil, err
	}
	out := make([]OrphanedInstance, 0, len(scopes))
	for _, u := range scopes {
		out = append(out, OrphanedInstance{Instance: orphanName(u.Scope, server), ScopeUsage: u})
	}
	return out, nil
}

// ErrInstalled refuses to purge the data of an installation of the active revision.
var ErrInstalled = errors.New("this installation is in the active revision: remove it from git first")

// PurgeOrphanedData deletes everything an uninstalled installation
// ("<bot>/<instance>") left on the server: records, rankings, index documents,
// tasks, event deliveries and DM receipts. It refuses an installation of the
// active revision (disabled or failing ones included) and holds the activation
// lock, so a revision that reinstalls it waits for the purge.
func (r *Runtime) PurgeOrphanedData(ctx context.Context, server, instance string) (state.ScopeUsage, error) {
	bot, name, ok := strings.Cut(instance, "/")
	if !ok || bot == "" || name == "" || strings.Contains(name, "/") {
		return state.ScopeUsage{}, errors.New("expected <bot>/<instance>")
	}
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	a := r.Active()
	if a == nil || a.servers[server] == nil {
		return state.ScopeUsage{}, errors.New("server unavailable")
	}
	scope := "plugin:" + bot + "/" + name + "/" + server
	for _, i := range a.instances {
		if instanceScope(i.cfg) == scope {
			return state.ScopeUsage{}, ErrInstalled
		}
	}
	purger, ok := r.opts.Store.KV(scope).(state.ScopePurger)
	if !ok {
		return state.ScopeUsage{}, errors.New("storage purge unavailable")
	}
	fence := r.privacyFence(server)
	fence.LockErase()
	defer fence.UnlockErase()
	id := strings.TrimPrefix(scope, "plugin:")
	usage, err := purger.PurgeScope(ctx, "dm-receipts:"+id, "channel-provisions:"+id)
	if err != nil {
		return usage, err
	}
	// The scope no longer holds anyone's data: settle the markers waiting on it.
	marks, err := r.markers(ctx, server)
	if err != nil {
		return usage, err
	}
	for key, m := range marks {
		if m.Settled && slices.Contains(m.Pending, scope) {
			m.cleaned(scope)
			if err := r.saveMark(ctx, server, key, m); err != nil {
				return usage, err
			}
		}
	}
	return usage, nil
}
