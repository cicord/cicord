package runtime

// End-to-end tests of the showcase plugins reaction-roles, audit-log and announcements on the
// Discord simulator (same harness as showcase_test.go). Each test maps to the sections of
// docs/plugin-coverage.md about these plugins.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// ───────────────────────────────────────────────────────────── reaction-roles

func TestShowcaseReactionRoles(t *testing.T) {
	s := startShowcase(t, showcaseOpts{})
	w := s.world
	alice := s.member("alice", "Member")
	bob := s.member("bob") // no role: cannot use the "pings" panel (requiredRole: member)
	rolesCh := s.channel("roles")
	red, blue, green := s.role("Red"), s.role("Blue"), s.role("Green")
	events, chess := s.role("Events"), s.role("Chess")
	hasRole := func(u discord.User, r discord.Role) bool {
		m, _ := w.Member(s.serverID, u.ID)
		return m.HasRole(r.ID)
	}

	// onStart posted the three panels; the reaction panel got the bot's reactions.
	colors := s.waitMessage("colors panel", rolesCh.ID, func(m discord.Message) bool {
		return hasComponent(m, cid(t, "reaction-roles", "rr:colors:"+red.ID))
	})
	pings := s.waitMessage("pings panel", rolesCh.ID, func(m discord.Message) bool {
		return hasComponent(m, cid(t, "reaction-roles", "rrs:pings"))
	})
	games := s.waitMessage("games panel", rolesCh.ID, func(m discord.Message) bool {
		return strings.Contains(textOf(m), "What do you play?")
	})
	s.waitFor("panel reactions", func() bool {
		r := w.Reactions(games.ID)
		return len(r["⛏️"]) == 1 && len(r["♟️"]) == 1
	})

	// Buttons, single mode: Red, then Blue replaces it. Answers are private.
	click := func(u discord.User, msg discord.Message, local string) string {
		st := wait(t, w.ClickButton(u.ID, msg, cid(t, "reaction-roles", local)))
		if !st.Ephemeral || st.ResponseType != discord.ResponseMessage {
			t.Errorf("%s: the answer is not ephemeral: %+v", local, st)
		}
		return replyText(st)
	}
	if got := click(alice, colors, "rr:colors:"+red.ID); !strings.Contains(got, "Added <@&"+red.ID+">") {
		t.Fatalf("red = %q", got)
	}
	s.waitFor("red given", func() bool { return hasRole(alice, red) })
	if got := click(alice, colors, "rr:colors:"+blue.ID); !strings.Contains(got, "Removed <@&"+red.ID+">") {
		t.Fatalf("blue = %q", got)
	}
	s.waitFor("red swapped for blue", func() bool { return hasRole(alice, blue) && !hasRole(alice, red) })

	// Select menu with a required role.
	st := wait(t, w.Select(bob.ID, pings, cid(t, "reaction-roles", "rrs:pings"), []string{events.ID}))
	if !strings.Contains(replyText(st), "You need the **Member** role") {
		t.Fatalf("bob select = %q", replyText(st))
	}
	wait(t, w.Select(alice.ID, pings, cid(t, "reaction-roles", "rrs:pings"), []string{events.ID}))
	s.waitFor("events role", func() bool { return hasRole(alice, events) })

	// Reactions (partial message events).
	if err := w.AddReaction(alice.ID, rolesCh.ID, games.ID, "♟️"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("chess role", func() bool { return hasRole(alice, chess) })
	if err := w.RemoveReaction(alice.ID, rolesCh.ID, games.ID, "♟️"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("chess role removed", func() bool { return !hasRole(alice, chess) })

	// /role-panel is idempotent: nothing is posted again.
	before := len(w.Messages(rolesCh.ID))
	st = wait(t, w.InvokeCommand(s.owner.ID, s.serverID, rolesCh.ID, s.appID, "role-panel", nil))
	if got := replyText(st); !st.Ephemeral || strings.Count(got, "already up to date") != 3 {
		t.Fatalf("/role-panel = %q", got)
	}
	if h := w.InvokeCommand(alice.ID, s.serverID, rolesCh.ID, s.appID, "role-panel", nil); h.Err() == nil {
		t.Fatal("/role-panel requires ManageRoles")
	}
	// A deleted panel is posted again.
	if err := w.AdminDeleteMessage(rolesCh.ID, colors.ID); err != nil {
		t.Fatal(err)
	}
	st = wait(t, w.InvokeCommand(s.owner.ID, s.serverID, rolesCh.ID, s.appID, "role-panel", map[string]any{"panel": "colors"}))
	if !strings.Contains(replyText(st), "posted again") || len(w.Messages(rolesCh.ID)) != before {
		t.Fatalf("repost = %q (%d messages, %d before)", replyText(st), len(w.Messages(rolesCh.ID)), before)
	}

	// Role hierarchy: a role above the bot's cannot be given, and the member is told why.
	var botRole discord.Role
	for _, r := range w.Roles(s.serverID) {
		if r.Managed && r.Position > botRole.Position {
			botRole = r
		}
	}
	if err := w.AdminSetRolePositions(s.serverID, []discord.RolePosition{{ID: green.ID, Position: botRole.Position + 1}}); err != nil {
		t.Fatal(err)
	}
	reposted := s.waitMessage("colors panel again", rolesCh.ID, func(m discord.Message) bool {
		return m.ID != colors.ID && hasComponent(m, cid(t, "reaction-roles", "rr:colors:"+green.ID))
	})
	if got := click(alice, reposted, "rr:colors:"+green.ID); !strings.Contains(got, "my highest role must be above it") {
		t.Fatalf("hierarchy = %q", got)
	}
	if !hasRole(alice, blue) {
		t.Error("a failed swap removed the current role")
	}
}

// ───────────────────────────────────────────────────────────── audit-log

func TestShowcaseAuditLog(t *testing.T) {
	s := startShowcase(t, showcaseOpts{})
	w := s.world
	ctx := context.Background()
	auditCh := s.channel("audit-log")
	general := s.channel("general")
	mia := s.member("mia", "Moderator")
	alice := s.member("alice")

	// A message, its deletion (the author was seen at creation), a role change.
	msg, err := w.SendMessage(alice.ID, general.ID, "oops")
	if err != nil {
		t.Fatal(err)
	}
	w.WaitIdle()
	if err := w.AdminDeleteMessage(general.ID, msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.AdminAddMemberRole(s.serverID, alice.ID, s.role("Member").ID); err != nil {
		t.Fatal(err)
	}
	// A poll closed by its author: polls.closed reaches the audit log.
	wait(t, w.InvokeCommand(alice.ID, s.serverID, s.channel("polls").ID, s.appID, "poll", map[string]any{
		"question": "Pizza?", "choices": "Yes | No",
	}))
	poll := pollMessage(s, s.channel("polls").ID, "1")
	wait(t, w.ClickButton(mia.ID, poll, cid(t, "polls", "vote:1:0")))
	wait(t, w.ClickButton(alice.ID, poll, cid(t, "polls", "end:1")))
	// A warning by a moderator: moderation.case reaches the audit log.
	wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "warn", map[string]any{"member": alice.ID, "reason": "spam"}))
	w.WaitIdle()

	// Nothing is posted before the batch flush; then one compact message.
	if n := len(w.Messages(auditCh.ID)); n != 0 {
		t.Fatalf("posted before the flush: %d messages", n)
	}
	if tasks := s.tasksOf(ctx, "audit-log", time.Now().Add(time.Minute)); len(tasks) != 1 || tasks[0].Name != "flush" {
		t.Fatalf("audit-log tasks = %+v", tasks)
	}
	s.waitFor("all events queued", func() bool {
		v, err := s.kv("audit-log").Get(ctx, "seq")
		return err == nil && string(v) == "7" // 2 joins, 2 role changes (mia: Moderator), deletion, poll, warning
	})
	s.rt.sched.runTasks(ctx, time.Now().Add(11*time.Second))
	batch := s.waitMessage("batch", auditCh.ID, func(m discord.Message) bool { return len(m.Embeds) > 0 })
	text := textOf(batch)
	for _, want := range []string{
		"📥 <@" + mia.ID + "> **mia** joined",
		"📥 <@" + alice.ID + "> **alice** joined",
		"🗑️ A message by <@" + alice.ID + "> **alice** was deleted in <#" + general.ID + ">",
		"🎭 <@" + alice.ID + "> **alice** roles: + <@&" + s.role("Member").ID + ">",
		"📊 Poll #1 “Pizza?” closed: Yes (1 voter)",
		"case #1 · **warn** <@" + alice.ID + "> **alice** by <@" + mia.ID + "> — spam",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("audit batch misses %q:\n%s", want, text)
		}
	}
	if len(w.Messages(auditCh.ID)) != 1 {
		t.Errorf("one batch, %d messages", len(w.Messages(auditCh.ID)))
	}
	// The moderation plugin's own log is untouched by the audit log.
	for _, m := range w.Messages(s.channel("mod-log").ID) {
		if strings.Contains(textOf(m), "was deleted") {
			t.Errorf("audit entry in mod-log: %s", textOf(m))
		}
	}
}

// ───────────────────────────────────────────────────────────── announcements

func TestShowcaseAnnouncements(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		// An announcement due every minute, to drive the tick.
		replaceIn(t, filepath.Join(dir, "bots/cicord/bot.yaml"), "        announcements:\n          - name: game-night",
			"        announcements:\n          - name: heartbeat\n            channel: general\n            cron: \"* * * * *\"\n            content: Still here @everyone\n          - name: game-night")
	}})
	w := s.world
	ctx := context.Background()
	general := s.channel("general")
	mia := s.member("mia", "Moderator")
	alice := s.member("alice", "Member")

	// Configured announcement: the minute tick posts it once; @everyone is neutralized.
	s.rt.sched.runCron(ctx, time.Now().UTC().Add(61*time.Second))
	beat := s.waitMessage("heartbeat", general.ID, func(m discord.Message) bool { return strings.HasPrefix(m.Content, "Still here") })
	if beat.Content != "Still here @\u200beveryone" {
		t.Errorf("heartbeat = %q", beat.Content)
	}

	// /announce: moderators only; the form; scheduled in 2 h; posted by its task.
	if h := w.InvokeCommand(alice.ID, s.serverID, general.ID, s.appID, "announce", map[string]any{"channel": general.ID}); h.Err() == nil {
		t.Fatal("/announce requires ManageMessages")
	}
	h := w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "announce", map[string]any{
		"channel": general.ID, "when": "in 2h", "mention": s.role("Events").ID,
	})
	st := wait(t, h)
	if st.Modal == nil || st.Modal.CustomID != cid(t, "announcements", "compose:1") {
		t.Fatalf("/announce = %+v", st)
	}
	sub := h.SubmitModal(map[string]string{"title": "Tournament", "message": "Chess tournament on Saturday!"})
	st = wait(t, sub)
	if !st.Ephemeral || !strings.Contains(replyText(st), "Announcement #1 is scheduled for <t:") {
		t.Fatalf("modal = %+v (%s)", st, replyText(st))
	}
	if tasks := s.tasksOf(ctx, "announcements", time.Now().Add(3*time.Hour)); len(tasks) != 1 || tasks[0].Name != "post" {
		t.Fatalf("announcements tasks = %+v", tasks)
	}
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "announce-list", nil))
	if got := replyText(st); !strings.Contains(got, "**#1** <#"+general.ID+">") || !strings.Contains(got, "**game-night**") || !strings.Contains(got, "(Europe/Paris), next <t:") {
		t.Errorf("/announce-list = %s", got)
	}
	resp, def := s.route("announcements", "GET", "/scheduled", &mia, nil)
	if def.Access != "moderator" || !strings.Contains(string(resp.Body), `"title":"Tournament"`) {
		t.Errorf("GET /scheduled = %s (%s)", resp.Body, def.Access)
	}

	s.rt.sched.runTasks(ctx, time.Now().Add(2*time.Hour+time.Minute))
	posted := s.waitMessage("scheduled announcement", general.ID, func(m discord.Message) bool {
		return strings.Contains(textOf(m), "Chess tournament on Saturday!")
	})
	if posted.Content != "<@&"+s.role("Events").ID+">" || posted.Embeds[0].Title != "Tournament" {
		t.Errorf("posted = %+v", posted)
	}

	// Cancel: a second one, cancelled before it fires.
	h = w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "announce", map[string]any{"channel": general.ID, "when": "in 1d"})
	wait(t, h)
	wait(t, h.SubmitModal(map[string]string{"message": "never"}))
	st = wait(t, w.InvokeCommand(mia.ID, s.serverID, general.ID, s.appID, "announce-cancel", map[string]any{"id": 2}))
	if !strings.Contains(replyText(st), "is cancelled") {
		t.Fatalf("/announce-cancel = %s", replyText(st))
	}
	if tasks := s.tasksOf(ctx, "announcements", time.Now().Add(48*time.Hour)); len(tasks) != 0 {
		t.Errorf("the cancelled task is still scheduled: %+v", tasks)
	}
}
