package disgoadapter

import (
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/discord"
)

func TestTranslateAutoModAndExpressions(t *testing.T) {
	s := testSession()
	rule := `{"id":"70","guild_id":"1","creator_id":"9","name":"Words","event_type":1,"trigger_type":1,
	  "trigger_metadata":{"keyword_filter":["darn"]},"actions":[{"type":1,"metadata":{}}],"enabled":true,"exempt_roles":[],"exempt_channels":[]}`
	if u, ok := one(t, mustTranslate(t, s, "AUTO_MODERATION_RULE_UPDATE", rule)).(discord.AutoModRuleUpdateEvent); !ok ||
		u.ServerID != "1" || u.Rule.ID != "70" || u.Rule.Name != "Words" || discord.ServerIDOf(u) != "1" {
		t.Errorf("automod update = %#v", u)
	}
	if _, ok := one(t, mustTranslate(t, s, "AUTO_MODERATION_RULE_CREATE", rule)).(discord.AutoModRuleCreateEvent); !ok {
		t.Error("automod create not translated")
	}
	if d, ok := one(t, mustTranslate(t, s, "AUTO_MODERATION_RULE_DELETE", rule)).(discord.AutoModRuleDeleteEvent); !ok || d.Rule.ID != "70" {
		t.Errorf("automod delete = %#v", d)
	}
	placeholder := strings.Replace(rule, `"creator_id":"9"`, `"creator_id":"`+autoModSystemUserID+`"`, 1)
	if evs := mustTranslate(t, s, "AUTO_MODERATION_RULE_UPDATE", placeholder); len(evs) != 0 {
		t.Errorf("placeholder rule emitted %v", evs)
	}

	e := one(t, mustTranslate(t, s, "GUILD_EMOJIS_UPDATE", `{"guild_id":"1","emojis":[{"id":"80","name":"wave","animated":true,"roles":["5"]}]}`)).(discord.EmojisUpdateEvent)
	if e.ServerID != "1" || len(e.Emojis) != 1 || e.Emojis[0].ID != "80" || !e.Emojis[0].Animated || discord.ServerIDOf(e) != "1" {
		t.Errorf("emojis update = %#v", e)
	}
	st := one(t, mustTranslate(t, s, "GUILD_STICKERS_UPDATE", `{"guild_id":"1","stickers":[{"id":"81","name":"Hi","tags":"wave","format_type":4}]}`)).(discord.StickersUpdateEvent)
	if st.ServerID != "1" || len(st.Stickers) != 1 || st.Stickers[0].Format != "gif" {
		t.Errorf("stickers update = %#v", st)
	}
	sound := `{"guild_id":"1","sound_id":"82","name":"Horn","volume":0.5,"emoji_name":"📯"}`
	if c, ok := one(t, mustTranslate(t, s, "GUILD_SOUNDBOARD_SOUND_CREATE", sound)).(discord.SoundCreateEvent); !ok || c.Sound.ID != "82" || c.ServerID != "1" {
		t.Errorf("sound create = %#v", c)
	}
	if u, ok := one(t, mustTranslate(t, s, "GUILD_SOUNDBOARD_SOUND_UPDATE", sound)).(discord.SoundUpdateEvent); !ok || u.Sound.Volume != 0.5 || u.Sound.Emoji != "📯" {
		t.Errorf("sound update = %#v", u)
	}
	if d, ok := one(t, mustTranslate(t, s, "GUILD_SOUNDBOARD_SOUND_DELETE", `{"guild_id":"1","sound_id":"82"}`)).(discord.SoundDeleteEvent); !ok || d.SoundID != "82" || discord.ServerIDOf(d) != "1" {
		t.Errorf("sound delete = %#v", d)
	}
	bulk := mustTranslate(t, s, "GUILD_SOUNDBOARD_SOUNDS_UPDATE", `{"guild_id":"1","soundboard_sounds":[{"sound_id":"82","name":"Horn","volume":1},{"sound_id":"83","name":"Bell","volume":1}]}`)
	if len(bulk) != 2 || bulk[1].(discord.SoundUpdateEvent).Sound.ID != "83" {
		t.Errorf("sounds update = %#v", bulk)
	}
}
