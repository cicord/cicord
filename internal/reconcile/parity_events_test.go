package reconcile

import (
	"context"
	"image/color"
	"testing"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

var parityEventIntents = discord.MustIntents("Servers", "AutoModerationConfiguration", "ServerExpressions")

// TestParityEventsSelfHeal: a manual change in Discord to a managed AutoMod rule,
// emoji, sticker or soundboard sound reaches the reconciler through the gateway and is
// reverted by selfHeal.
func TestParityEventsSelfHeal(t *testing.T) {
	desired, root := parityRepo(t, fullSpec)
	d := *desired
	on := true
	d.Spec.SyncPolicy.SelfHealP = &on
	e := newEnv(t, &d, root)
	if err := e.w.SetBoosts(e.srv.ID, 7); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	synced := make(chan Plan, 64)
	r := e.reconciler(e.session, func(o *Options) {
		o.OnSync = func(p Plan, _ Result, err error) {
			if err != nil {
				t.Errorf("selfHeal sync: %v", err)
			}
			synced <- p
		}
	})
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.session.Open(ctx, parityEventIntents, func(ev discord.Event) { r.HandleEvent(ev) }); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	e.mustEmptyPlan(r)
	drain(synced)

	ids := map[state.ResourceKind]string{}
	mapped, _ := e.store.Resources().List(ctx, "parity")
	for _, m := range mapped {
		switch m.Kind {
		case state.KindAutoMod:
			if m.Key == "bad-words" {
				ids[m.Kind] = m.ID
			}
		case state.KindEmoji, state.KindSticker, state.KindSound:
			ids[m.Kind] = m.ID
		}
	}
	if len(ids) != 4 {
		t.Fatalf("mapped = %+v", mapped)
	}
	admin := e.w.AdminClient(e.srv.ID)
	rules, _ := admin.ListAutoModRules(ctx, e.srv.ID)
	for _, rule := range rules {
		if rule.ID == ids[state.KindAutoMod] {
			rule.Name = "Hacked words"
			if _, err := admin.UpdateAutoModRule(ctx, e.srv.ID, rule.ID, rule, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := admin.UpdateEmoji(ctx, e.srv.ID, ids[state.KindEmoji], discord.EmojiParams{Name: "hacked"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.UpdateSticker(ctx, e.srv.ID, ids[state.KindSticker], discord.StickerParams{Name: "Wave", Description: "Hacked", Tags: "wave"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.UpdateSoundboardSound(ctx, e.srv.ID, ids[state.KindSound], discord.SoundParams{Name: "Horn", Volume: 1, Emoji: "📯"}, ""); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	waitFor(t, synced, func() bool {
		rules, _ := admin.ListAutoModRules(ctx, e.srv.ID)
		emojis, _ := admin.ListEmojis(ctx, e.srv.ID)
		stickers, _ := admin.ListStickers(ctx, e.srv.ID)
		sounds, _ := admin.ListSoundboardSounds(ctx, e.srv.ID)
		ok := 0
		for _, x := range rules {
			if x.ID == ids[state.KindAutoMod] && x.Name == "Bad words" {
				ok++
			}
		}
		for _, x := range emojis {
			if x.ID == ids[state.KindEmoji] && x.Name == "party" {
				ok++
			}
		}
		for _, x := range stickers {
			if x.ID == ids[state.KindSticker] && x.Description == "Waving hand" {
				ok++
			}
		}
		for _, x := range sounds {
			if x.ID == ids[state.KindSound] && x.Volume == 0.5 {
				ok++
			}
		}
		return ok == 4
	})
}

// TestParityEventsRelevance: only events about managed expressions mark drift.
func TestParityEventsRelevance(t *testing.T) {
	desired, root := parityRepo(t, fullSpec)
	e := newEnv(t, desired, root)
	if err := e.w.SetBoosts(e.srv.ID, 7); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := e.reconciler(e.session)
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.session.Open(ctx, parityEventIntents, func(ev discord.Event) { r.HandleEvent(ev) }); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	e.mustEmptyPlan(r) // clears the drift flag set by our own events
	if r.DriftPending() {
		t.Fatal("no drift expected")
	}
	// Unmanaged expressions: no drift.
	admin := e.w.AdminClient(e.srv.ID)
	if _, err := admin.CreateEmoji(ctx, e.srv.ID, discord.EmojiParams{Name: "other", Image: pngBytes(t, 32, 32, color.RGBA{9, 9, 9, 255})}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateSoundboardSound(ctx, e.srv.ID, discord.SoundParams{Name: "Other", Volume: 1, Sound: append([]byte("OggS"), make([]byte, 32)...)}, ""); err != nil {
		t.Fatal(err)
	}
	e.w.WaitIdle()
	if r.DriftPending() {
		t.Fatal("unmanaged expressions marked drift")
	}
	// A managed emoji deleted by hand: drift.
	mapped, _ := e.store.Resources().List(ctx, "parity")
	for _, m := range mapped {
		if m.Kind == state.KindEmoji {
			if err := admin.DeleteEmoji(ctx, e.srv.ID, m.ID, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.w.WaitIdle()
	if !r.DriftPending() {
		t.Fatal("deleting a managed emoji did not mark drift")
	}
}
