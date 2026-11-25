// Package examples embeds the official example plugins into the cicord binary: they are
// the bundled plugin catalog (internal/catalog) of the setup wizard and of the Store, for
// offline and first installs. The official registry (github.com/cicord/cicord-plugins)
// publishes the same plugins, at the same versions; github-releases is only there.
//
// Only the plugin sources are embedded (explicit patterns, so that node_modules or build
// outputs never end up in the binary).
package examples

import "embed"

// Plugins holds <example>/plugins/<name>/{plugin.yaml,config.schema.json,tsconfig.json,src/…}.
//
//go:embed basic/plugins/welcome/plugin.yaml basic/plugins/welcome/config.schema.json basic/plugins/welcome/tsconfig.json basic/plugins/welcome/src/*.ts
//go:embed basic/plugins/onboarding/plugin.yaml basic/plugins/onboarding/config.schema.json basic/plugins/onboarding/tsconfig.json basic/plugins/onboarding/src/*.ts
//go:embed showcase/plugins/tickets/plugin.yaml showcase/plugins/tickets/config.schema.json showcase/plugins/tickets/tsconfig.json showcase/plugins/tickets/src/*.ts
//go:embed showcase/plugins/polls/plugin.yaml showcase/plugins/polls/config.schema.json showcase/plugins/polls/tsconfig.json showcase/plugins/polls/src/*.ts
//go:embed showcase/plugins/levels/plugin.yaml showcase/plugins/levels/config.schema.json showcase/plugins/levels/tsconfig.json showcase/plugins/levels/src/*.ts
//go:embed showcase/plugins/moderation/plugin.yaml showcase/plugins/moderation/config.schema.json showcase/plugins/moderation/tsconfig.json showcase/plugins/moderation/src/*.ts
//go:embed showcase/plugins/reaction-roles/plugin.yaml showcase/plugins/reaction-roles/config.schema.json showcase/plugins/reaction-roles/tsconfig.json showcase/plugins/reaction-roles/src/*.ts
//go:embed showcase/plugins/audit-log/plugin.yaml showcase/plugins/audit-log/config.schema.json showcase/plugins/audit-log/tsconfig.json showcase/plugins/audit-log/src/*.ts
//go:embed showcase/plugins/announcements/plugin.yaml showcase/plugins/announcements/config.schema.json showcase/plugins/announcements/tsconfig.json showcase/plugins/announcements/src/*.ts
//go:embed showcase/plugins/voice-rooms/plugin.yaml showcase/plugins/voice-rooms/config.schema.json showcase/plugins/voice-rooms/tsconfig.json showcase/plugins/voice-rooms/src/*.ts
var Plugins embed.FS

// Official lists the catalog plugins (directory inside Plugins), in display order.
var Official = []string{
	"basic/plugins/welcome",
	"basic/plugins/onboarding",
	"showcase/plugins/tickets",
	"showcase/plugins/polls",
	"showcase/plugins/levels",
	"showcase/plugins/moderation",
	"showcase/plugins/reaction-roles",
	"showcase/plugins/audit-log",
	"showcase/plugins/announcements",
	"showcase/plugins/voice-rooms",
}
