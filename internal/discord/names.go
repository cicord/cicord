package discord

import (
	"fmt"
	"math/bits"
	"sort"
)

// Permissions is a Discord permission bitset.
type Permissions uint64

// Intents is a Discord gateway intents bitset.
type Intents uint64

// permissionBits maps Cicord permission names (https://cicord.dev/docs/configuration/bot/#gateway-intents) to Discord bits.
var permissionBits = map[string]uint{
	"CreateInstantInvite":              0,
	"KickMembers":                      1,
	"BanMembers":                       2,
	"Administrator":                    3,
	"ManageChannels":                   4,
	"ManageServer":                     5,
	"AddReactions":                     6,
	"ViewAuditLog":                     7,
	"PrioritySpeaker":                  8,
	"Stream":                           9,
	"ViewChannel":                      10,
	"SendMessages":                     11,
	"SendTTSMessages":                  12,
	"ManageMessages":                   13,
	"EmbedLinks":                       14,
	"AttachFiles":                      15,
	"ReadMessageHistory":               16,
	"MentionEveryone":                  17,
	"UseExternalEmojis":                18,
	"ViewServerInsights":               19,
	"Connect":                          20,
	"Speak":                            21,
	"MuteMembers":                      22,
	"DeafenMembers":                    23,
	"MoveMembers":                      24,
	"UseVAD":                           25,
	"ChangeNickname":                   26,
	"ManageNicknames":                  27,
	"ManageRoles":                      28,
	"ManageWebhooks":                   29,
	"ManageExpressions":                30,
	"UseApplicationCommands":           31,
	"RequestToSpeak":                   32,
	"ManageEvents":                     33,
	"ManageThreads":                    34,
	"CreatePublicThreads":              35,
	"CreatePrivateThreads":             36,
	"UseExternalStickers":              37,
	"SendMessagesInThreads":            38,
	"UseEmbeddedActivities":            39,
	"ModerateMembers":                  40,
	"ViewCreatorMonetizationAnalytics": 41,
	"UseSoundboard":                    42,
	"CreateExpressions":                43,
	"CreateEvents":                     44,
	"UseExternalSounds":                45,
	"SendVoiceMessages":                46,
	"SendPolls":                        49,
	"UseExternalApps":                  50,
	"PinMessages":                      51,
}

var intentBits = map[string]uint{
	"Servers":                     0,
	"ServerMembers":               1,
	"ServerModeration":            2,
	"ServerExpressions":           3,
	"ServerIntegrations":          4,
	"ServerWebhooks":              5,
	"ServerInvites":               6,
	"ServerVoiceStates":           7,
	"ServerPresences":             8,
	"ServerMessages":              9,
	"ServerMessageReactions":      10,
	"ServerMessageTyping":         11,
	"DirectMessages":              12,
	"DirectMessageReactions":      13,
	"DirectMessageTyping":         14,
	"MessageContent":              15,
	"ServerScheduledEvents":       16,
	"AutoModerationConfiguration": 20,
	"AutoModerationExecution":     21,
	"ServerMessagePolls":          24,
	"DirectMessagePolls":          25,
}

// PrivilegedIntents must also be enabled in the Discord developer portal.
const PrivilegedIntents = Intents(1<<1 | 1<<8 | 1<<15)

func ParsePermissions(names []string) (Permissions, error) {
	var p Permissions
	for _, n := range names {
		b, ok := permissionBits[n]
		if !ok {
			return 0, fmt.Errorf("unknown permission %q", n)
		}
		p |= 1 << b
	}
	return p, nil
}

// MustPermissions is ParsePermissions for constant names; it panics on unknown names.
func MustPermissions(names ...string) Permissions {
	p, err := ParsePermissions(names)
	if err != nil {
		panic(err)
	}
	return p
}

// Names returns the Cicord names of the set bits, sorted. Unknown bits are ignored.
func (p Permissions) Names() []string { return names(uint64(p), permissionBits) }

func (p Permissions) Has(q Permissions) bool { return p&q == q }

func ParseIntents(names []string) (Intents, error) {
	var i Intents
	for _, n := range names {
		b, ok := intentBits[n]
		if !ok {
			return 0, fmt.Errorf("unknown intent %q", n)
		}
		i |= 1 << b
	}
	return i, nil
}

func MustIntents(names ...string) Intents {
	i, err := ParseIntents(names)
	if err != nil {
		panic(err)
	}
	return i
}

func (i Intents) Names() []string { return names(uint64(i), intentBits) }

func (i Intents) Has(j Intents) bool { return i&j == j }

func names(v uint64, table map[string]uint) []string {
	out := make([]string, 0, bits.OnesCount64(v))
	for n, b := range table {
		if v&(1<<b) != 0 {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ───────────────────────────────────────────────────────────── channel types

type ChannelType int

const (
	ChannelText               ChannelType = 0
	ChannelDM                 ChannelType = 1
	ChannelVoice              ChannelType = 2
	ChannelCategory           ChannelType = 4
	ChannelAnnouncement       ChannelType = 5
	ChannelAnnouncementThread ChannelType = 10
	ChannelPublicThread       ChannelType = 11
	ChannelPrivateThread      ChannelType = 12
	ChannelStage              ChannelType = 13
	ChannelForum              ChannelType = 15
	ChannelMedia              ChannelType = 16
)

var channelTypeNames = map[ChannelType]string{
	ChannelText:               "text",
	ChannelDM:                 "dm",
	ChannelVoice:              "voice",
	ChannelCategory:           "category",
	ChannelAnnouncement:       "announcement",
	ChannelAnnouncementThread: "thread",
	ChannelPublicThread:       "thread",
	ChannelPrivateThread:      "thread",
	ChannelStage:              "stage",
	ChannelForum:              "forum",
	ChannelMedia:              "media",
}

// String returns the Cicord name of the channel type (as used in Server YAML).
func (t ChannelType) String() string {
	if n, ok := channelTypeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("unknown(%d)", int(t))
}

// IsText reports whether messages can be sent in the channel.
func (t ChannelType) IsText() bool {
	switch t {
	case ChannelText, ChannelDM, ChannelAnnouncement, ChannelAnnouncementThread,
		ChannelPublicThread, ChannelPrivateThread, ChannelVoice, ChannelStage:
		return true
	}
	return false
}

// ParseChannelType converts a Server YAML channel type to a ChannelType.
func ParseChannelType(name string) (ChannelType, error) {
	switch name {
	case "text":
		return ChannelText, nil
	case "voice":
		return ChannelVoice, nil
	case "category":
		return ChannelCategory, nil
	case "announcement":
		return ChannelAnnouncement, nil
	case "stage":
		return ChannelStage, nil
	case "forum":
		return ChannelForum, nil
	case "media":
		return ChannelMedia, nil
	}
	return 0, fmt.Errorf("unknown channel type %q", name)
}

func (t ChannelType) IsThread() bool {
	return t == ChannelPublicThread || t == ChannelPrivateThread || t == ChannelAnnouncementThread
}
