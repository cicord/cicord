package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// Plugin route responses: only allowlisted headers pass, a 1xx status never reaches the
// connection, and a public answer personalized for a signed-in viewer is never left to
// shared caches.
func TestPluginRouteResponsePolicy(t *testing.T) {
	e := startFull(t, func(dir string) {
		writeTestFile(t, filepath.Join(dir, "plugins", "onboarding", "src", "index.ts"), `import { definePlugin } from "@cicord/sdk";
const headers = {
  "Refresh": "0;url=https://phish.example", "Clear-Site-Data": "\"cookies\"", "Upgrade": "websocket",
  "Link": "<https://phish.example/x.js>; rel=preload", "Cross-Origin-Opener-Policy": "unsafe-none",
  "Set-Cookie": "cicord_session=stolen", "Location": "https://phish.example", "Access-Control-Allow-Origin": "*",
  "X-Cicord-Portal": "spoofed", "X-Frame-Options": "ALLOWALL",
  "X-Plugin-Version": "7", "Content-Disposition": "attachment; filename=\"export.json\"", "ETag": "\"v7\"",
  "Cache-Control": "public, max-age=600",
};
export default definePlugin({ routes: {
  "GET /headers": { access:"public", handler(){return {status:200, headers, body:{ok:true}};} },
  "GET /empty": { access:"public", handler(){return {status:204, headers};} },
  "GET /switch": { access:"public", handler(){return {status:101, body:{}};} },
  "GET /switch.txt": { access:"public", handler(){return {status:101, contentType:"text/plain", raw:""};} },
  "GET /mine.ics": { access:"public", handler(ctx,req){
    return {status:200, contentType:"text/calendar", raw: req.viewer ? "BEGIN:VCALENDAR\r\nX-OWNER:"+req.viewer.user.id+"\r\nEND:VCALENDAR\r\n" : "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"};
  } }
}});`)
	}, nil, nil)
	base := "/api/v1/portal/" + serverKey + "/plugins/moderator/onboarding/"
	anon := e.client()
	for _, path := range []string{"headers", "empty"} {
		r := anon.do("GET", base+path, nil)
		if r.status != 200 && r.status != 204 {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		for _, h := range []string{"Refresh", "Clear-Site-Data", "Upgrade", "Link", "Cross-Origin-Opener-Policy", "Set-Cookie", "Location", "Access-Control-Allow-Origin", "X-Cicord-Portal"} {
			if r.header.Get(h) != "" {
				t.Errorf("%s: plugin set %s: %q", path, h, r.header.Get(h))
			}
		}
		if r.header.Get("X-Frame-Options") == "ALLOWALL" {
			t.Errorf("%s: plugin overrode X-Frame-Options", path)
		}
		if r.header.Get("X-Plugin-Version") != "7" || r.header.Get("Content-Disposition") == "" || r.header.Get("ETag") != `"v7"` {
			t.Errorf("%s: allowed headers dropped: %v", path, r.header)
		}
	}
	// A public route without a body keeps the plugin's cache policy for anonymous callers.
	if r := anon.do("GET", base+"empty", nil); r.header.Get("Cache-Control") != "public, max-age=600" || !strings.Contains(vary(r), "Cookie") {
		t.Errorf("anonymous public cache policy: %v", r.header)
	}
	for _, path := range []string{"switch", "switch.txt"} {
		if r := anon.do("GET", base+path, nil); r.status != 500 || r.errCode() != "internal" {
			t.Errorf("%s: status %d %s", path, r.status, r.body)
		}
	}

	r := anon.expect(200, "GET", base+"mine.ics", nil)
	if strings.Contains(string(r.body), "X-OWNER") || !strings.Contains(vary(r), "Cookie") || !strings.Contains(vary(r), "Authorization") {
		t.Fatalf("anonymous calendar: %s %v", r.body, r.header)
	}
	viewer := e.client()
	viewer.login(e.seed.Owner.ID)
	r = viewer.expect(200, "GET", base+"mine.ics", nil)
	if !strings.Contains(string(r.body), "X-OWNER:"+e.seed.Owner.ID) || r.header.Get("Cache-Control") != "no-store" || !strings.Contains(vary(r), "Cookie") {
		t.Fatalf("personalized calendar: %s %v", r.body, r.header)
	}
	if r := viewer.expect(204, "GET", base+"empty", nil); r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("personalized empty answer is cacheable: %v", r.header)
	}
}

func vary(r resp) string { return strings.Join(r.header.Values("Vary"), ", ") }

func TestRouteHeaderAllowlist(t *testing.T) {
	for name, want := range map[string][2]bool{ // public, member
		"Cache-Control": {true, false}, "content-disposition": {true, true}, "ETag": {true, true},
		"Last-Modified": {true, true}, "X-Total-Count": {true, true}, "x-cicord-portal": {false, false},
		"Upgrade": {false, false}, "Connection": {false, false}, "Keep-Alive": {false, false},
		"Refresh": {false, false}, "Set-Cookie": {false, false}, "Location": {false, false},
		"Access-Control-Allow-Origin": {false, false}, "Content-Type": {false, false},
		"Strict-Transport-Security": {false, false}, "X-Content-Type-Options": {false, false},
	} {
		if got := [2]bool{routeHeader(name, true), routeHeader(name, false)}; got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}
