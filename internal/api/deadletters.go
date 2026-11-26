package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

func (s *server) handleDeadLetters(w http.ResponseWriter, r *http.Request) {
	serverKey := r.PathValue("server")
	if _, _, err := s.requireRole(r, serverKey, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxDeadLetters {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "limit must be between 1 and %d", maxDeadLetters))
			return
		}
		limit = n
	}
	after, err := decodeDeadCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "invalid cursor"))
		return
	}
	queue := s.rt.Store().Tasks()
	items, err := queue.DeadLettersAfter(r.Context(), serverKey, after, limit+1)
	if err != nil {
		s.fail(w, err)
		return
	}
	truncated := len(items) > limit
	var next *string
	if truncated {
		items = items[:limit]
		last := items[len(items)-1]
		c := encodeDeadCursor(state.DeadCursor{RunAt: last.RunAt, ID: last.ID})
		next = &c
	}
	// The total counts every parked task of the server, including those of instances
	// removed from the revision, so the admin knows how many the list leaves out.
	counts, err := queue.DeadCounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	total := 0
	for scope, n := range counts {
		if strings.HasPrefix(scope, "plugin:") && strings.HasSuffix(scope, "/"+serverKey) {
			total += n
		}
	}
	total = max(total, len(items))
	// truncated: more dead letters follow this page; nextCursor (null on the last page)
	// continues the listing after its last item.
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "truncated": truncated, "nextCursor": next})
}

// A dead-letter cursor is opaque to clients: the (run_at, id) of the last item of a page,
// as base64url JSON. Keyset paging keeps pages stable while letters are parked or resolved.
type deadCursorJSON struct {
	RunAt int64  `json:"t"`
	ID    string `json:"id"`
}

func encodeDeadCursor(c state.DeadCursor) string {
	raw, _ := json.Marshal(deadCursorJSON{RunAt: c.RunAt.UnixNano(), ID: c.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeDeadCursor(v string) (state.DeadCursor, error) {
	if v == "" {
		return state.DeadCursor{}, nil
	}
	if len(v) > 1024 {
		return state.DeadCursor{}, errors.New("cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return state.DeadCursor{}, err
	}
	var c deadCursorJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || c.ID == "" {
		return state.DeadCursor{}, errors.New("malformed cursor")
	}
	return state.DeadCursor{RunAt: time.Unix(0, c.RunAt).UTC(), ID: c.ID}, nil
}

// maxDeadLetters bounds one dead-letter listing (the oldest first).
const maxDeadLetters = 500

func (s *server) handleDeadLetterDecision(w http.ResponseWriter, r *http.Request) {
	serverKey := r.PathValue("server")
	actor, _, err := s.requireRole(r, serverKey, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	decision := r.PathValue("decision")
	if decision != "retry" && decision != "discard" {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown task decision"))
		return
	}
	id := r.PathValue("id")
	changed, err := s.rt.Store().Tasks().ResolveDead(r.Context(), serverKey, id, decision == "retry", time.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if !changed {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "no such dead letter"))
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: actor.auditActor(), Action: "task." + decision, Target: id})
	w.WriteHeader(http.StatusNoContent)
}

// The event payload may describe a private channel. Require current server-wide
// Discord read authority before returning it; the queue itself stays available
// to all Cicord admins for retry and discard.
func (s *server) handleDeadEvent(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	viewer, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	info, ok := s.rt.ServerInfo(key)
	client, connected := s.rt.Client(key)
	allowed := viewer.bearer || (ok && connected && viewer.user.ID != "" && discord.CurrentGuildOwner(client, info.ID) == viewer.user.ID)
	if !allowed && viewer.user.ID != "" {
		m, e := s.lookupMember(r.Context(), key, viewer.user.ID, true)
		if e != nil {
			s.fail(w, e)
			return
		}
		if m != nil && connected && ok {
			roles, err := client.ListRoles(r.Context(), info.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			allowed = slices.ContainsFunc(roles, func(role discord.Role) bool {
				return (role.ID == info.ID || m.HasRole(role.ID)) && role.Permissions.Has(discord.MustPermissions("Administrator"))
			})
		}
	}
	if !allowed {
		s.fail(w, httpErr(403, "forbidden", "reading raw event data requires Discord Administrator access"))
		return
	}
	data, err := s.rt.Store().Interop().DeadEvent(r.Context(), key, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(data) == 0 {
		s.fail(w, httpErr(404, "not_found", "no such event dead letter"))
		return
	}
	if gate, ok := any(s.rt).(interface {
		ErasedPayload(context.Context, string, string, json.RawMessage) (bool, error)
	}); ok {
		erased, err := gate.ErasedPayload(r.Context(), key, "", data)
		if err != nil {
			s.fail(w, err)
			return
		}
		if erased {
			s.fail(w, httpErr(404, "not_found", "event data erased"))
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, data)
}
