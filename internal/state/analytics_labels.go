package state

import (
	"context"
	"time"
)

// AnalyticsLabel is the last known label of a dimension value that names a Discord
// resource (a channel, a role, a scheduled event): analytics store dimensions as
// Discord IDs, and a deleted resource's name can no longer be read from Discord.
type AnalyticsLabel struct {
	Server string // server key
	Dim    string // the metrics' dimension: "channel", "role", "event"
	ID     string // the dimension value (a Discord ID)
	Name   string
	// Type is the label kind of the API ("channel", "voice", "thread", "role", "event").
	Type  string
	Color string // roles: "#rrggbb" ("" otherwise)
	// Owner is "plugin:<bot>/<instance>" when a plugin created the channel ("" when
	// unknown or created otherwise). Once stored it is kept: a later write without
	// owner does not clear it (the resource map forgets a plugin's channel when the
	// plugin deletes it).
	Owner string
	// Deleted is true once the resource was seen deleted.
	Deleted bool
	// SeenAt is the last time the collector saw the resource (retention).
	SeenAt time.Time
}

// AnalyticsLabels is an optional interface of Analytics stores (both backends
// implement it; fakes of Analytics need not): the remembered labels of the
// dimensions the collector saw, so that a deleted channel or role keeps its name in
// the dashboard. Labels are bounded like the points: PruneLabels deletes those not
// seen within the retention and those whose dimension has no point left.
type AnalyticsLabels interface {
	// PutLabels inserts or replaces labels by (Server, Dim, ID), in one transaction.
	// A label with an empty Owner keeps the stored owner. Server, Dim and ID must not
	// be empty; a zero SeenAt is the store's now.
	PutLabels(ctx context.Context, labels ...AnalyticsLabel) error
	// GetLabels returns the stored labels of a server's dimension for ids (unknown
	// ids are left out), in no particular order. At most MaxListLimit ids per call.
	GetLabels(ctx context.Context, server, dim string, ids []string) ([]AnalyticsLabel, error)
	// PruneLabels deletes the labels of a server seen before before, and those whose
	// ID is not the dimension of any point of the server any more. It returns how
	// many it removed.
	PruneLabels(ctx context.Context, server string, before time.Time) (int64, error)
}
