// Package alert turns the runtime's state into notifications for a person: a bot or a
// plugin instance that becomes Degraded or crash-loops, a server whose sync fails, a
// rejected revision, a paused server, and the recovery of each
// (https://cicord.dev/docs/self-hosting/monitoring/#alerts).
//
// The notifier subscribes to the runtime's change bus (the one behind GET /api/v1/events)
// and re-reads the status on every change, and every Poll as a fallback; it diffs the
// conditions of consecutive snapshots. A new condition fires at once; a condition that
// clears is only resolved once it stayed clear for ResolveAfter, so a crash loop or a
// flapping sync is one incident: one firing message, one resolved message. Messages are
// batched and spaced by MinInterval.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/source"
)

// Source is what the notifier watches (*runtime.Runtime).
type Source interface {
	Status() runtime.Status
	Revision() (source.Revision, bool)
	SubscribeChanges() (<-chan runtime.Change, func())
}

// Options configure a Notifier. Zero durations take the defaults.
type Options struct {
	Sender Sender
	// PublicURL names the runtime in every alert (its host); AdminURL is the base URL of
	// the admin console (links to /admin/…).
	PublicURL string
	AdminURL  string
	Logger    *slog.Logger
	// Poll re-reads the status without a change hint (default 15 s).
	Poll time.Duration
	// ResolveAfter is how long a bot, plugin or server condition must stay clear before
	// it is resolved (default 5 min, the runtime's restartStable: a plugin that crashes
	// again within it is still crash-looping). Pause, resume and revisions resolve at once.
	ResolveAfter time.Duration
	// MinInterval spaces two webhook calls (default 5 s); messages queued meanwhile are
	// sent together.
	MinInterval time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Notifier watches a runtime and sends alerts. Create it with New, pass ObserveHandler as
// runtime.Options.OnHandler, then Run it.
type Notifier struct {
	opts    Options
	log     *slog.Logger
	runtime string
	admin   string

	mu       sync.Mutex
	failures map[string]*handlerFailures // instance key → its failing handlers
	revision func() string               // the active revision (set by Run)

	incidents map[string]*incident
	outbox    []Message
	lastSend  time.Time
	backoff   time.Duration
}

// handlerFailures are the handler failures of an instance: a handler (workflow) that
// failed FailureThreshold times in a row is failing, even when other handlers of the
// instance succeed in between (the runtime's instance health counts every handler).
type handlerFailures struct {
	revision string         // the revision the failures happened on
	last     string         // the last failed workflow
	cause    string         // its classified error
	streak   map[string]int // workflow → consecutive failures (removed on a success)
}

// FailureThreshold is the number of consecutive failures of a handler that makes it a
// failing workflow.
const FailureThreshold = 3

type incident struct {
	cond        condition
	since       time.Time
	clearSince  time.Time // zero while the condition holds
	fingerprint string
	flaps       int
}

// New returns a notifier.
func New(o Options) *Notifier {
	if o.Poll <= 0 {
		o.Poll = 15 * time.Second
	}
	if o.ResolveAfter <= 0 {
		o.ResolveAfter = 5 * time.Minute
	}
	if o.MinInterval <= 0 {
		o.MinInterval = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	n := &Notifier{opts: o, log: o.Logger, failures: map[string]*handlerFailures{}, incidents: map[string]*incident{}}
	n.runtime = RuntimeName(o.PublicURL)
	n.admin = strings.TrimRight(o.AdminURL, "/")
	if n.admin == "" {
		n.admin = strings.TrimRight(o.PublicURL, "/")
	}
	return n
}

// RuntimeName is the host of a public URL (the name of the runtime in alerts).
func RuntimeName(public string) string {
	if u, err := url.Parse(public); err == nil && u.Host != "" {
		return u.Host
	}
	if public == "" {
		return "cicord"
	}
	return public
}

// ObserveHandler records handler outcomes (runtime.Options.OnHandler): the alert of a
// failing instance names its failing handler. It never keeps the error text.
func (n *Notifier) ObserveHandler(in config.Instance, res plugin.HandlerResult) {
	wf := workflowOf(res.Kind, res.Name)
	k := pluginKey(in.Server, in.Bot, in.Name)
	n.mu.Lock()
	defer n.mu.Unlock()
	rev := ""
	if n.revision != nil {
		rev = n.revision()
	}
	f := n.failures[k]
	if f != nil && f.revision != rev {
		// A new revision replaced the instance: its failures start over.
		delete(n.failures, k)
		f = nil
	}
	if res.Err == nil {
		if f != nil {
			delete(f.streak, wf)
		}
		return
	}
	if f == nil {
		f = &handlerFailures{revision: rev, streak: map[string]int{}}
		n.failures[k] = f
	}
	f.last, f.cause = wf, causeOf(res.Err)
	f.streak[wf]++
}

// failing returns the failing workflows of an instance (sorted), its last failed
// workflow and the cause of that failure.
func (n *Notifier) failing(key, revision string) (workflows []string, last, cause string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	f := n.failures[key]
	if f == nil || f.revision != revision {
		return nil, "", ""
	}
	for wf, c := range f.streak {
		if c >= FailureThreshold {
			workflows = append(workflows, wf)
		}
	}
	sort.Strings(workflows)
	return workflows, f.last, f.cause
}

// Run watches src until ctx ends.
func (n *Notifier) Run(ctx context.Context, src Source) {
	n.mu.Lock()
	n.revision = func() string {
		if rev, ok := src.Revision(); ok {
			return rev.ID
		}
		return ""
	}
	n.mu.Unlock()
	ch, unsubscribe := src.SubscribeChanges()
	defer unsubscribe()
	poll := time.NewTicker(n.opts.Poll)
	defer poll.Stop()
	// Change hints come in bursts (a revision syncs every server): evaluate once after a
	// short quiet period.
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	// recheck fires when a clearing condition can be resolved.
	recheck := time.NewTimer(time.Hour)
	recheck.Stop()
	defer recheck.Stop()
	step := func() {
		n.Evaluate(src.Status())
		if d, ok := n.flushDelay(); ok {
			flush.Reset(d)
		}
		if d, ok := n.resolveDelay(); ok {
			recheck.Reset(d)
		}
	}
	step()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			debounce.Reset(300 * time.Millisecond)
		case <-debounce.C:
			step()
		case <-poll.C:
			step()
		case <-recheck.C:
			step()
		case <-flush.C:
			n.Flush(ctx)
			if d, ok := n.flushDelay(); ok {
				flush.Reset(d)
			}
		}
	}
}

// flushDelay is when the outbox can be sent (false: it is empty).
func (n *Notifier) flushDelay() (time.Duration, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.outbox) == 0 {
		return 0, false
	}
	gap := max(n.opts.MinInterval, n.backoff)
	return max(gap-n.opts.Now().Sub(n.lastSend), 0), true
}

// resolveDelay is when the next clearing condition can be resolved (false: none).
func (n *Notifier) resolveDelay() (time.Duration, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var next time.Time
	for _, inc := range n.incidents {
		if inc.clearSince.IsZero() {
			continue
		}
		at := inc.clearSince.Add(n.opts.ResolveAfter)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if next.IsZero() {
		return 0, false
	}
	return max(next.Sub(n.opts.Now()), 0) + 10*time.Millisecond, true
}

// Flush sends the queued messages now. On failure they stay queued (at most maxQueued,
// the oldest dropped) and the next attempt backs off up to 5 minutes.
func (n *Notifier) Flush(ctx context.Context) {
	n.mu.Lock()
	msgs := n.outbox
	n.outbox = nil
	n.mu.Unlock()
	if len(msgs) == 0 {
		return
	}
	err := n.opts.Sender.Send(ctx, msgs)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastSend = n.opts.Now()
	if err == nil {
		n.backoff = 0
		for _, m := range msgs {
			n.log.Info("alert sent", "state", m.State, "title", m.Title, "key", m.Key)
		}
		return
	}
	n.backoff = min(max(2*n.backoff, 30*time.Second), 5*time.Minute)
	n.log.Warn("alert not sent, retrying", "err", err, "alerts", len(msgs), "in", n.backoff)
	n.outbox = append(msgs, n.outbox...)
	if over := len(n.outbox) - maxQueued; over > 0 {
		n.log.Warn("alert queue full, dropping the oldest alerts", "dropped", over)
		n.outbox = n.outbox[over:]
	}
}

const maxQueued = 100

// Pending returns the queued messages (tests).
func (n *Notifier) Pending() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Message(nil), n.outbox...)
}

// Evaluate diffs the conditions of st against the open incidents and queues the
// messages to send.
func (n *Notifier) Evaluate(st runtime.Status) { n.evaluate(view(st)) }

func (n *Notifier) evaluate(s snapshot) {
	now := n.opts.Now()
	conds := n.conditions(s)
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, c := range conds {
		inc := n.incidents[c.key]
		switch {
		case inc == nil:
			n.incidents[c.key] = &incident{cond: c, since: now, fingerprint: c.fingerprint}
			n.queue(c, StateFiring, now, now)
		case inc.fingerprint != c.fingerprint:
			// Another occurrence of the same condition (another rejected revision).
			inc.cond, inc.fingerprint, inc.clearSince = c, c.fingerprint, time.Time{}
			n.queue(c, StateFiring, inc.since, now)
		default:
			if !inc.clearSince.IsZero() {
				inc.flaps++ // came back within ResolveAfter: the same incident
				inc.clearSince = time.Time{}
			}
			inc.cond = c
		}
	}
	active := make(map[string]bool, len(conds))
	for _, c := range conds {
		active[c.key] = true
	}
	keys := make([]string, 0, len(n.incidents))
	for k := range n.incidents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		inc := n.incidents[k]
		if active[k] {
			continue
		}
		if inc.clearSince.IsZero() {
			inc.clearSince = now
		}
		hold := n.opts.ResolveAfter
		if inc.cond.immediate {
			hold = 0
		}
		if now.Sub(inc.clearSince) < hold {
			continue
		}
		c := inc.cond
		c.detail = c.resolved
		if inc.flaps > 0 {
			c.detail = strings.TrimSpace(c.detail + " It recovered and failed again " + plural(inc.flaps, "time") + " meanwhile.")
		}
		delete(n.incidents, k)
		if strings.HasPrefix(k, "plugin/") {
			delete(n.failures, k)
		}
		n.queue(c, StateResolved, inc.since, now)
	}
}

func (n *Notifier) queue(c condition, state string, since, now time.Time) {
	m := c.msg
	m.Key, m.State, m.Since, m.Time, m.Runtime = c.key, state, since, now, n.runtime
	m.Detail = c.detail
	if state == StateResolved {
		m.Title = c.resolvedTitle
	}
	n.outbox = append(n.outbox, m)
}

// ───────────────────────────────────────────────────────────── conditions

// condition is a problem seen in a status snapshot.
type condition struct {
	key           string
	msg           Message // Severity, Title and the names; state and times are set when queued
	detail        string
	fingerprint   string // a change fires again
	resolvedTitle string
	resolved      string // detail of the resolved message
	immediate     bool   // resolved without ResolveAfter
}

// snapshot is the part of runtime.Status the notifier reads, decoded from its JSON form:
// fields the runtime adds later (a server's pause) are read without a build dependency.
type snapshot struct {
	Revision *struct {
		ID string `json:"id"`
	} `json:"revision"`
	LastError string `json:"lastError"`
	Servers   []struct {
		Key     string `json:"key"`
		Name    string `json:"name"`
		Health  string `json:"health"`
		Message string `json:"message"`
		Bot     string `json:"bot"`
		Paused  *struct {
			By     string    `json:"by"`
			ByName string    `json:"byName"`
			At     time.Time `json:"at"`
			Reason string    `json:"reason"`
		} `json:"paused"`
	} `json:"servers"`
	Bots []struct {
		Key     string `json:"key"`
		Health  string `json:"health"`
		Message string `json:"message"`
		Plugins []struct {
			Instance string `json:"instance"`
			Plugin   string `json:"plugin"`
			Version  string `json:"version"`
			Server   string `json:"server"`
			Health   string `json:"health"`
			Message  string `json:"message"`
		} `json:"plugins"`
	} `json:"bots"`
}

func view(st runtime.Status) snapshot {
	var s snapshot
	data, err := json.Marshal(st)
	if err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func pluginKey(server, bot, instance string) string {
	return "plugin/" + server + "/" + bot + "/" + instance
}

func (n *Notifier) link(path string) string {
	if n.admin == "" {
		return ""
	}
	return n.admin + "/admin" + path
}

func serverPath(key, page string) string {
	p := "/servers/" + url.PathEscape(key)
	if page != "" {
		p += "/" + page
	}
	return p
}

// conditions lists the problems of a snapshot.
func (n *Notifier) conditions(s snapshot) []condition {
	var out []condition
	rev := ""
	if s.Revision != nil {
		rev = s.Revision.ID
	}
	names := map[string]string{}
	for _, sv := range s.Servers {
		names[sv.Key] = sv.Name
	}

	// The revision (or the source fetch) failed: the previous revision keeps running.
	if s.LastError != "" {
		first := firstLine(s.LastError)
		c := condition{key: "runtime/revision", fingerprint: first, immediate: true, detail: sanitize(first),
			msg:           Message{Severity: SeverityCritical, Title: "Revision rejected", Workflow: "revision activation", Revision: rejectedRevision(first), Link: n.link("/")},
			resolvedTitle: "Revision active again", resolved: "A revision was activated."}
		if strings.HasPrefix(first, "fetch:") {
			c.msg.Title, c.msg.Severity, c.msg.Workflow, c.msg.Revision = "Source fetch failing", SeverityWarning, "source fetch", rev
			c.resolvedTitle, c.resolved = "Source fetch recovered", "The source was fetched and a revision activated."
		} else {
			c.detail = strings.TrimSpace(c.detail + " The previous revision keeps running.")
		}
		if rev != "" && c.msg.Revision != rev {
			c.detail += " Active revision: " + rev + "."
		}
		out = append(out, c)
	}

	for _, sv := range s.Servers {
		switch sv.Health {
		case runtime.HealthDegraded, runtime.HealthMissing:
			title, detail := "Server sync failing", sanitize(sv.Message)
			if sv.Health == runtime.HealthMissing {
				title = "Server unreachable"
			}
			out = append(out, condition{key: "server/" + sv.Key, detail: detail,
				msg:           Message{Severity: SeverityCritical, Title: title, Server: sv.Key, ServerName: sv.Name, Bot: sv.Bot, Workflow: "sync", Revision: rev, Link: n.link(serverPath(sv.Key, "history"))},
				resolvedTitle: "Server sync recovered", resolved: "The server is " + runtime.HealthHealthy + " again."})
		}
		if p := sv.Paused; p != nil {
			by := p.ByName
			if by == "" {
				by = p.By
			}
			detail := "Cicord does not change this server until it is resumed."
			if by != "" {
				detail = "Paused by " + sanitize(by) + ". " + detail
			}
			if p.Reason != "" {
				detail += " Reason: " + clip(sanitize(p.Reason), 200)
			}
			out = append(out, condition{key: "server/" + sv.Key + "/paused", immediate: true, detail: detail,
				msg:           Message{Severity: SeverityWarning, Title: "Server paused", Server: sv.Key, ServerName: sv.Name, Bot: sv.Bot, Workflow: "pause", Revision: rev, Link: n.link(serverPath(sv.Key, ""))},
				resolvedTitle: "Server resumed", resolved: "Cicord enforces this server's configuration again."})
		}
	}

	for _, b := range s.Bots {
		degradedPlugin := false
		for _, p := range b.Plugins {
			key := pluginKey(p.Server, b.Key, p.Instance)
			workflows, last, cause := n.failing(key, rev)
			down := p.Health == runtime.HealthDegraded || p.Health == runtime.HealthMissing
			if !down && len(workflows) == 0 {
				continue
			}
			degradedPlugin = degradedPlugin || down
			title, workflow := "Plugin handler failing", strings.Join(workflows, ", ")
			detail := "It failed " + strconv.Itoa(FailureThreshold) + " times in a row or more."
			if cause != "" {
				detail += " Last error: " + cause + "."
			}
			if down {
				title = "Plugin instance " + strings.ToLower(p.Health)
				if workflow == "" {
					workflow = last
				}
				switch {
				case strings.Contains(p.Message, "(restarting in "):
					detail = "The instance crashed; the runtime restarts it with a growing delay (crash loop)."
				case cause != "":
					detail = "Its handlers fail. Last error: " + cause + "."
				case p.Health == runtime.HealthMissing:
					detail = "The instance is not running."
				default:
					detail = "Its handlers fail repeatedly."
				}
			}
			detail += " The error and the plugin logs are in the admin console."
			name := p.Plugin
			if p.Version != "" {
				name += "@" + p.Version
			}
			out = append(out, condition{key: key, detail: detail,
				msg: Message{Severity: SeverityCritical, Title: title, Server: p.Server, ServerName: names[p.Server],
					Bot: b.Key, Instance: p.Instance, Plugin: name, Workflow: workflow, Revision: rev, Link: n.link(serverPath(p.Server, "plugins"))},
				resolvedTitle: "Plugin instance healthy again", resolved: "The instance is " + runtime.HealthHealthy + " and its handlers succeed again (or a new revision replaced it)."})
		}
		if b.Health == runtime.HealthMissing || (b.Health == runtime.HealthDegraded && !degradedPlugin) {
			title := "Bot offline"
			if b.Health == runtime.HealthDegraded {
				title = "Bot degraded"
			}
			out = append(out, condition{key: "bot/" + b.Key, detail: sanitize(b.Message),
				msg:           Message{Severity: SeverityCritical, Title: title, Bot: b.Key, Workflow: "gateway connection", Revision: rev, Link: n.link("/")},
				resolvedTitle: "Bot connected again", resolved: "The bot is " + runtime.HealthHealthy + " again."})
		}
	}
	return out
}

var rejectedRe = regexp.MustCompile(`^revision (\S+) rejected`)

func rejectedRevision(line string) string {
	if m := rejectedRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

func workflowOf(kind, name string) string {
	switch {
	case name == "":
		return kind
	case kind == "command":
		return "command /" + name
	}
	return kind + " " + name
}

var identRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,40}$`)

// causeOf classifies a handler error without its text (which the plugin wrote and may
// hold user input).
func causeOf(err error) string {
	var se *plugin.ScriptError
	var ce *plugin.CapabilityError
	switch {
	case errors.Is(err, plugin.ErrCrashed):
		return "the instance crashed"
	case errors.Is(err, plugin.ErrOutOfMemory):
		return "memory limit exceeded"
	case errors.Is(err, plugin.ErrHandlerTimeout):
		return "the handler timed out"
	case errors.Is(err, plugin.ErrBudgetExceeded):
		return "CPU budget exceeded"
	case errors.Is(err, plugin.ErrBusy):
		return "too many queued calls"
	case errors.As(err, &ce):
		return "missing capability " + ce.Capability
	case errors.As(err, &se):
		if identRe.MatchString(se.Name) {
			return "uncaught " + se.Name
		}
		return "uncaught exception"
	}
	return "the handler failed"
}

var (
	urlCredRe  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)
	urlQueryRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s?#]+)[?#]\S*`)
	tokenRe    = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|[A-Za-z0-9_-]{23,28}\.[A-Za-z0-9_-]{6,7}\.[A-Za-z0-9_-]{27,})\b`)
)

// sanitize keeps the first line of a runtime message, without URL credentials or
// queries, token-like strings, and at most 300 characters.
func sanitize(s string) string {
	s = firstLine(strings.TrimSpace(s))
	s = urlCredRe.ReplaceAllString(s, "${1}[redacted]@")
	s = urlQueryRe.ReplaceAllString(s, "${1}")
	s = tokenRe.ReplaceAllString(s, "[redacted]")
	return clip(s, 300)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
