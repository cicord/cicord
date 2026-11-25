package api

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/version"
)

// sseEvent is one event of a Server-Sent Events stream.
type sseEvent struct{ name, data string }

// openEvents opens GET /api/v1/events with the client's cookies and returns a function
// reading the next event (pings skipped).
func openEvents(t *testing.T, c *client) (func() sseEvent, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.e.ts.URL+"/api/v1/events", nil)
	res, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %s", ct)
	}
	sc := bufio.NewScanner(res.Body)
	return func() sseEvent {
		t.Helper()
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "" && ev.name != "":
				return ev
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
		t.Fatalf("event stream ended: %v", sc.Err())
		return ev
	}, http.StatusOK
}

func TestEvents(t *testing.T) {
	e := start(t)
	if _, status := openEvents(t, e.client()); status != http.StatusUnauthorized {
		t.Errorf("anonymous = %d", status)
	}
	alice := e.seed.Users[0]
	if err := e.world.AddMember(e.serverID(), alice.ID); err != nil {
		t.Fatal(err)
	}
	member := e.client()
	member.login(alice.ID)
	if _, status := openEvents(t, member); status != http.StatusForbidden {
		t.Errorf("member = %d", status)
	}

	owner := e.client()
	owner.login(e.seed.Owner.ID)
	next, status := openEvents(t, owner)
	if status != http.StatusOK {
		t.Fatalf("owner = %d", status)
	}
	if ev := next(); ev.name != "hello" || !strings.Contains(ev.data, `"version":"`+version.Version+`"`) || !strings.Contains(ev.data, `"revision":"`) {
		t.Fatalf("first event = %+v", ev)
	}
	// A sync records the server: a "sync" hint for that server, without content.
	owner.expect(200, "POST", "/api/v1/servers/"+serverKey+"/sync", nil)
	for {
		ev := next()
		if ev.name == "sync" {
			if ev.data != `{"server":"`+serverKey+`"}` {
				t.Errorf("sync event data = %s", ev.data)
			}
			break
		}
	}
	// A Store change (a registry refresh found a new release): a "store" hint, no server.
	e.rt.NotifyStore()
	for {
		if ev := next(); ev.name == "store" {
			if ev.data != `{}` {
				t.Errorf("store event data = %s", ev.data)
			}
			break
		}
	}
}

// TestEventsStopping: on shutdown the stream says "stopping" before it ends (a proxy can
// keep its side of a closed connection open).
func TestEventsStopping(t *testing.T) {
	stopping := make(chan struct{})
	e := startWith(t, func(o *Options) { o.Stopping = stopping })
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	next, status := openEvents(t, owner)
	if status != http.StatusOK {
		t.Fatalf("owner = %d", status)
	}
	if ev := next(); ev.name != "hello" {
		t.Fatalf("first event = %+v", ev)
	}
	close(stopping)
	if ev := next(); ev.name != "stopping" {
		t.Fatalf("after shutdown = %+v", ev)
	}
}
