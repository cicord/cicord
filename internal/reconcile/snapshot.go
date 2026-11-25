package reconcile

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/cicord/cicord/internal/discord"
)

// Snapshot is the live state of a server.
type Snapshot struct {
	Server   discord.Server
	Roles    []discord.Role
	Channels []discord.Channel // categories included, threads excluded
	// BotRoleIDs are the roles of the bot member; nil when no bot user was given.
	BotRoleIDs []string
	// HasBot is true when BotRoleIDs was fetched.
	HasBot bool

	// Optional parts, fetched only when requested (FetchOptions): they need Manage
	// Server or Manage Expressions, so servers that do not declare them never pay for
	// (or fail on) the extra calls. A nil pointer / false Has* flag = not fetched.
	Widget        *discord.ServerWidget
	Onboarding    *discord.Onboarding
	WelcomeScreen *discord.WelcomeScreen
	AutoMod       []discord.AutoModRule
	Emojis        []discord.Emoji
	Stickers      []discord.Sticker
	Sounds        []discord.SoundboardSound
	HasAutoMod    bool
	HasEmojis     bool
	HasStickers   bool
	HasSounds     bool
	// Members are the fetched members among FetchOptions.Members (by user ID); a listed
	// user missing from the map is not in the server. HasMembers = fetched.
	Members    map[string]discord.Member
	HasMembers bool
}

// FetchOptions selects the optional parts of a Snapshot.
type FetchOptions struct {
	Widget, Onboarding, WelcomeScreen, AutoMod, Emojis, Stickers, Sounds bool
	// Members are user IDs to fetch one by one (roles.<key>.members): only listed users
	// are read, never the whole member list.
	Members []string
}

// FetchAll fetches every optional part (cicord import).
var FetchAll = FetchOptions{Widget: true, Onboarding: true, WelcomeScreen: true, AutoMod: true, Emojis: true, Stickers: true, Sounds: true}

// Fetch reads the live state of a server. botUserID may be empty, in which case no
// role hierarchy constraint applies.
func Fetch(ctx context.Context, c discord.Client, serverID, botUserID string) (Snapshot, error) {
	return FetchWith(ctx, c, serverID, botUserID, FetchOptions{})
}

// FetchWith is Fetch with optional parts.
func FetchWith(ctx context.Context, c discord.Client, serverID, botUserID string, o FetchOptions) (Snapshot, error) {
	var s Snapshot
	var err error
	if s.Server, err = c.GetServer(ctx, serverID); err != nil {
		return s, fmt.Errorf("get server %s: %w", serverID, err)
	}
	if s.Roles, err = c.ListRoles(ctx, serverID); err != nil {
		return s, fmt.Errorf("list roles: %w", err)
	}
	chans, err := c.ListChannels(ctx, serverID)
	if err != nil {
		return s, fmt.Errorf("list channels: %w", err)
	}
	for _, ch := range chans {
		if isThread(ch.Type) {
			continue
		}
		s.Channels = append(s.Channels, ch)
	}
	if botUserID != "" {
		m, err := c.GetMember(ctx, serverID, botUserID)
		if err != nil {
			return s, fmt.Errorf("get bot member: %w", err)
		}
		s.BotRoleIDs = append([]string{}, m.RoleIDs...)
		s.HasBot = true
	}
	if o.Widget {
		w, err := c.GetServerWidget(ctx, serverID)
		if err != nil {
			return s, fmt.Errorf("get widget: %w", err)
		}
		s.Widget = &w
	}
	if o.Onboarding {
		ob, err := c.GetOnboarding(ctx, serverID)
		if err != nil && !errors.Is(err, discord.ErrNotFound) {
			return s, fmt.Errorf("get onboarding: %w", err)
		}
		s.Onboarding = &ob
	}
	if o.WelcomeScreen {
		ws, err := c.GetWelcomeScreen(ctx, serverID)
		if err != nil && !errors.Is(err, discord.ErrNotFound) {
			return s, fmt.Errorf("get welcome screen: %w", err)
		}
		s.WelcomeScreen = &ws
	}
	if o.AutoMod {
		if s.AutoMod, err = c.ListAutoModRules(ctx, serverID); err != nil {
			return s, fmt.Errorf("list automod rules: %w", err)
		}
		s.HasAutoMod = true
	}
	if o.Emojis {
		if s.Emojis, err = c.ListEmojis(ctx, serverID); err != nil {
			return s, fmt.Errorf("list emojis: %w", err)
		}
		s.HasEmojis = true
	}
	if o.Stickers {
		if s.Stickers, err = c.ListStickers(ctx, serverID); err != nil {
			return s, fmt.Errorf("list stickers: %w", err)
		}
		s.HasStickers = true
	}
	if len(o.Members) > 0 {
		s.Members = make(map[string]discord.Member, len(o.Members))
		for _, id := range o.Members {
			if _, ok := s.Members[id]; ok {
				continue
			}
			m, err := c.GetMember(ctx, serverID, id)
			if errors.Is(err, discord.ErrNotFound) {
				continue // not in the server (yet)
			}
			if err != nil {
				return s, fmt.Errorf("get member %s: %w", id, err)
			}
			s.Members[id] = m
		}
		s.HasMembers = true
	}
	if o.Sounds {
		if s.Sounds, err = c.ListSoundboardSounds(ctx, serverID); err != nil {
			return s, fmt.Errorf("list soundboard sounds: %w", err)
		}
		s.HasSounds = true
	}
	return s, nil
}

// BotTopPosition returns the position of the bot's highest role, or math.MaxInt when
// unknown (no hierarchy constraint).
func (s Snapshot) BotTopPosition() int {
	if !s.HasBot {
		return math.MaxInt
	}
	top := 0
	for _, id := range s.BotRoleIDs {
		for _, r := range s.Roles {
			if r.ID == id && r.Position > top {
				top = r.Position
			}
		}
	}
	return top
}

func isThread(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelAnnouncementThread, discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelDM:
		return true
	}
	return false
}
