package disgoadapter

import (
	"context"
	"fmt"
	"net/http"

	"github.com/disgoorg/disgo/rest"

	"github.com/npjonath/codi/internal/discord"
)

// Endpoints disgo does not define.
var (
	getGuildWidget    = rest.NewEndpoint(http.MethodGet, "/guilds/{guild.id}/widget")
	updateGuildWidget = rest.NewEndpoint(http.MethodPatch, "/guilds/{guild.id}/widget")
)

// ───────────────────────────────────────────────────────────── widget

func (s *session) GetServerWidget(ctx context.Context, serverID string) (discord.ServerWidget, error) {
	if err := checkIDs("GetServerWidget", serverID); err != nil {
		return discord.ServerWidget{}, err
	}
	var w wireWidget
	if err := s.do(ctx, "GetServerWidget", getGuildWidget.Compile(nil, serverID), nil, &w); err != nil {
		return discord.ServerWidget{}, err
	}
	return widgetFromWire(w), nil
}

func (s *session) UpdateServerWidget(ctx context.Context, serverID string, w discord.ServerWidget, reason string) (discord.ServerWidget, error) {
	if err := checkIDs("UpdateServerWidget", serverID); err != nil {
		return discord.ServerWidget{}, err
	}
	if w.ChannelID != "" {
		if err := checkIDs("UpdateServerWidget", w.ChannelID); err != nil {
			return discord.ServerWidget{}, err
		}
	}
	var out wireWidget
	if err := s.do(ctx, "UpdateServerWidget", updateGuildWidget.Compile(nil, serverID), widgetBody(w), &out, reason); err != nil {
		return discord.ServerWidget{}, err
	}
	return widgetFromWire(out), nil
}

// ───────────────────────────────────────────────────────────── auto moderation

func (s *session) ListAutoModRules(ctx context.Context, serverID string) ([]discord.AutoModRule, error) {
	if err := checkIDs("ListAutoModRules", serverID); err != nil {
		return nil, err
	}
	var ws []wireAutoModRule
	if err := s.do(ctx, "ListAutoModRules", rest.GetAutoModerationRules.Compile(nil, serverID), nil, &ws); err != nil {
		return nil, err
	}
	out := make([]discord.AutoModRule, 0, len(ws))
	for _, w := range ws {
		out = append(out, autoModRuleFromWire(w))
	}
	return out, nil
}

func (s *session) CreateAutoModRule(ctx context.Context, serverID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	if err := checkIDs("CreateAutoModRule", serverID); err != nil {
		return discord.AutoModRule{}, err
	}
	body, err := autoModRuleBody(r, true)
	if err != nil {
		return discord.AutoModRule{}, fmt.Errorf("discord CreateAutoModRule: %w", err)
	}
	var w wireAutoModRule
	if err := s.do(ctx, "CreateAutoModRule", rest.CreateAutoModerationRule.Compile(nil, serverID), body, &w, reason); err != nil {
		return discord.AutoModRule{}, err
	}
	return autoModRuleFromWire(w), nil
}

func (s *session) UpdateAutoModRule(ctx context.Context, serverID, ruleID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	if err := checkIDs("UpdateAutoModRule", serverID, ruleID); err != nil {
		return discord.AutoModRule{}, err
	}
	body, err := autoModRuleBody(r, false)
	if err != nil {
		return discord.AutoModRule{}, fmt.Errorf("discord UpdateAutoModRule: %w", err)
	}
	var w wireAutoModRule
	if err := s.do(ctx, "UpdateAutoModRule", rest.UpdateAutoModerationRule.Compile(nil, serverID, ruleID), body, &w, reason); err != nil {
		return discord.AutoModRule{}, err
	}
	return autoModRuleFromWire(w), nil
}

func (s *session) DeleteAutoModRule(ctx context.Context, serverID, ruleID, reason string) error {
	if err := checkIDs("DeleteAutoModRule", serverID, ruleID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteAutoModRule", rest.DeleteAutoModerationRule.Compile(nil, serverID, ruleID), nil, nil, reason)
}

// ───────────────────────────────────────────────────────────── onboarding & welcome screen

func (s *session) GetOnboarding(ctx context.Context, serverID string) (discord.Onboarding, error) {
	if err := checkIDs("GetOnboarding", serverID); err != nil {
		return discord.Onboarding{}, err
	}
	var w wireOnboarding
	if err := s.do(ctx, "GetOnboarding", rest.GetGuildOnboarding.Compile(nil, serverID), nil, &w); err != nil {
		return discord.Onboarding{}, err
	}
	return onboardingFromWire(w), nil
}

func (s *session) UpdateOnboarding(ctx context.Context, serverID string, o discord.Onboarding, reason string) (discord.Onboarding, error) {
	if err := checkIDs("UpdateOnboarding", serverID); err != nil {
		return discord.Onboarding{}, err
	}
	if err := checkIDs("UpdateOnboarding", o.DefaultChannelIDs...); err != nil {
		return discord.Onboarding{}, err
	}
	body, err := onboardingBody(o)
	if err != nil {
		return discord.Onboarding{}, fmt.Errorf("discord UpdateOnboarding: %w", err)
	}
	var w wireOnboarding
	if err := s.do(ctx, "UpdateOnboarding", rest.UpdateGuildOnboarding.Compile(nil, serverID), body, &w, reason); err != nil {
		return discord.Onboarding{}, err
	}
	return onboardingFromWire(w), nil
}

func (s *session) GetWelcomeScreen(ctx context.Context, serverID string) (discord.WelcomeScreen, error) {
	if err := checkIDs("GetWelcomeScreen", serverID); err != nil {
		return discord.WelcomeScreen{}, err
	}
	var w wireWelcomeScreen
	if err := s.do(ctx, "GetWelcomeScreen", rest.GetGuildWelcomeScreen.Compile(nil, serverID), nil, &w); err != nil {
		return discord.WelcomeScreen{}, err
	}
	out := welcomeScreenFromWire(w)
	if w.Enabled == nil {
		// The welcome screen object has no "enabled" field: it is the
		// WELCOME_SCREEN_ENABLED server feature.
		srv, err := s.GetServer(ctx, serverID)
		if err != nil {
			return discord.WelcomeScreen{}, err
		}
		out.Enabled = srv.HasFeature(discord.FeatureWelcomeScreenEnabled)
	}
	return out, nil
}

func (s *session) UpdateWelcomeScreen(ctx context.Context, serverID string, ws discord.WelcomeScreen, reason string) (discord.WelcomeScreen, error) {
	if err := checkIDs("UpdateWelcomeScreen", serverID); err != nil {
		return discord.WelcomeScreen{}, err
	}
	for _, c := range ws.Channels {
		if err := checkIDs("UpdateWelcomeScreen", c.ChannelID); err != nil {
			return discord.WelcomeScreen{}, err
		}
	}
	var w wireWelcomeScreen
	if err := s.do(ctx, "UpdateWelcomeScreen", rest.UpdateGuildWelcomeScreen.Compile(nil, serverID), welcomeScreenBody(ws), &w, reason); err != nil {
		return discord.WelcomeScreen{}, err
	}
	out := welcomeScreenFromWire(w)
	if w.Enabled == nil {
		out.Enabled = ws.Enabled
	}
	return out, nil
}

// ───────────────────────────────────────────────────────────── emojis

func (s *session) ListEmojis(ctx context.Context, serverID string) ([]discord.Emoji, error) {
	if err := checkIDs("ListEmojis", serverID); err != nil {
		return nil, err
	}
	var ws []wireGuildEmoji
	if err := s.do(ctx, "ListEmojis", rest.GetEmojis.Compile(nil, serverID), nil, &ws); err != nil {
		return nil, err
	}
	out := make([]discord.Emoji, 0, len(ws))
	for _, w := range ws {
		out = append(out, emojiFromGuildWire(w))
	}
	return out, nil
}

func (s *session) CreateEmoji(ctx context.Context, serverID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	if err := checkIDs("CreateEmoji", serverID); err != nil {
		return discord.Emoji{}, err
	}
	if len(p.Image) == 0 {
		return discord.Emoji{}, fmt.Errorf("discord CreateEmoji: %w: image is required", discord.ErrBadRequest)
	}
	body := map[string]any{"name": p.Name, "image": imageDataURI(p.Image), "roles": []string{}}
	if p.RoleIDs != nil {
		if err := checkIDs("CreateEmoji", (*p.RoleIDs)...); err != nil {
			return discord.Emoji{}, err
		}
		body["roles"] = nonNil(*p.RoleIDs)
	}
	var w wireGuildEmoji
	if err := s.do(ctx, "CreateEmoji", rest.CreateEmoji.Compile(nil, serverID), body, &w, reason); err != nil {
		return discord.Emoji{}, err
	}
	return emojiFromGuildWire(w), nil
}

func (s *session) UpdateEmoji(ctx context.Context, serverID, emojiID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	if err := checkIDs("UpdateEmoji", serverID, emojiID); err != nil {
		return discord.Emoji{}, err
	}
	body := map[string]any{"name": p.Name}
	if p.RoleIDs != nil {
		if err := checkIDs("UpdateEmoji", (*p.RoleIDs)...); err != nil {
			return discord.Emoji{}, err
		}
		body["roles"] = nonNil(*p.RoleIDs)
	}
	var w wireGuildEmoji
	if err := s.do(ctx, "UpdateEmoji", rest.UpdateEmoji.Compile(nil, serverID, emojiID), body, &w, reason); err != nil {
		return discord.Emoji{}, err
	}
	return emojiFromGuildWire(w), nil
}

func (s *session) DeleteEmoji(ctx context.Context, serverID, emojiID, reason string) error {
	if err := checkIDs("DeleteEmoji", serverID, emojiID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteEmoji", rest.DeleteEmoji.Compile(nil, serverID, emojiID), nil, nil, reason)
}

// ───────────────────────────────────────────────────────────── stickers

func (s *session) ListStickers(ctx context.Context, serverID string) ([]discord.Sticker, error) {
	if err := checkIDs("ListStickers", serverID); err != nil {
		return nil, err
	}
	var ws []wireSticker
	if err := s.do(ctx, "ListStickers", rest.GetGuildStickers.Compile(nil, serverID), nil, &ws); err != nil {
		return nil, err
	}
	out := make([]discord.Sticker, 0, len(ws))
	for _, w := range ws {
		out = append(out, stickerFromWire(w))
	}
	return out, nil
}

func (s *session) CreateSticker(ctx context.Context, serverID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	if err := checkIDs("CreateSticker", serverID); err != nil {
		return discord.Sticker{}, err
	}
	body, err := stickerMultipart(p)
	if err != nil {
		return discord.Sticker{}, fmt.Errorf("discord CreateSticker: %w", err)
	}
	var w wireSticker
	if err := s.do(ctx, "CreateSticker", rest.CreateGuildSticker.Compile(nil, serverID), body, &w, reason); err != nil {
		return discord.Sticker{}, err
	}
	return stickerFromWire(w), nil
}

func (s *session) UpdateSticker(ctx context.Context, serverID, stickerID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	if err := checkIDs("UpdateSticker", serverID, stickerID); err != nil {
		return discord.Sticker{}, err
	}
	body := map[string]any{"name": p.Name, "description": nullIfEmpty(p.Description), "tags": p.Tags}
	var w wireSticker
	if err := s.do(ctx, "UpdateSticker", rest.UpdateGuildSticker.Compile(nil, serverID, stickerID), body, &w, reason); err != nil {
		return discord.Sticker{}, err
	}
	return stickerFromWire(w), nil
}

func (s *session) DeleteSticker(ctx context.Context, serverID, stickerID, reason string) error {
	if err := checkIDs("DeleteSticker", serverID, stickerID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteSticker", rest.DeleteGuildSticker.Compile(nil, serverID, stickerID), nil, nil, reason)
}

// ───────────────────────────────────────────────────────────── soundboard

func (s *session) ListSoundboardSounds(ctx context.Context, serverID string) ([]discord.SoundboardSound, error) {
	if err := checkIDs("ListSoundboardSounds", serverID); err != nil {
		return nil, err
	}
	var l wireSoundList
	if err := s.do(ctx, "ListSoundboardSounds", rest.GetGuildSoundboardSounds.Compile(nil, serverID), nil, &l); err != nil {
		return nil, err
	}
	out := make([]discord.SoundboardSound, 0, len(l.Items))
	for _, w := range l.Items {
		out = append(out, soundFromWire(w))
	}
	return out, nil
}

func (s *session) CreateSoundboardSound(ctx context.Context, serverID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	if err := checkIDs("CreateSoundboardSound", serverID); err != nil {
		return discord.SoundboardSound{}, err
	}
	if len(p.Sound) == 0 {
		return discord.SoundboardSound{}, fmt.Errorf("discord CreateSoundboardSound: %w: sound is required", discord.ErrBadRequest)
	}
	var w wireSound
	if err := s.do(ctx, "CreateSoundboardSound", rest.CreateGuildSoundboardSound.Compile(nil, serverID), soundBody(p, true), &w, reason); err != nil {
		return discord.SoundboardSound{}, err
	}
	return soundFromWire(w), nil
}

func (s *session) UpdateSoundboardSound(ctx context.Context, serverID, soundID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	if err := checkIDs("UpdateSoundboardSound", serverID, soundID); err != nil {
		return discord.SoundboardSound{}, err
	}
	var w wireSound
	if err := s.do(ctx, "UpdateSoundboardSound", rest.UpdateGuildSoundboardSound.Compile(nil, serverID, soundID), soundBody(p, false), &w, reason); err != nil {
		return discord.SoundboardSound{}, err
	}
	return soundFromWire(w), nil
}

func (s *session) DeleteSoundboardSound(ctx context.Context, serverID, soundID, reason string) error {
	if err := checkIDs("DeleteSoundboardSound", serverID, soundID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteSoundboardSound", rest.DeleteGuildSoundboardSound.Compile(nil, serverID, soundID), nil, nil, reason)
}
