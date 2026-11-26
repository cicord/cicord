package runtime

import (
	"time"

	"github.com/cicord/cicord/internal/source"
)

// Sync and health values (ArgoCD style, https://cicord.dev/docs/reference/http-api/servers/).
const (
	SyncSynced    = "Synced"
	SyncOutOfSync = "OutOfSync"
	SyncUnknown   = "Unknown"

	HealthHealthy     = "Healthy"
	HealthProgressing = "Progressing"
	HealthDegraded    = "Degraded"
	HealthMissing     = "Missing"
)

type Status struct {
	Version   string           `json:"version"`
	Mode      string           `json:"mode"`
	Simulator bool             `json:"simulator"`
	Revision  *source.Revision `json:"revision"`
	// LastError is the latest rejected revision / failed sync, if any.
	LastError string         `json:"lastError,omitempty"`
	Servers   []ServerStatus `json:"servers"`
	Bots      []BotStatus    `json:"bots"`
}

type DriftEntry struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Field    string `json:"field"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type ServerStatus struct {
	Key       string       `json:"key"`
	Name      string       `json:"name"`
	DiscordID string       `json:"discordId"`
	Sync      string       `json:"sync"`
	Health    string       `json:"health"`
	Message   string       `json:"message"`
	LastSync  *time.Time   `json:"lastSync,omitempty"`
	Drift     []DriftEntry `json:"drift"`
	// Bot is the bot used to reconcile this server.
	Bot string `json:"bot,omitempty"`
	// Paused is set while enforcement is paused on the server (nothing is applied; Sync
	// still reports the drift from git).
	Paused *PauseInfo `json:"paused,omitempty"`
}

type BotStatus struct {
	Key      string         `json:"key"`
	Username string         `json:"username"`
	UserID   string         `json:"userId"`
	Health   string         `json:"health"`
	Message  string         `json:"message"`
	Servers  []string       `json:"servers"`
	Plugins  []PluginStatus `json:"plugins"`
}

type PluginStatus struct {
	Advisories []FeatureAdvisory `json:"advisories,omitempty"`
	Instance   string            `json:"instance"`
	Plugin     string            `json:"plugin"`
	Version    string            `json:"version"`
	Server     string            `json:"server"`
	Health     string            `json:"health"`
	Message    string            `json:"message"`
}
