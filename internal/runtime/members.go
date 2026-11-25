package runtime

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// memberListTTL is how long a server's member listing is reused by role.members() before
// it is listed again. Member events keep it current in between; the TTL bounds what a
// missed event (a bot without the ServerMembers gateway intent, a reconnect) can hide.
const memberListTTL = 60 * time.Second

// memberListTimeout bounds one listing (1000 members per request, rate limited). It is
// detached from the calling handler: a slow first listing still fills the cache.
const memberListTimeout = 2 * time.Minute

// memberList is a server's member listing for role.members(), shared by the plugin
// instances of the server (Discord has no "members of a role" endpoint). One listing runs
// at a time; concurrent callers wait for it. Member events patch the listing in place
// (joins, updates, leaves), also while a listing is in flight (they are replayed onto it).
type memberList struct {
	mu      sync.Mutex
	now     func() time.Time
	members map[string]discord.Member // user ID → member; nil: not listed
	at      time.Time                 // when members was listed
	fetch   *memberFetch              // listing in flight
	pending []discord.Event           // member events seen while fetch is in flight
}

type memberFetch struct {
	done    chan struct{}
	members []discord.Member
	err     error
}

func newMemberList() *memberList { return &memberList{now: time.Now} }

// List returns the server's members in ascending user ID order, listing them with cl when
// the cached listing is missing or older than memberListTTL.
func (l *memberList) List(ctx context.Context, cl discord.Client, serverID string) ([]discord.Member, error) {
	ml, ok := cl.(discord.MemberLister)
	if !ok {
		return nil, errors.New("this Discord client cannot list members")
	}
	l.mu.Lock()
	if l.members != nil && l.now().Sub(l.at) < memberListTTL {
		out := l.snapshotLocked()
		l.mu.Unlock()
		return out, nil
	}
	f := l.fetch
	if f == nil {
		f = &memberFetch{done: make(chan struct{})}
		l.fetch, l.pending = f, nil
		go l.run(f, ml, serverID) //nolint:gosec // G118: shared by every caller, it outlives the first one (memberListTimeout bounds it)
	}
	l.mu.Unlock()
	select {
	case <-f.done:
		return f.members, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *memberList) run(f *memberFetch, ml discord.MemberLister, serverID string) {
	ctx, cancel := context.WithTimeout(context.Background(), memberListTimeout)
	defer cancel()
	ms, err := ml.ListMembers(ctx, serverID)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fetch = nil
	if err != nil {
		f.err = err
		l.pending = nil
		close(f.done)
		return
	}
	l.members = make(map[string]discord.Member, len(ms))
	for _, m := range ms {
		l.members[m.User.ID] = m
	}
	l.at = l.now()
	for _, ev := range l.pending {
		l.applyLocked(ev)
	}
	l.pending = nil
	f.members = l.snapshotLocked()
	close(f.done)
}

// Observe patches the listing from member events; leaving the server drops it.
func (l *memberList) Observe(ev discord.Event) {
	switch ev.(type) {
	case discord.MemberJoinEvent, discord.MemberUpdateEvent, discord.MemberLeaveEvent, discord.ServerLeaveEvent:
	default:
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, leave := ev.(discord.ServerLeaveEvent); leave {
		l.members, l.pending = nil, nil
		return
	}
	if l.fetch != nil {
		l.pending = append(l.pending, ev)
	}
	l.applyLocked(ev)
}

func (l *memberList) applyLocked(ev discord.Event) {
	if l.members == nil {
		return
	}
	switch e := ev.(type) {
	case discord.MemberJoinEvent:
		l.members[e.Member.User.ID] = e.Member
	case discord.MemberUpdateEvent:
		l.members[e.Member.User.ID] = e.Member
	case discord.MemberLeaveEvent:
		delete(l.members, e.User.ID)
	}
}

func (l *memberList) snapshotLocked() []discord.Member {
	out := make([]discord.Member, 0, len(l.members))
	for _, m := range l.members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return discord.SnowflakeLess(out[i].User.ID, out[j].User.ID) })
	return out
}
