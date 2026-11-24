// Package state is the runtime's persistent store (SQLite by default).
//
// Configuration lives in git; everything here is runtime state or community data:
// the resource map (YAML key ↔ Discord ID), plugin KV storage, scheduled tasks,
// the audit log, sync history and API sessions. See ADR-0003 and ADR-0004.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("state: not found")

// Store groups all state. Implementations are safe for concurrent use.
type Store interface {
	Resources() ResourceMap
	// KV returns the key-value space of a scope, e.g. "plugin:<bot>/<instance>/<server>".
	KV(scope string) KV
	Tasks() TaskQueue
	Audit() AuditLog
	Syncs() SyncLog
	Sessions() SessionStore
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

type TaskQueue interface {
	Schedule(ctx context.Context, t Task) error
	Cancel(ctx context.Context, scope, id string) error
	// Due returns tasks with RunAt <= now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Task, error)
	// Complete removes a task after it ran.
	Complete(ctx context.Context, id string) error
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
