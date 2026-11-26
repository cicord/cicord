package runtime

import (
	"context"
	"crypto/rand"
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
}

type cronJob struct {
	inst *instanceRT
	name string
	expr string
	next time.Time
}

func newScheduler(r *Runtime) *scheduler {
	return &scheduler{r: r, tick: time.Second, worker: rand.Text(), slots: make(chan struct{}, 64)}
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
	tasks, err := queue.ClaimDue(ctx, now, now.Add(2*timeout), s.worker, space)
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
		if i.inst() != nil {
			byScope[instanceScope(i.cfg)] = i
		}
	}
	for _, task := range tasks {
		if task.Scope == overlayTaskScope {
			s.slots <- struct{}{}
			go s.r.deliverOverlay(ctx, task)
			continue
		}
		inst := byScope[task.Scope]
		if inst == nil || !inst.plugin.def.HasTask(task.Name) {
			retry := now.Add(time.Minute)
			reason := ""
			if !now.Before(task.RunAt.Add(24 * time.Hour)) {
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
	_, err := s.r.WithReplayPrivacy(c, inst.cfg.Server, task.Payload, func() error {
		return inst.inst().RunTask(c, task.Name, task.Payload, plugin.TaskDelivery{ID: task.ID, Attempt: task.Attempt})
	})
	s.r.handlerDone(inst, err)
	if ctx.Err() != nil {
		return // shutdown leaves the lease recoverable
	}
	if err == nil {
		err = queue.Acknowledge(ctx, task)
	} else {
		s.r.log.Warn("task handler failed", "instance", inst.id(), "id", task.ID, "attempt", task.Attempt, "err", err)
		// Never persist arbitrary plugin error text with the administrative record.
		var retry time.Time
		if task.Failures < 4 {
			retry = time.Now().Add(time.Second << task.Failures)
		}
		err = queue.Release(ctx, task, retry, "handler failed")
	}
	if err != nil {
		s.r.log.Warn("settle task delivery", "instance", inst.id(), "id", task.ID, "err", err)
	}
}
