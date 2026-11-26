package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// KVWrite records a buffered mutation. Expected is the live value observed on first access.
type KVWrite struct {
	Key             string
	Expected, Value json.RawMessage
	Changed         bool
	TTL             time.Duration
	KeepTTL         bool
}

type EventFanout struct {
	ID, Server, Publisher string
	Payload               json.RawMessage // encoded event and metadata
	Topic                 string
	Subscribers           []string // subscriber KV scopes
	At                    time.Time
}
type InteropStore interface {
	PublishEvent(context.Context, EventFanout) error
	PublishEventTx(context.Context, *sql.Tx, EventFanout) error
	ReserveAction(ctx context.Context, id, server, digest, owner string, now time.Time) (result json.RawMessage, acquired bool, err error)
	FailAction(ctx context.Context, id, owner string) error
	CommitAction(ctx context.Context, id, owner, scope string, writes []KVWrite, result json.RawMessage, events ...EventFanout) error
	PurgeInteropMember(ctx context.Context, server, userID string) (int, error)
}
