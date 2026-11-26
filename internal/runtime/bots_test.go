package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// The bot profile's avatar and banner (files next to bot.yaml) are uploaded once, and
// again only when a file changes.
func TestBotProfileImages(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		bot := filepath.Join(dir, "bots/cicord")
		if err := os.WriteFile(filepath.Join(bot, "avatar.png"), []byte("avatar-1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bot, "banner.png"), []byte("banner-1"), 0o644); err != nil {
			t.Fatal(err)
		}
		replaceIn(t, filepath.Join(bot, "bot.yaml"), "    username: Cicord\n",
			"    username: Cicord\n    avatar: ./avatar.png\n    banner: ./banner.png\n")
	}})
	w, botID := s.world, s.bot.ID
	s.waitFor("profile uploaded", func() bool { return w.CountCalls(botID, "UpdateSelf") == 1 })
	u, _ := w.User(botID)
	banner := w.UserBanner(botID)
	if u.AvatarURL == "" || banner == "" {
		t.Fatalf("avatar %q, banner %q", u.AvatarURL, banner)
	}

	// A new revision that does not touch the profile uploads nothing.
	newRevision := func(edit func()) {
		t.Helper()
		rev, _ := s.rt.Revision()
		edit()
		s.waitFor("new revision", func() bool { r, _ := s.rt.Revision(); return r.SHA != rev.SHA })
	}
	newRevision(func() {
		replaceIn(t, filepath.Join(s.dir, "bots/cicord/bot.yaml"), "name: the showcase", "name: the showcase again")
	})
	if n := w.CountCalls(botID, "UpdateSelf"); n != 1 {
		t.Fatalf("UpdateSelf calls after an unrelated revision = %d, want 1", n)
	}

	// A changed banner is uploaded.
	newRevision(func() {
		if err := os.WriteFile(filepath.Join(s.dir, "bots/cicord/banner.png"), []byte("banner-2"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	s.waitFor("banner updated", func() bool { return w.CountCalls(botID, "UpdateSelf") == 2 })
	if got := w.UserBanner(botID); got == banner || got == "" {
		t.Fatalf("banner = %q, was %q", got, banner)
	}
}

func TestGuildBotProfile(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		replaceIn(t, filepath.Join(dir, "bots/cicord/bot.yaml"), "servers: [showcase]", "servers:\n    - key: showcase\n      profile:\n        nickname: Concierge\n        bio: Community events")
	}})
	s.waitFor("guild member profile", func() bool {
		m, ok := s.world.Member(s.serverID, s.bot.ID)
		return ok && m.Nick == "Concierge" && m.Bio == "Community events"
	})
	if u, _ := s.world.User(s.bot.ID); u.Username == "Concierge" {
		t.Fatal("guild profile changed global identity")
	}
}
