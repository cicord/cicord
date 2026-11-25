package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/runtime"
)

// Server activity analytics (https://cicord.dev/docs/reference/http-api/analytics/):
// GET /api/v1/servers/{server}/analytics/summary|series|catalog, for moderators and
// admins of the server, on the admin host. The JSON contract is internal/analytics/api.go.

func (s *server) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	req, err := analytics.ParseSummaryRequest(r.URL.Query(), time.Now())
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.SummaryFor(r.Context(), key, req, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAnalyticsSeries(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	req, err := analytics.ParseSeriesRequest(key, r.URL.Query(), time.Now())
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.Series(r.Context(), req, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAnalyticsCatalog(w http.ResponseWriter, r *http.Request) {
	rd, key, st, err := s.analyticsRequest(r)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	out, err := rd.Catalog(r.Context(), key, st)
	if err != nil {
		s.failAnalytics(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// analyticsRequest checks access (moderator or admin of the server) and returns the
// reader, the server key and the collection status of the server.
func (s *server) analyticsRequest(r *http.Request) (*analytics.Reader, string, analytics.Status, error) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleModerator); err != nil {
		return nil, "", analytics.Status{}, err
	}
	if s.opts.Analytics == nil {
		return nil, "", analytics.Status{}, httpErr(http.StatusNotFound, "analytics_disabled",
			"analytics are not available on this runtime (no analytics store is configured)")
	}
	labelers := analytics.Labelers{runtimeLabeler{s.rt}}
	if s.store != nil {
		labelers = append(labelers, analytics.ResourceLabeler{Resources: s.store.Resources(), Spec: s.serverSpec})
	}
	rd := &analytics.Reader{Store: s.opts.Analytics, Labeler: labelers}
	return rd, key, s.analyticsStatus(key), nil
}

func (s *server) serverSpec(key string) *config.ServerSpec {
	if srv, ok := s.rt.ServerConfig(key); ok {
		return &srv.Spec
	}
	return nil
}

// analyticsStatus is the collection status of a server: from the runtime
// (Options.AnalyticsStatus) or, without it, from the server configuration. Since is
// filled by the reader, from the store.
func (s *server) analyticsStatus(key string) analytics.Status {
	if s.opts.AnalyticsStatus != nil {
		return s.opts.AnalyticsStatus(key)
	}
	st := analytics.Status{Missing: []analytics.Missing{}}
	if srv, ok := s.rt.ServerConfig(key); ok {
		st.Enabled, st.RetentionDays, st.Presence = srv.Spec.AnalyticsSettings()
	}
	return st
}

func (s *server) failAnalytics(w http.ResponseWriter, err error) {
	var re *analytics.RequestError
	if errors.As(err, &re) {
		writeError(w, http.StatusBadRequest, "invalid", re.Message)
		return
	}
	s.fail(w, err)
}

// runtimeLabeler labels channels and roles from the runtime's live cache of the
// server (current names, channel types and role colors, whether declared in git or
// not). It knows nothing while the cache is empty.
type runtimeLabeler struct{ rt *runtime.Runtime }

func (l runtimeLabeler) Labels(_ context.Context, server string, dims []analytics.DimRef) map[string]analytics.Label {
	if l.rt == nil {
		return nil
	}
	roles, channels, _, ok := l.rt.ServerResources(server)
	if !ok || (len(roles) == 0 && len(channels) == 0) {
		return nil
	}
	byChannel := make(map[string]discord.Channel, len(channels))
	for _, c := range channels {
		byChannel[c.ID] = c
	}
	byRole := make(map[string]discord.Role, len(roles))
	for _, ro := range roles {
		byRole[ro.ID] = ro
	}
	out := map[string]analytics.Label{}
	for _, d := range dims {
		switch d.Kind {
		case "channel":
			if c, ok := byChannel[d.Value]; ok {
				out[d.Value] = analytics.Label{Name: c.Name, Kind: channelLabelKind(c.Type)}
			}
		case "role":
			if ro, ok := byRole[d.Value]; ok {
				out[d.Value] = analytics.Label{Name: ro.Name, Kind: "role", Color: colorHex(ro.Color)}
			}
		}
	}
	return out
}

func channelLabelKind(t discord.ChannelType) string {
	switch t {
	case discord.ChannelVoice, discord.ChannelStage:
		return "voice"
	case discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelAnnouncementThread:
		return "thread"
	}
	return "channel"
}
