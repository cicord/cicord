package api

import (
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// pluginRoom creates a channel in the simulated server as a plugin would: on
// Discord, then in the resource map with its owner. It waits until the runtime's
// cache sees it.
func pluginRoom(t *testing.T, e *env, name string, overwrites []discord.Overwrite) discord.Channel {
	t.Helper()
	p := discord.ChannelParams{Name: &name, Type: discord.ChannelVoice}
	if overwrites != nil {
		p.Overwrites = &overwrites
	}
	ch, err := e.world.AdminClient(e.serverID()).CreateChannel(t.Context(), e.serverID(), p, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.rt.Store().Resources().Put(t.Context(), state.Resource{Server: serverKey, Kind: state.KindChannel,
		Key: "main/voice-rooms/" + ch.ID, ID: ch.ID, Owner: "plugin:main/voice-rooms"}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the room in the cache", func() bool {
		_, channels, _, ok := e.rt.ServerResources(serverKey)
		_, found := channelByID(channels, ch.ID)
		return ok && found
	})
	return ch
}

// TestPortalAnalyticsPluginRooms: a room a plugin created, named after a member, is
// never named by the portal: it counts in the plugin's group; a private room is not
// counted at all.
func TestPortalAnalyticsPluginRooms(t *testing.T) {
	e := startPortalAnalytics(t, "public")
	public := pluginRoom(t, e, "NPjonath's channel", nil)
	private := pluginRoom(t, e, "Secretroom of Alice", []discord.Overwrite{
		{ID: e.serverID(), Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")}})
	a := e.rt.Store().Analytics()
	now := time.Now().UTC().Truncate(time.Hour)
	for _, p := range []state.AnalyticsPoint{
		{Server: serverKey, Metric: analytics.MessagesSent, Dim: public.ID, Bucket: now, Value: 6},
		{Server: serverKey, Metric: analytics.MessagesSent, Dim: private.ID, Bucket: now, Value: 50},
	} {
		if err := a.Record(t.Context(), state.AnalyticsCounter, p); err != nil {
			t.Fatal(err)
		}
	}
	r := e.client().expect(200, "GET", "/api/v1/portal/"+serverKey+"/analytics", nil)
	for _, leak := range []string{"NPjonath", "Alice", public.ID, private.ID} {
		if strings.Contains(string(r.body), leak) {
			t.Errorf("the portal's payload contains %q:\n%s", leak, r.body)
		}
	}
	var out analytics.PortalAnalytics
	r.json(t, &out)
	want := analytics.PortalChannel{ID: "plugin:main/voice-rooms", Name: "voice-rooms", Value: 6, Group: true}
	if len(out.TopChannels) != 1 || out.TopChannels[0] != want {
		t.Errorf("top channels = %+v, want only %+v (the private room is not counted)", out.TopChannels, want)
	}
}

// TestAnalyticsSummaryPluginRooms: the admin summary groups a plugin's rooms, live and
// deleted, and a deleted room keeps the name the collector remembered.
func TestAnalyticsSummaryPluginRooms(t *testing.T) {
	e := startAnalytics(t, nil)
	live := pluginRoom(t, e, "Bob's room", nil)
	const deleted = "1575242066328551536" // not in Discord nor in the resource map any more
	a := e.rt.Store().Analytics()
	if err := a.(state.AnalyticsLabels).PutLabels(t.Context(), state.AnalyticsLabel{Server: serverKey, Dim: "channel", ID: deleted,
		Name: "NPjonath's channel", Type: "voice", Owner: "plugin:main/voice-rooms", Deleted: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	for _, p := range []state.AnalyticsPoint{
		{Server: serverKey, Metric: analytics.VoiceSeconds, Dim: live.ID, Bucket: now, Value: 600},
		{Server: serverKey, Metric: analytics.VoiceSeconds, Dim: deleted, Bucket: now, Value: 3600},
	} {
		if err := a.Record(t.Context(), state.AnalyticsCounter, p); err != nil {
			t.Fatal(err)
		}
	}
	c := e.client()
	c.login(e.moderator().ID)
	var s analytics.Summary
	c.expect(200, "GET", analyticsPath+"summary", nil).json(t, &s)
	voice := s.Rankings[analytics.VoiceSeconds]
	if len(voice) != 1 || voice[0].Dim != "plugin:main/voice-rooms" || voice[0].Value != 4200 || voice[0].Count != 2 ||
		len(voice[0].Children) != 2 || voice[0].Children[0].Dim != deleted || voice[0].Children[1].Dim != live.ID {
		t.Fatalf("voice ranking = %+v", voice)
	}
	if l := s.Labels["plugin:main/voice-rooms"]; l != (analytics.Label{Name: "voice-rooms", Kind: "group"}) {
		t.Errorf("group label = %+v", l)
	}
	if l := s.Labels[deleted]; l != (analytics.Label{Name: "NPjonath's channel", Kind: "voice", Deleted: true}) {
		t.Errorf("deleted room label = %+v", l)
	}
	if l := s.Labels[live.ID]; l != (analytics.Label{Name: "Bob's room", Kind: "voice"}) {
		t.Errorf("live room label = %+v", l)
	}
}
