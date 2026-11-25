package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// startPortalAnalytics starts the API with analytics and spec.analytics.portal = mode.
func startPortalAnalytics(t *testing.T, mode string) *env {
	t.Helper()
	return startFull(t, func(dir string) {
		if mode == "" {
			return
		}
		p := filepath.Join(dir, "servers", "ma-commu.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.Replace(string(data), "\n  syncPolicy:", "\n  analytics:\n    portal: "+mode+"\n  syncPolicy:", 1))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}, nil, func(o *Options) { o.Analytics = o.Runtime.Store().Analytics() })
}

func TestPortalAnalyticsAccess(t *testing.T) {
	path := "/api/v1/portal/" + serverKey + "/analytics"
	// Off by default: the endpoint does not exist for this server.
	if r := start(t).client().do("GET", path, nil); r.status != 404 {
		t.Errorf("off = %d %s", r.status, r.body)
	}
	off := startPortalAnalytics(t, "")
	if r := off.client().do("GET", path, nil); r.status != 404 {
		t.Errorf("off with analytics = %d %s", r.status, r.body)
	}

	e := startPortalAnalytics(t, "members")
	if r := e.client().do("GET", path, nil); r.status != 401 {
		t.Errorf("members, anonymous = %d %s", r.status, r.body)
	}
	stranger := e.client()
	stranger.login(e.seed.Users[0].ID) // not a member of the server
	if r := stranger.do("GET", path, nil); r.status != 403 {
		t.Errorf("members, stranger = %d %s", r.status, r.body)
	}
	if err := e.world.AddMember(e.serverID(), e.seed.Users[0].ID); err != nil {
		t.Fatal(err)
	}
	stranger.expect(200, "GET", path, nil)
	if r := stranger.do("GET", path+"?period=1y", nil); r.status != 400 {
		t.Errorf("bad period = %d %s", r.status, r.body)
	}

	pub := startPortalAnalytics(t, "public")
	pub.client().expect(200, "GET", path+"?period=7d", nil)
}

// TestPortalAnalyticsChannels: the rankings name public channels only.
func TestPortalAnalyticsChannels(t *testing.T) {
	e := startPortalAnalytics(t, "public")
	_, channels, _, ok := e.rt.ServerResources(serverKey)
	if !ok {
		t.Fatal("no resources")
	}
	var public, private discord.Channel
	for _, c := range channels {
		if c.Type != discord.ChannelText {
			continue
		}
		if parent, ok := channelByID(channels, c.ParentID); ok && parent.Name == "Staff" && private.ID == "" {
			private = c
		} else if ok && parent.Name != "Staff" && public.ID == "" {
			public = c
		}
	}
	if public.ID == "" || private.ID == "" {
		t.Fatalf("channels: public %+v, private %+v", public, private)
	}
	a := e.rt.Store().Analytics()
	now := time.Now().UTC().Truncate(time.Hour)
	for _, p := range []state.AnalyticsPoint{
		{Server: serverKey, Metric: analytics.MessagesSent, Dim: private.ID, Bucket: now, Value: 9},
		{Server: serverKey, Metric: analytics.MessagesSent, Dim: public.ID, Bucket: now, Value: 4},
		{Server: serverKey, Metric: analytics.BotsCommands, Dim: "Cicord /rank", Bucket: now, Value: 3},
	} {
		if err := a.Record(context.Background(), state.AnalyticsCounter, p); err != nil {
			t.Fatal(err)
		}
	}
	var out analytics.PortalAnalytics
	e.client().expect(200, "GET", "/api/v1/portal/"+serverKey+"/analytics", nil).json(t, &out)
	if out.Period != "30d" || len(out.KPIs) != len(analytics.PortalKPIs) {
		t.Fatalf("out = %+v", out)
	}
	if len(out.TopChannels) != 1 || out.TopChannels[0].ID != public.ID || out.TopChannels[0].Name != public.Name {
		t.Errorf("top channels = %+v (private %s must not appear)", out.TopChannels, private.Name)
	}
	if len(out.TopBotCommands) != 1 || out.TopBotCommands[0] != (analytics.PortalCommand{Bot: "Cicord", Command: "/rank", Value: 3}) {
		t.Errorf("top commands = %+v", out.TopBotCommands)
	}
	for _, k := range out.KPIs {
		if k.Metric == analytics.MessagesSent && k.Value != 13 {
			t.Errorf("messages KPI = %v (server-wide total)", k.Value)
		}
	}
}

func channelByID(cs []discord.Channel, id string) (discord.Channel, bool) {
	for _, c := range cs {
		if c.ID == id {
			return c, true
		}
	}
	return discord.Channel{}, false
}
