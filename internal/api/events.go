package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/version"
)

// Live refresh of the admin console (https://cicord.dev/docs/reference/http-api/events/):
// GET /api/v1/events is a Server-Sent Events stream of change hints. The first event,
// "hello", gives the runtime version and revision (a client that sees another version
// than the one it loaded reloads its code); then one event per change topic of the
// runtime (status, sync, plugins, analytics) with the server concerned. Events carry no
// content: clients refetch through the usual endpoints and their access checks. Server
// events are only sent for the servers the user moderates. "stopping" is sent when the
// runtime shuts down (an update restarts it): reconnect. The stream ends after
// eventsLifetime (the client reconnects, which checks the session and access again).

const (
	eventsLifetime = 10 * time.Minute
	eventsCoalesce = 250 * time.Millisecond
	eventsPing     = 25 * time.Second
)

type eventHello struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type eventChange struct {
	Server string `json:"server,omitempty"`
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.rt == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "runtime not available"))
		return
	}
	vis, err := s.visibleServers(r.Context(), id, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(vis) == 0 && !s.isBearer(r) && !id.setup {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "no server to administer"))
		return
	}
	// Subscribe before the hello event, so that no change is missed in between.
	ch, unsubscribe := s.rt.SubscribeChanges()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	seq := 0
	send := func(event string, data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		seq++
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", strconv.Itoa(seq), event, b); err != nil {
			return err
		}
		return rc.Flush()
	}
	hello := eventHello{Version: version.Version}
	if rev, ok := s.rt.Revision(); ok {
		hello.Revision = rev.ID
	}
	if err := send("hello", hello); err != nil {
		return
	}

	end := time.NewTimer(eventsLifetime)
	defer end.Stop()
	ping := time.NewTicker(eventsPing)
	defer ping.Stop()
	// Coalesce bursts (a revision syncs every server): changes are sent at most every
	// eventsCoalesce, each topic and server once.
	flush := time.NewTimer(0)
	<-flush.C
	pending := map[runtime.Change]bool{}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.opts.Stopping:
			// Tell the client now: a proxy may keep its side of the connection open.
			_ = send("stopping", struct{}{})
			return
		case <-end.C:
			return
		case c, ok := <-ch:
			if !ok {
				return
			}
			if c.Server != "" {
				if _, ok := vis[c.Server]; !ok && !s.isBearer(r) {
					continue
				}
			}
			if len(pending) == 0 {
				flush.Reset(eventsCoalesce)
			}
			pending[c] = true
		case <-flush.C:
			batch := make([]runtime.Change, 0, len(pending))
			for c := range pending {
				batch = append(batch, c)
			}
			clear(pending)
			slices.SortFunc(batch, func(a, b runtime.Change) int {
				if a.Topic != b.Topic {
					return strings.Compare(a.Topic, b.Topic)
				}
				return strings.Compare(a.Server, b.Server)
			})
			for _, c := range batch {
				if err := send(c.Topic, eventChange{Server: c.Server}); err != nil {
					return
				}
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
