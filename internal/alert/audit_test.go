package alert

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/runtime"
)

// CORE-04: audited actions by a project bot that Cicord did not emit reach the
// operators as one incident per burst, never as a log line only.
func TestUnconfirmedBotActivityAlerts(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	first := c.now()
	st := healthyStatus()
	st.Servers[0].UnconfirmedAudit = &runtime.UnconfirmedAudit{Bot: "cicord", Count: 1, First: first, Last: first, LastAction: "webhook.create"}
	n.Evaluate(st)
	// More entries of the same burst do not fire again.
	st.Servers[0].UnconfirmedAudit = &runtime.UnconfirmedAudit{Bot: "cicord", Count: 4, First: first, Last: first.Add(time.Second), LastAction: "role.update"}
	c.advance(time.Second)
	n.Evaluate(st)
	n.Flush(context.Background())
	got := r.all()
	if fmt.Sprint(states(got)) != fmt.Sprint([]string{"firing Bot token possibly compromised"}) {
		t.Fatalf("messages = %v", states(got))
	}
	if m := got[0]; m.Server != "showcase" || m.Bot != "cicord" || !strings.Contains(m.Detail, "1 audited action") || !strings.Contains(m.Detail, "rotate the token") {
		t.Fatalf("alert = %+v", m)
	}
	// Once the advisory expires the incident resolves after ResolveAfter.
	c.advance(10 * time.Second)
	n.Evaluate(healthyStatus())
	c.advance(3 * time.Minute)
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	if got := r.all()[1:]; len(got) != 1 || got[0].Title != "No more unconfirmed bot activity" {
		t.Fatalf("resolved: %v", states(got))
	}
}
