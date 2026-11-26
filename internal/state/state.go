// Package state is the runtime's persistent store. Two backends implement it:
// SQLite in the data dir (package state/sqlite, the default) and PostgreSQL
// (package state/postgres). Both pass the conformance suite in state/statetest,
// which is the executable form of the contract documented here.
//
// Configuration lives in git; everything here is runtime state or community data:
// the resource map (YAML key ↔ Discord ID), plugin KV storage, scheduled tasks,
// the audit log, sync history, API sessions and activity analytics. See ADR-0003 and ADR-0004.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("state: not found")

// ErrTaskIDConflict is returned by TaskQueue.Schedule when the task ID is already
// used by a task of another scope. Task IDs are global (Complete takes only an
// ID), so one scope must not be able to replace another scope's task.
var ErrTaskIDConflict = errors.New("state: task id already used by another scope")

// Page sizes shared by every backend: a limit <= 0 means the default, and every
// limit is capped at MaxListLimit.
const (
	DefaultKVListLimit = 100
	DefaultDueLimit    = 100
	DefaultAuditLimit  = 50
	DefaultSyncLimit   = 50
	MaxListLimit       = 1000
)

// Store groups all state. Implementations are safe for concurrent use.
//
// Times are stored with nanosecond precision and returned in UTC; times outside
// the int64 unix-nanosecond range (roughly before 1678 or after 2262) are
// rejected. JSON payloads are returned byte for byte as stored.
type Store interface {
	Resources() ResourceMap
	// KV returns the key-value space of a scope, e.g. "plugin:<bot>/<instance>/<server>".
	KV(scope string) KV
	Index(scope string) FullTextIndex
	Tasks() TaskQueue
	Audit() AuditLog
	Syncs() SyncLog
	Sessions() SessionStore
	// Analytics returns the server activity analytics (hourly aggregates and daily
	// distinct-member tokens, analytics.go).
	Analytics() Analytics
	// Sweep deletes expired KV entries and sessions and returns how many rows it
	// removed. Expired rows are already invisible to readers (and removed lazily
	// when read); the runtime calls Sweep periodically to reclaim the rest.
	Sweep(ctx context.Context) (int64, error)
	Close() error
}

// ───────────────────────────────────────────────────────────── resource map

type ResourceKind string

const (
	KindRole     ResourceKind = "role"
	KindCategory ResourceKind = "category"
	KindChannel  ResourceKind = "channel"
	// Server-level keyed resources (Discord parity).
	KindAutoMod ResourceKind = "automod"
	KindEmoji   ResourceKind = "emoji"
	KindSticker ResourceKind = "sticker"
	KindSound   ResourceKind = "sound"
)

// OwnerGit marks resources declared in the server YAML. Plugin-owned resources use
// "plugin:<bot>/<instance>".
const OwnerGit = "git"

type Resource struct {
	Server    string // server key
	Kind      ResourceKind
	Key       string // YAML key (for plugin-owned resources: a plugin-chosen key)
	ID        string // Discord ID
	Owner     string
	UpdatedAt time.Time
}

type ResourceMap interface {
	Get(ctx context.Context, server string, kind ResourceKind, key string) (Resource, error) // ErrNotFound
	GetByID(ctx context.Context, server, id string) (Resource, error)                        // ErrNotFound
	// Put inserts or replaces the resource identified by (Server, Kind, Key).
	Put(ctx context.Context, r Resource) error
	Delete(ctx context.Context, server string, kind ResourceKind, key string) error
	// List returns all resources of a server, ordered by kind then key.
	List(ctx context.Context, server string) ([]Resource, error)
}

// ───────────────────────────────────────────────────────────── KV

type KVItem struct {
	Key   string
	Value json.RawMessage
}

// KV is one scope's key-value space.
//
// Keys are arbitrary non-empty byte strings (they may contain NUL or invalid
// UTF-8) and are compared and listed in byte order. Values must be valid JSON;
// ttl < 0 is rejected. Expired keys are invisible to Get, List and Increment.
// Increment on a live key keeps its TTL; on a missing or expired key it starts
// from 0 with no TTL. The stored value must be a JSON number and the result
// must be finite.
type KV interface {
	Get(ctx context.Context, key string) (json.RawMessage, error) // ErrNotFound (also when expired)
	// Set stores a JSON value; ttl 0 means no expiry.
	Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	// Increment atomically adds by to a numeric value (missing = 0) and returns the result.
	Increment(ctx context.Context, key string, by float64) (float64, error)
	// List returns up to limit items whose key starts with prefix, ordered by key, after
	// cursor (exclusive). next is "" when there are no more items.
	List(ctx context.Context, prefix string, limit int, cursor string) (items []KVItem, next string, err error)
}

// ───────────────────────────────────────────────────────────── tasks

// Task is a persisted one-shot job (ctx.schedule.after in the SDK).
type Task struct {
	ID      string
	Scope   string // same scope format as KV
	Name    string
	Payload json.RawMessage
	RunAt   time.Time
}

// TaskLease identifies one delivery. A receipt can only settle the same claim:
// replacing or cancelling a task invalidates every outstanding receipt.
type TaskLease struct {
	Task
	Receipt  string
	Attempt  int
	Failures int
}

// TaskWriter is the part of the queue exposed to a plugin.
type TaskWriter interface {
	// Schedule inserts the task, or replaces the task with the same ID and scope
	// (ErrTaskIDConflict when the ID belongs to another scope).
	Schedule(ctx context.Context, t Task) error
	Cancel(ctx context.Context, scope, id string) error
}

// TaskQueue persists scheduled work until successful delivery or operator disposal.
type TaskQueue interface {
	TaskWriter
	ClaimDue(ctx context.Context, now, until time.Time, worker string, limit int) ([]TaskLease, error)
	Acknowledge(ctx context.Context, lease TaskLease) error
	// Release schedules another delivery or, with a zero retry time, parks the task
	// as a dead letter. An empty failure defers an unavailable handler.
	Release(ctx context.Context, lease TaskLease, retry time.Time, failure string) error
	DeadCounts(ctx context.Context) (map[string]int, error)
	DeadLetters(ctx context.Context, server string, limit int) ([]DeadTask, error)
	ResolveDead(ctx context.Context, server, id string, retry bool, now time.Time) (bool, error)
	// Due returns tasks with RunAt <= now, oldest first (ties by ID, byte order).
	Due(ctx context.Context, now time.Time, limit int) ([]Task, error)
}

// DeadTask omits the payload: it may contain private member or message data.
type DeadTask struct {
	Scope    string    `json:"scope"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	RunAt    time.Time `json:"runAt"`
	Attempts int       `json:"attempts"`
	Failures int       `json:"failures"`
	Reason   string    `json:"reason"`
}

// ───────────────────────────────────────────────────────────── audit

type AuditEntry struct {
	ID      int64
	Time    time.Time
	Server  string // server key, "" for project-level entries
	Actor   string // e.g. "discord:<id>", "plugin:<bot>/<instance>", "cicord"
	Action  string // e.g. "config.update", "member.kick", "sync.apply"
	Target  string
	Details json.RawMessage
}

type AuditFilter struct {
	Server string
	Before int64 // ID, 0 = latest
	Limit  int
}

// AuditLog is append-only. IDs increase strictly and are never reused; "newest
// first" means by descending ID (on PostgreSQL, IDs come from a sequence, so
// under concurrent appends ID order is allocation order, not commit order).
type AuditLog interface {
	Append(ctx context.Context, e AuditEntry) (int64, error)
	List(ctx context.Context, f AuditFilter) ([]AuditEntry, error) // newest first
}

// ───────────────────────────────────────────────────────────── sync history

type SyncRecord struct {
	ID       int64
	Time     time.Time
	Revision string // git SHA or local revision ID
	Target   string // "server:<key>", "bot:<key>" or "revision"
	Status   string // "succeeded" | "failed" | "rejected"
	Message  string
	Details  json.RawMessage
}

// SyncLog is append-only, ordered by ID like AuditLog.
type SyncLog interface {
	Record(ctx context.Context, r SyncRecord) (int64, error)
	List(ctx context.Context, target string, limit int) ([]SyncRecord, error) // newest first; target "" = all
}

// ───────────────────────────────────────────────────────────── sessions

// Session is an authenticated API session (admin UI / portal).
type Session struct {
	ID        string // random, opaque
	UserID    string // Discord user ID
	Data      json.RawMessage
	ExpiresAt time.Time
}

type SessionStore interface {
	Put(ctx context.Context, s Session) error
	Get(ctx context.Context, id string) (Session, error) // ErrNotFound (also when expired)
	Delete(ctx context.Context, id string) error
}
