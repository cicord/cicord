package discord

import "fmt"

// Server feature flags Cicord cares about (Discord's `features` strings).
const (
	FeatureCommunity            = "COMMUNITY"
	FeatureNews                 = "NEWS"
	FeatureDiscoverable         = "DISCOVERABLE"
	FeatureInvitesDisabled      = "INVITES_DISABLED"
	FeatureRaidAlertsDisabled   = "RAID_ALERTS_DISABLED"
	FeatureWelcomeScreenEnabled = "WELCOME_SCREEN_ENABLED"
	FeatureGuildOnboarding      = "GUILD_ONBOARDING"
	FeatureAnimatedIcon         = "ANIMATED_ICON"
	FeatureInviteSplash         = "INVITE_SPLASH"
	FeatureBanner               = "BANNER"
	FeatureAnimatedBanner       = "ANIMATED_BANNER"
	FeatureRoleIcons            = "ROLE_ICONS"
	FeatureEnhancedRoleColors   = "ENHANCED_ROLE_COLORS"
	FeatureVanityURL            = "VANITY_URL"
)

// MutableFeatures are the feature flags a bot can toggle with Modify Guild `features`.
var MutableFeatures = []string{FeatureCommunity, FeatureDiscoverable, FeatureInvitesDisabled, FeatureRaidAlertsDisabled}

// IsMutableFeature reports whether f can be set through ServerParams.Features.
func IsMutableFeature(f string) bool {
	for _, m := range MutableFeatures {
		if m == f {
			return true
		}
	}
	return false
}

// BoostTierFor returns the boost level (premium tier) reached with n boosts:
// 2 boosts → 1, 7 → 2, 14 → 3.
func BoostTierFor(n int) int {
	switch {
	case n >= 14:
		return 3
	case n >= 7:
		return 2
	case n >= 2:
		return 1
	}
	return 0
}

// BoostsForTier is the number of boosts needed for a boost level.
func BoostsForTier(tier int) int {
	switch tier {
	case 1:
		return 2
	case 2:
		return 7
	case 3:
		return 14
	}
	return 0
}

// FeatureTier is the boost level that unlocks a boost-gated feature (0 = not boost-gated).
var FeatureTier = map[string]int{
	FeatureAnimatedIcon:       1,
	FeatureInviteSplash:       1,
	FeatureBanner:             2,
	FeatureRoleIcons:          2,
	FeatureEnhancedRoleColors: 2,
	FeatureAnimatedBanner:     3,
	FeatureVanityURL:          3,
}

// TierFeatures returns the boost-gated features unlocked at a boost level.
func TierFeatures(tier int) []string {
	var out []string
	for _, f := range []string{FeatureAnimatedIcon, FeatureInviteSplash, FeatureBanner, FeatureRoleIcons,
		FeatureEnhancedRoleColors, FeatureAnimatedBanner, FeatureVanityURL} {
		if FeatureTier[f] <= tier {
			out = append(out, f)
		}
	}
	return out
}

// HasBoostFeature reports whether the server can use a boost-gated feature: it has the
// feature flag (partnered/verified servers get some without boosts) or its boost level
// unlocks it.
func (s Server) HasBoostFeature(f string) bool {
	if s.HasFeature(f) {
		return true
	}
	t, ok := FeatureTier[f]
	return ok && s.PremiumTier >= t
}

// BoostReason explains a boost-gated setting the server cannot use, or "" when it can:
// "banner requires boost level 2; server is level 0".
func (s Server) BoostReason(what, feature string) string {
	if s.HasBoostFeature(feature) {
		return ""
	}
	return fmt.Sprintf("%s requires boost level %d; server is level %d (%d boost(s))",
		what, FeatureTier[feature], s.PremiumTier, s.PremiumSubscriptionCount)
}

// Expression slots per boost level (static and animated emojis each have their own
// emoji slots).
var (
	emojiSlots   = [4]int{50, 100, 150, 250}
	stickerSlots = [4]int{5, 15, 30, 60}
	soundSlots   = [4]int{8, 24, 36, 48}
)

func tierIndex(t int) int { return min(max(t, 0), 3) }

// EmojiLimit is the number of static (and, separately, animated) emojis at a boost level.
func EmojiLimit(tier int) int { return emojiSlots[tierIndex(tier)] }

// StickerLimit is the number of custom stickers at a boost level.
func StickerLimit(tier int) int { return stickerSlots[tierIndex(tier)] }

// SoundboardLimit is the number of soundboard sounds at a boost level.
func SoundboardLimit(tier int) int { return soundSlots[tierIndex(tier)] }

// AutoModRuleLimit is the number of rules of a trigger type per server.
func AutoModRuleLimit(trigger string) int {
	switch trigger {
	case AutoModKeyword:
		return 6
	case AutoModSpam, AutoModKeywordPreset, AutoModMentionSpam, AutoModMemberProfile:
		return 1
	}
	return 0
}

// AutoMod validation limits (Discord API documentation, Auto Moderation).
const (
	AutoModMaxKeywords         = 1000
	AutoModMaxKeywordLength    = 60
	AutoModMaxRegex            = 10
	AutoModMaxRegexLength      = 260
	AutoModMaxAllowList        = 100  // keyword and member profile rules
	AutoModMaxPresetAllowList  = 1000 // keyword preset rules
	AutoModMaxMentionLimit     = 50
	AutoModMaxTimeoutSeconds   = 2419200 // 4 weeks
	AutoModMaxCustomMessage    = 150
	AutoModMaxExemptRoles      = 20
	AutoModMaxExemptChannels   = 50
	AutoModMaxRuleNameLength   = 100
	WelcomeScreenMaxChannels   = 5
	WelcomeScreenMaxDesc       = 140
	ForumMaxTags               = 20
	ForumMaxTagName            = 20
	OnboardingMinDefaultChans  = 7
	OnboardingMinChattable     = 5
	AFKTimeoutsDescription     = "60, 300, 900, 1800 or 3600"
	ThreadArchiveDescription   = "60, 1440, 4320 or 10080"
	MaxEmojiImageBytes         = 256 * 1024
	MaxStickerFileBytes        = 512 * 1024
	MaxSoundFileBytes          = 512 * 1024
	EmojiNameMin, EmojiNameMax = 2, 32
)

// ValidAFKTimeout reports whether seconds is one of Discord's AFK timeouts.
func ValidAFKTimeout(seconds int) bool {
	switch seconds {
	case 60, 300, 900, 1800, 3600:
		return true
	}
	return false
}

// ValidAutoArchive reports whether minutes is a valid thread auto archive duration.
func ValidAutoArchive(minutes int) bool {
	switch minutes {
	case 60, 1440, 4320, 10080:
		return true
	}
	return false
}

// AutoModAllowsAction reports whether a trigger type supports an action type.
func AutoModAllowsAction(trigger, action string) bool {
	switch action {
	case AutoModActionBlock, AutoModActionAlert:
		return trigger != AutoModMemberProfile || action == AutoModActionAlert
	case AutoModActionTimeout:
		return trigger == AutoModKeyword || trigger == AutoModMentionSpam
	case AutoModActionBlockInteraction:
		return trigger == AutoModMemberProfile
	}
	return false
}
