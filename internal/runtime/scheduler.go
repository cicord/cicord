package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/plugin"
)

// scheduler runs one-shot tasks (persisted in the store) and cron schedules of the active
// revision's plugin instances. Ticks every second.
type scheduler struct {
	r    *Runtime
	mu   sync.Mutex
	jobs []*cronJob
	tick time.Duration
}

type cronJob struct {
	inst *instanceRT
	name string
	expr string
	next time.Time
}

func newScheduler(r *Runtime) *scheduler { return &scheduler{r: r, tick: time.Second} }

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
			j.inst.setHealth(err)
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
	tasks, err := s.r.opts.Store.Tasks().Due(ctx, now, 100)
	if err != nil || len(tasks) == 0 {
		return
	}
	byScope := map[string]*instanceRT{}
	for _, i := range a.instances {
		if i.inst() != nil {
			byScope[instanceScope(i.cfg)] = i
		}
	}
	for _, t := range tasks {
		inst := byScope[t.Scope]
		if inst == nil || !inst.plugin.def.HasTask(t.Name) {
			// The instance is gone or no longer declares the task: keep it unless it is
			// more than a day overdue, so a temporarily broken revision does not lose work.
			if now.Sub(t.RunAt) > 24*time.Hour {
				_ = s.r.opts.Store.Tasks().Complete(ctx, t.ID)
			}
			continue
		}
		_ = s.r.opts.Store.Tasks().Complete(ctx, t.ID) // at-most-once delivery
		go func() {
			c, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			err := inst.inst().RunTask(c, t.Name, t.Payload)
			inst.setHealth(err)
			if err != nil {
				s.r.log.Warn("task failed", "instance", inst.id(), "task", t.Name, "err", err)
			}
		}()
	}
}
