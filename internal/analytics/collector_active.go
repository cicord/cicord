package analytics

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// Daily distinct members: a member active in a set (all, chat, voice) is recorded as
// HMAC-SHA256(day key, user ID), with a random key per UTC day kept in the KV (never
// next to the tokens). Once the day is over, the distinct counts are rolled up into
// the daily metrics and every token of the day is deleted.

// KV keys of the "analytics" scope.
const (
	kvDayKeyPrefix = "daykey:"
	kvRolledUp     = "rollup:last" // last day rolled up ("YYYY-MM-DD")
)

type activeKey struct {
	server, set, day string
}

// markActive remembers that a member was active today in the given sets. Caller holds c.mu.
func (c *Collector) markActive(s *collected, userID string, now time.Time, sets ...string) {
	if userID == "" || c.botUsers[userID] {
		return
	}
	day := dayOf(now)
	for _, set := range sets {
		k := activeKey{server: s.cfg.Key, set: set, day: day}
		m := c.pending[k]
		if m == nil {
			m = map[string]struct{}{}
			c.pending[k] = m
		}
		if _, ok := m[userID]; ok {
			continue
		}
		if c.npending >= maxPendingActive {
			return
		}
		m[userID] = struct{}{}
		c.npending++
	}
}

// flushActive marks the pending members in the store (as tokens). Failed ones go back
// to pending. Caller holds tickMu.
func (c *Collector) flushActive(ctx context.Context, pending map[activeKey]map[string]struct{}) {
	keys := make([]activeKey, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.day != b.day {
			return a.day < b.day
		}
		if a.server != b.server {
			return a.server < b.server
		}
		return a.set < b.set
	})
	var failed error
	for _, k := range keys {
		users := pending[k]
		if failed != nil {
			c.requeue(k, users)
			continue
		}
		dk, err := c.dayKey(ctx, k.day)
		if err != nil {
			failed = err
			c.requeue(k, users)
			continue
		}
		seen := c.seen[k]
		if seen == nil {
			seen = map[string]struct{}{}
			c.seen[k] = seen
		}
		left := map[string]struct{}{}
		for uid := range users {
			if failed != nil {
				left[uid] = struct{}{}
				continue
			}
			tok := activeToken(dk, uid)
			if _, ok := seen[tok]; ok {
				continue
			}
			if err := c.opts.Store.MarkActive(ctx, k.server, k.set, k.day, tok); err != nil {
				failed = err
				left[uid] = struct{}{}
				continue
			}
			seen[tok] = struct{}{}
		}
		if len(left) > 0 {
			c.requeue(k, left)
		}
	}
	if failed != nil {
		c.log.Warn("analytics: mark active members", "err", failed)
	}
}

func (c *Collector) requeue(k activeKey, users map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.pending[k]
	if m == nil {
		m = map[string]struct{}{}
		c.pending[k] = m
	}
	for uid := range users {
		if _, ok := m[uid]; ok || c.npending >= maxPendingActive {
			continue
		}
		m[uid] = struct{}{}
		c.npending++
	}
}

// activeToken is the opaque daily token of a member: the first 16 bytes of
// HMAC-SHA256(day key, user ID), hex encoded.
func activeToken(dayKey []byte, userID string) string {
	mac := hmac.New(sha256.New, dayKey)
	mac.Write([]byte(userID))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// dayKey returns the HMAC key of a day, creating it on first use. Caller holds tickMu.
func (c *Collector) dayKey(ctx context.Context, day string) ([]byte, error) {
	if k := c.dayKeys[day]; k != nil {
		return k, nil
	}
	if c.opts.KV == nil {
		return nil, errors.New("analytics: no KV for the daily keys")
	}
	raw, err := c.opts.KV.Get(ctx, kvDayKeyPrefix+day)
	switch {
	case err == nil:
		var h string
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, fmt.Errorf("analytics: day key %s: %w", day, err)
		}
		k, err := hex.DecodeString(h)
		if err != nil || len(k) < 16 {
			return nil, fmt.Errorf("analytics: day key %s is invalid", day)
		}
		c.dayKeys[day] = k
		return k, nil
	case errors.Is(err, state.ErrNotFound):
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, err
		}
		v, _ := json.Marshal(hex.EncodeToString(k))
		if err := c.opts.KV.Set(ctx, kvDayKeyPrefix+day, v, dayKeyTTL); err != nil {
			return nil, err
		}
		c.dayKeys[day] = k
		return k, nil
	}
	return nil, err
}

// daily rolls up the days before today (once) and applies retention to every enabled
// server (once a day).
func (c *Collector) daily(ctx context.Context) {
	c.tickMu.Lock()
	defer c.tickMu.Unlock()
	if c.opts.Store == nil {
		return
	}
	now := c.now()
	today := dayOf(now)
	c.mu.Lock()
	var enabled []CollectorServer
	for _, s := range c.servers {
		if s.cfg.Enabled {
			enabled = append(enabled, s.cfg)
		}
	}
	// Members of yesterday not marked yet (store errors) hold the rollup back; older
	// ones are given up.
	yesterday := now.AddDate(0, 0, -1).Format(time.DateOnly)
	oldPending := false
	for k, users := range c.pending {
		switch {
		case k.day < yesterday:
			c.npending -= len(users)
			delete(c.pending, k)
		case k.day < today:
			oldPending = true
		}
	}
	c.mu.Unlock()
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Key < enabled[j].Key })

	if c.rolledUp != today && !oldPending && c.opts.KV != nil {
		if err := c.rollup(ctx, today, enabled); err != nil {
			c.log.Warn("analytics: daily rollup", "err", err)
		} else {
			c.rolledUp = today
		}
	}
	for _, s := range enabled {
		if c.purged[s.Key] == today {
			continue
		}
		before := now.Add(-time.Duration(s.RetentionDays) * 24 * time.Hour)
		n, err := c.opts.Store.Purge(ctx, s.Key, before)
		if err != nil {
			c.log.Warn("analytics: retention", "server", s.Key, "err", err)
			continue
		}
		c.purged[s.Key] = today
		if n > 0 {
			c.log.Info("analytics: retention applied", "server", s.Key, "removed", n, "before", before.Format(time.DateOnly))
		}
	}
}

// rollup writes the distinct counts of the days after the last rolled-up day and
// before today, then deletes their tokens and keys. Caller holds tickMu.
func (c *Collector) rollup(ctx context.Context, today string, servers []CollectorServer) error {
	todayT, _ := time.Parse(time.DateOnly, today)
	yesterday := todayT.AddDate(0, 0, -1)
	from := yesterday
	raw, err := c.opts.KV.Get(ctx, kvRolledUp)
	switch {
	case errors.Is(err, state.ErrNotFound):
		// First run: no token can predate it. Nothing to roll up.
		from = todayT
	case err != nil:
		return err
	default:
		var last string
		if err := json.Unmarshal(raw, &last); err != nil {
			return fmt.Errorf("rollup marker: %w", err)
		}
		lastT, err := time.Parse(time.DateOnly, last)
		if err != nil {
			return fmt.Errorf("rollup marker %q: %w", last, err)
		}
		from = lastT.AddDate(0, 0, 1)
		if oldest := todayT.AddDate(0, 0, -maxRollupDays); from.Before(oldest) {
			from = oldest
		}
	}
	marker, _ := json.Marshal(yesterday.Format(time.DateOnly))
	if !from.Before(todayT) { // nothing to roll up (first run, or done before a restart)
		if errors.Is(err, state.ErrNotFound) {
			return c.opts.KV.Set(ctx, kvRolledUp, marker, 0)
		}
		return nil
	}
	var pts []state.AnalyticsPoint
	var days []string
	for d := from; d.Before(todayT); d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		days = append(days, day)
		for _, s := range servers {
			for _, set := range []string{ActiveSetAll, ActiveSetChat, ActiveSetVoice} {
				n, err := c.opts.Store.CountActive(ctx, s.Key, set, day)
				if err != nil {
					return err
				}
				if n > 0 { // a zero would overwrite a count already rolled up
					pts = append(pts, state.AnalyticsPoint{Server: s.Key, Metric: ActiveSetMetrics[set], Bucket: d, Value: float64(n)})
				}
			}
		}
	}
	if len(pts) > 0 {
		if err := c.opts.Store.Record(ctx, state.AnalyticsGaugeLast, pts...); err != nil {
			return err
		}
	}
	if _, err := c.opts.Store.DropActive(ctx, today); err != nil {
		return err
	}
	if err := c.opts.KV.Set(ctx, kvRolledUp, marker, 0); err != nil {
		return err
	}
	for _, day := range days {
		if err := c.opts.KV.Delete(ctx, kvDayKeyPrefix+day); err != nil && !errors.Is(err, state.ErrNotFound) {
			c.log.Warn("analytics: delete day key", "day", day, "err", err)
		}
	}
	for k := range c.seen {
		if k.day < today {
			delete(c.seen, k)
		}
	}
	for day := range c.dayKeys {
		if day < today {
			delete(c.dayKeys, day)
		}
	}
	return nil
}
