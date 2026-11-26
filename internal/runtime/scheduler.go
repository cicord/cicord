package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	mrand "math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- retry jitter, not a secret
	"sync"
	"time"

	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

// scheduler runs one-shot tasks (persisted in the store) and cron schedules of the active
// revision's plugin instances. Ticks every second.
type scheduler struct {
	r      *Runtime
	mu     sync.Mutex
	jobs   []*cronJob
	tick   time.Duration
	worker string
	slots  chan struct{}
	// wake asks the loop to claim again before the next tick: the next event of a
	// stream becomes eligible only once its predecessor is acknowledged.
	wake chan struct{}
}

type cronJob struct {
	inst *instanceRT
	name string
	expr string
	next time.Time
}

func newScheduler(r *Runtime) *scheduler {
	return &scheduler{r: r, tick: time.Second, worker: rand.Text(), slots: make(chan struct{}, 64), wake: make(chan struct{}, 1)}
}

// retryDelays is the SDK-01 backoff (1 s → 5 min): five deliveries, then a dead letter.
var retryDelays = [...]time.Duration{time.Second, 7 * time.Second, 45 * time.Second, 5 * time.Minute}

// retryDelay is the wait before the delivery that follows failure number failures+1, or
// 0 when that failure is the last one. Up to 20% jitter (never above the nominal delay)
// spreads the retries of tasks that failed together, e.g. during a Discord outage.
func retryDelay(failures int) time.Duration {
	if failures < 0 || failures >= len(retryDelays) {
		return 0
	}
	d := retryDelays[failures]
	return d - time.Duration(mrand.Int64N(int64(d)/5+1)) //nolint:gosec // retry jitter, not a secret
}

// wakeUp asks for another claim without waiting for the next tick.
func (s *scheduler) wakeUp() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *scheduler) reset(a *active) {
	now := time.Now().UTC()
	var jobs []*cronJob
	for _, i := range a.instances {
		if i.inst() == nil {
			continue
		}
		for _, sd := range i.plugin.def.Schedules {
			next, err := plugin.NextRun(sd.Cron, now)
			if err != nil {
				continue
			}
			jobs = append(jobs, &cronJob{inst: i, name: sd.Name, expr: sd.Cron, next: next})
		}
	}
	s.mu.Lock()
	s.jobs = jobs
	s.mu.Unlock()
}

func (s *scheduler) run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	sweep := time.NewTicker(10 * time.Minute)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.runCron(ctx, now.UTC())
			s.runTasks(ctx, now.UTC())
		case <-s.wake:
			s.runTasks(ctx, time.Now().UTC())
		case <-sweep.C:
			if _, err := s.r.opts.Store.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.r.log.Warn("state sweep failed", "err", err)
			}
		}
	}
}

func (s *scheduler) runCron(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var due []*cronJob
	for _, j := range s.jobs {
		if !now.Before(j.next) {
			due = append(due, j)
			if n, err := plugin.NextRun(j.expr, now); err == nil {
				j.next = n
			}
		}
	}
	s.mu.Unlock()
	for _, j := range due {
		go func() {
			c, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			err := j.inst.inst().RunSchedule(c, j.name)
			s.r.handlerDone(j.inst, err)
			if err != nil {
				s.r.log.Warn("schedule failed", "instance", j.inst.id(), "schedule", j.name, "err", err)
			}
		}()
	}
}

func (s *scheduler) runTasks(ctx context.Context, now time.Time) {
	a := s.r.Active()
	if a == nil {
		return
	}
	space := cap(s.slots) - len(s.slots)
	if space == 0 {
		return
	}
	timeout := s.r.opts.PluginLimits.HandlerTimeout
	if timeout <= 0 {
		timeout = plugin.DefaultHandlerTimeout
	}
	queue := s.r.opts.Store.Tasks()
	var paused []string
	for _, i := range a.instances {
		// A crashed instance stays installed until its supervisor restarts it. Its
		// tasks wait like a disabled instance's: claiming them would only fail and
		// inflate their attempts while nothing can run.
		if cur := i.inst(); i.cfg.Disabled() || (cur != nil && cur.Err() != nil) {
			paused = append(paused, instanceScope(i.cfg))
		}
	}
	tasks, err := queue.ClaimDue(ctx, now, now.Add(2*timeout), s.worker, space, paused...)
	if err != nil {
		if ctx.Err() == nil {
			s.r.log.Warn("claim due tasks", "err", err)
		}
		return
	}
	if len(tasks) == 0 {
		return
	}
	byScope := map[string]*instanceRT{}
	for _, i := range a.instances {
		byScope[instanceScope(i.cfg)] = i
	}
	for _, task := range tasks {
		if task.Scope == overlayTaskScope {
			s.slots <- struct{}{}
			go s.r.deliverOverlay(ctx, task)
			continue
		}
		inst := byScope[task.Scope]
		if inst == nil || inst.inst() == nil || (!task.Event && !inst.plugin.def.HasTask(task.Name)) {
			retry := now.Add(time.Minute)
			reason := ""
			// An installed plugin may be unable to start because its erasure
			// migration failed. Its original deadline remains recoverable until repair.
			protecting := false
			if inst != nil && !task.Event && inst.cfg.Plugin.Manifest.Spec.Privacy != nil {
				erased, err := s.r.ErasedPayload(ctx, inst.cfg.Server, task.Scope, task.Payload)
				protecting = erased || err != nil
			}
			if !protecting && !now.Before(task.RunAt.Add(24*time.Hour)) {
				retry = time.Time{}
				reason = "handler unavailable for 24 hours"
			}
			if err := queue.Release(ctx, task, retry, reason); err != nil {
				s.r.log.Warn("defer orphan task", "id", task.ID, "err", err)
			}
			continue
		}
		s.slots <- struct{}{}
		go s.deliverTask(ctx, inst, task, now.Add(timeout))
	}
}

func (s *scheduler) deliverTask(ctx context.Context, inst *instanceRT, task state.TaskLease, deadline time.Time) {
	queue := s.r.opts.Store.Tasks()
	defer func() { <-s.slots }()
	c, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	legacy := inst.cfg.Plugin.Manifest.Spec.Delivery != "at-least-once"
	if legacy {
		c = plugin.BeforeDelivery(c, func(ctx context.Context) error {
			taken, err := queue.Consume(ctx, task)
			if err != nil {
				return err
			}
			if !taken {
				return plugin.ErrDeliverySuperseded
			}
			return nil
		})
	}
	err := checkedTask(c, inst, task, s.r)
	if !errors.Is(err, errSubscriptionUnavailable) {
		s.r.handlerDone(inst, err)
	}
	if ctx.Err() != nil {
		return // shutdown leaves the lease recoverable
	}
	switch {
	case errors.Is(err, plugin.ErrStopped), errors.Is(err, plugin.ErrDeliverySuperseded):
		err = queue.Release(ctx, task, time.Now(), "")
	case err == nil:
		err = queue.Acknowledge(ctx, task)
		if task.Event {
			s.wakeUp()
		}
	case errors.Is(err, errPrivacyRetry):
		// Keep the original legal enforcement timer recoverable while its plugin
		// repairs member state. A blocked privacy operation is not a delivery failure.
		err = queue.Release(ctx, task, time.Now().Add(time.Minute), "")
	case errors.Is(err, errSubscriptionUnavailable):
		// A revision dropped or re-versioned the subscription, or its grant. Like an
		// undeclared task: no failure, parked after 24 hours. A retry cannot succeed
		// until the subscription is declared again.
		retry, reason := time.Now().Add(time.Minute), ""
		if !time.Now().Before(task.RunAt.Add(24 * time.Hour)) {
			retry, reason = time.Time{}, "subscription no longer declared for 24 hours"
		}
		err = queue.Release(ctx, task, retry, reason)
	case errors.Is(err, plugin.ErrCrashed) && !errors.Is(err, plugin.ErrCrashedByHandler):
		// Another handler crashed the VM while this one waited or ran. The supervisor
		// restarts the instance; this delivery did not fail (CORE-R1).
		err = queue.Release(ctx, task, time.Now(), "")
	default:
		s.r.log.Warn("task handler failed", "instance", inst.id(), "id", task.ID, "attempt", task.Attempt, "err", err)
		var retry time.Time
		if d := retryDelay(task.Failures); d > 0 {
			retry = time.Now().Add(d)
		}
		err = queue.Release(ctx, task, retry, deliveryFailure(err))
	}
	if err != nil {
		s.r.log.Warn("settle task delivery", "instance", inst.id(), "id", task.ID, "err", err)
	}
}

// errSubscriptionUnavailable: a queued event whose subscription the active revision no
// longer declares (topic, version or events.subscribe grant).
var errSubscriptionUnavailable = errors.New("event subscription unavailable")

// deliveryFailure classifies a failed delivery for its dead-letter record (SDK-01:
// subscriber, error, payload). Plugin error text and plugin-chosen error names can carry
// member data, so only host-defined categories and the names of built-in JS errors are
// persisted; the full error stays in the runtime log.
func deliveryFailure(err error) string {
	switch {
	case errors.Is(err, plugin.ErrCrashedByHandler):
		return "handler crashed the instance"
	case errors.Is(err, plugin.ErrHandlerTimeout), errors.Is(err, context.DeadlineExceeded):
		return "handler timed out"
	case errors.Is(err, plugin.ErrBudgetExceeded):
		return "handler exceeded its execution budget"
	case errors.Is(err, plugin.ErrNoHandler):
		return "no handler"
	case errors.Is(err, plugin.ErrBusy):
		return "instance busy"
	}
	var se *plugin.ScriptError
	if errors.As(err, &se) {
		switch {
		case se.Capability != "":
			return "handler failed: capability denied"
		case se.Name == "InteropError":
			switch se.InteropCode {
			case "ambiguous", "cycle", "denied", "handler_error", "invalid_input", "not_found", "timeout":
				return "handler failed: interop " + se.InteropCode
			}
			return "handler failed: interop error"
		}
		switch se.Name {
		case "Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "EvalError", "URIError", "AggregateError", "InternalError":
			return "handler failed: " + se.Name
		}
		return "handler failed: script error"
	}
	return "handler failed"
}
