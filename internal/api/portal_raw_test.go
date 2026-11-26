package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A calendar subscriber polls one unauthenticated URL across a revision change. A
// second route verifies that authenticated exports keep the host's cache controls.
func TestPortalRawCalendarRevision(t *testing.T) {
	const uid = "game-night@server.example"
	calendar := func(rule string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTART:20300104T210000Z\r\nRRULE:" + rule + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	}
	install := func(dir, rule string) {
		source := fmt.Sprintf(`import { definePlugin } from "@cicord/sdk";
export default definePlugin({ routes: {
  "GET /calendar.ics": { access:"public", handler(){return {status:200,contentType:"text/calendar",raw:%q};} },
  "GET /staff.csv": { access:"member",handler(){return {status:200,contentType:"text/csv",raw:"members,4"};} }
}});`, calendar(rule))
		writeTestFile(t, filepath.Join(dir, "plugins", "onboarding", "src", "index.ts"), source)
	}
	const first = "FREQ=WEEKLY;BYDAY=FR"
	const next = "FREQ=WEEKLY;BYDAY=SA"
	e := startFull(t, func(dir string) { install(dir, first) }, nil, nil)
	client := e.client()
	base := "/api/v1/portal/" + serverKey + "/plugins/moderator/onboarding/"
	check := func(rule string) {
		t.Helper()
		r := client.expect(200, "GET", base+"calendar.ics", nil)
		if string(r.body) != calendar(rule) || r.header.Get("Content-Type") != "text/calendar; charset=utf-8" || r.header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("calendar returned %q %+v", r.body, r.header)
		}
	}
	check(first)
	install(e.dir, next)
	e.waitFor("calendar revision", func() bool { return strings.Contains(string(client.do("GET", base+"calendar.ics", nil).body), next) })
	check(next)
	client.expect(401, "GET", base+"staff.csv", nil)
	client.login(e.seed.Owner.ID)
	r := client.expect(200, "GET", base+"staff.csv", nil)
	if string(r.body) != "members,4" || r.header.Get("Content-Type") != "text/csv; charset=utf-8" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatal(string(r.body), r.header)
	}
}
