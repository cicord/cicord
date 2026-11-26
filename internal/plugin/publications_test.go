package plugin

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
)

func TestLiteralEventContracts(t *testing.T) {
	topics := []config.Topic{{Topic: "closed", Version: 2}}
	for _, tc := range []struct{ name, code, problem string }{
		{"declared", `ctx.events.publish("closed", { version: 98 }, { version: 2 })`, ""},
		{"implicit v1", `ctx.events.publish("closed", {})`, "closed v1"},
		{"implicit v1 with key", `ctx.events.publish("closed", {}, {idempotencyKey: "one"})`, "closed v1"},
		{"bad version", `ctx.events.publish("closed", {}, {version: 3})`, "closed v3"},
		{"unknown topic", `ctx.events.publish('clsoed', {}, options)`, "clsoed"},
		{"computed topic", `ctx.events.publish(whichTopic(), {}, {version: 98})`, ""},
		{"dynamic options", `ctx.events.publish('closed', {}, options)`, ""},
		{"dynamic version", `ctx.events.publish('closed', {}, {version: choose()})`, ""},
		{"nested version", `ctx.events.publish('closed', {}, {elsewhere:{version:2}})`, "closed v1"},
		{"overridden by spread", `ctx.events.publish('closed', {}, {version:8,...options})`, ""},
		{"override spread", `ctx["events"]['publish']('closed', {}, {...options,["version"]:4})`, "closed v4"},
		{"escaped version property", `ctx.events.publish('closed', {}, {"version":2})`, ""},
		{"embedded source", "/* ctx.events.publish('wrong', {}) */ const text = `ctx.events.publish('wrong', {})`;const rx=/ctx.events.publish('wrong')/;", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(checkPublications(tc.code, topics), "\n")
			if (tc.problem == "" && got != "") || (tc.problem != "" && !strings.Contains(got, tc.problem)) {
				t.Fatalf("%s: %q", tc.code, got)
			}
		})
	}
}
