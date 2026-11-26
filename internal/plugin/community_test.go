package plugin

import (
	"strings"
	"testing"
)

func TestRawRouteContent(t *testing.T) {
	h := start(t, `
import {definePlugin} from "@cicord/sdk";
export default definePlugin({routes:{"GET /calendar.ics":{access:"public",handler:()=>({status:200,raw:"BEGIN:VCALENDAR\r\n",contentType:"text/calendar"})},
 "GET /limit":{access:"public",handler:()=>({status:200,raw:"\n".repeat(2*1024*1024),contentType:"text/plain"})},
 "GET /bad":{access:"member",handler:()=>({status:200,raw:"x",contentType:"text/html"})},
 "GET /big":{access:"public",handler:()=>({status:200,raw:"x".repeat(2*1024*1024+1),contentType:"text/plain"})}}});`, nil)
	r, err := h.inst.HandleRoute(t.Context(), "GET", "/calendar.ics", RouteRequest{})
	if err != nil || r.Raw == nil || *r.Raw != "BEGIN:VCALENDAR\r\n" || r.ContentType != "text/calendar" {
		t.Fatal(r, err)
	}
	r, err = h.inst.HandleRoute(t.Context(), "GET", "/limit", RouteRequest{})
	if err != nil || r.Raw == nil || len(*r.Raw) != 2<<20 {
		t.Fatalf("escaped text at limit: %v", err)
	}
	for _, path := range []string{"/bad", "/big"} {
		if _, err = h.inst.HandleRoute(t.Context(), "GET", path, RouteRequest{}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
func TestPluginAnalyticsGrantAndRange(t *testing.T) {
	h := start(t, `import {definePlugin} from "@cicord/sdk";export default definePlugin({routes:{"GET /":{access:"admin",async handler(ctx){return await ctx.analytics.summary({from:"2025-01-01T00:00:00Z",to:"2025-01-07T00:00:00Z",granularity:"day"});}}}});`, nil)
	_, err := h.inst.HandleRoute(t.Context(), "GET", "/", RouteRequest{})
	if err == nil || !strings.Contains(err.Error(), "analytics.read") {
		t.Fatal(err)
	}
}
