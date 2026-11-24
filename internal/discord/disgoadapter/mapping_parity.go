package disgoadapter

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	disgodiscord "github.com/disgoorg/disgo/discord"

	"github.com/cicord/cicord/internal/discord"
)

// ───────────────────────────────────────────────────────────── widget

func widgetFromWire(w wireWidget) discord.ServerWidget {
	return discord.ServerWidget{Enabled: w.Enabled, ChannelID: deref(w.ChannelID)}
}

func widgetBody(w discord.ServerWidget) map[string]any {
	return map[string]any{"enabled": w.Enabled, "channel_id": nullIfEmpty(w.ChannelID)}
}

// ───────────────────────────────────────────────────────────── auto moderation

// Trigger types (index = Discord value; 2 was the removed HARMFUL_LINK).
var autoModTriggers = map[int]string{
	1: discord.AutoModKeyword, 3: discord.AutoModSpam, 4: discord.AutoModKeywordPreset,
	5: discord.AutoModMentionSpam, 6: discord.AutoModMemberProfile,
}

var autoModActions = map[int]string{
	1: discord.AutoModActionBlock, 2: discord.AutoModActionAlert, 3: discord.AutoModActionTimeout,
	4: discord.AutoModActionBlockInteraction,
}

var autoModPresets = map[int]string{1: "profanity", 2: "sexualContent", 3: "slurs"}

func reverse(m map[int]string, name, what string) (int, error) {
	for k, v := range m {
		if v == name {
			return k, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown %s %q", discord.ErrBadRequest, what, name)
}

func nameOf(m map[int]string, v int) string {
	if n, ok := m[v]; ok {
		return n
	}
	return fmt.Sprintf("unknown(%d)", v)
}

// autoModEventType is derived from the trigger: member profile rules run on member
// updates (2 MEMBER_UPDATE), every other trigger on messages (1 MESSAGE_SEND).
func autoModEventType(trigger string) int {
	if trigger == discord.AutoModMemberProfile {
		return 2
	}
	return 1
}

func autoModRuleFromWire(w wireAutoModRule) discord.AutoModRule {
	r := discord.AutoModRule{
		ID: w.ID, Name: w.Name, Trigger: nameOf(autoModTriggers, w.TriggerType),
		Keywords:              append([]string{}, w.TriggerMetadata.KeywordFilter...),
		Regex:                 append([]string{}, w.TriggerMetadata.RegexPatterns...),
		AllowList:             append([]string{}, w.TriggerMetadata.AllowList...),
		MentionLimit:          w.TriggerMetadata.MentionTotalLimit,
		MentionRaidProtection: w.TriggerMetadata.MentionRaidProtectionEnabled,
		Enabled:               w.Enabled,
		ExemptRoles:           append([]string{}, w.ExemptRoles...),
		ExemptChannels:        append([]string{}, w.ExemptChannels...),
		Presets:               []string{},
		Actions:               []discord.AutoModAction{},
	}
	for _, p := range w.TriggerMetadata.Presets {
		r.Presets = append(r.Presets, nameOf(autoModPresets, p))
	}
	for _, a := range w.Actions {
		act := discord.AutoModAction{Type: nameOf(autoModActions, a.Type)}
		if m := a.Metadata; m != nil {
			act.ChannelID, act.DurationSeconds, act.CustomMessage = m.ChannelID, m.DurationSeconds, m.CustomMessage
		}
		r.Actions = append(r.Actions, act)
	}
	return r
}

// autoModRuleBody builds the create (withTrigger) or modify body. Discord's Modify Auto
// Moderation Rule does not accept trigger_type (it is immutable).
func autoModRuleBody(r discord.AutoModRule, withTrigger bool) (wireAutoModRule, error) {
	w := wireAutoModRule{
		Name: r.Name, EventType: autoModEventType(r.Trigger), Enabled: r.Enabled,
		ExemptRoles: nonNil(r.ExemptRoles), ExemptChannels: nonNil(r.ExemptChannels),
		Actions: []wireAutoModAction{},
		TriggerMetadata: wireAutoModMetadata{
			KeywordFilter: r.Keywords, RegexPatterns: r.Regex, AllowList: r.AllowList,
			MentionTotalLimit: r.MentionLimit, MentionRaidProtectionEnabled: r.MentionRaidProtection,
		},
	}
	if withTrigger {
		t, err := reverse(autoModTriggers, r.Trigger, "automod trigger")
		if err != nil {
			return w, err
		}
		w.TriggerType = t
	}
	for _, p := range r.Presets {
		v, err := reverse(autoModPresets, p, "automod preset")
		if err != nil {
			return w, err
		}
		w.TriggerMetadata.Presets = append(w.TriggerMetadata.Presets, v)
	}
	for _, a := range r.Actions {
		t, err := reverse(autoModActions, a.Type, "automod action")
		if err != nil {
			return w, err
		}
		wa := wireAutoModAction{Type: t}
		switch a.Type {
		case discord.AutoModActionBlock, discord.AutoModActionBlockInteraction:
			if a.CustomMessage != "" {
				wa.Metadata = &wireAutoModActionMetadata{CustomMessage: a.CustomMessage}
			}
		case discord.AutoModActionAlert:
			if !isSnowflake(a.ChannelID) {
				return w, fmt.Errorf("%w: automod alert action needs a channel id", discord.ErrBadRequest)
			}
			wa.Metadata = &wireAutoModActionMetadata{ChannelID: a.ChannelID}
		case discord.AutoModActionTimeout:
			wa.Metadata = &wireAutoModActionMetadata{DurationSeconds: a.DurationSeconds}
		}
		w.Actions = append(w.Actions, wa)
	}
	return w, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ───────────────────────────────────────────────────────────── onboarding

var (
	onboardingModes = []string{"default", "advanced"}
	promptTypes     = []string{"multipleChoice", "dropdown"}
)

func onboardingFromWire(w wireOnboarding) discord.Onboarding {
	o := discord.Onboarding{Enabled: w.Enabled, Mode: enumName(onboardingModes, w.Mode),
		DefaultChannelIDs: append([]string{}, w.DefaultChannelIDs...), Prompts: []discord.OnboardingPrompt{}}
	for _, p := range w.Prompts {
		op := discord.OnboardingPrompt{ID: p.ID, Type: enumName(promptTypes, p.Type), Title: p.Title,
			SingleSelect: p.SingleSelect, Required: p.Required, InOnboarding: p.InOnboarding, Options: []discord.OnboardingOption{}}
		for _, opt := range p.Options {
			oo := discord.OnboardingOption{ID: opt.ID, Title: opt.Title, Description: deref(opt.Description),
				RoleIDs: append([]string{}, opt.RoleIDs...), ChannelIDs: append([]string{}, opt.ChannelIDs...)}
			switch {
			case opt.Emoji != nil && (opt.Emoji.ID != "" || opt.Emoji.Name != ""):
				oo.Emoji = emojiFromWire(opt.Emoji)
			default:
				oo.Emoji = joinEmoji(opt.EmojiID, opt.EmojiName)
			}
			op.Options = append(op.Options, oo)
		}
		o.Prompts = append(o.Prompts, op)
	}
	return o
}

// idGen hands out snowflakes for prompts and options created by an onboarding PUT.
// Discord requires an id on every prompt and option: unknown ids create new ones
// (the official client generates them the same way, from the current time).
var idGen struct {
	mu   sync.Mutex
	last uint64
}

const discordEpochMs = 1420070400000

func newSnowflake() string {
	idGen.mu.Lock()
	defer idGen.mu.Unlock()
	var id uint64
	if ms := time.Now().UnixMilli() - discordEpochMs; ms > 0 { // 0 for a clock before the Discord epoch
		id = uint64(ms) << 22
	}
	if id <= idGen.last {
		id = idGen.last + 1
	}
	idGen.last = id
	return strconv.FormatUint(id, 10)
}

func onboardingBody(o discord.Onboarding) (wireOnboarding, error) {
	mode := o.Mode
	if mode == "" {
		mode = "default"
	}
	m, err := enumValue(onboardingModes, mode, "onboarding mode")
	if err != nil {
		return wireOnboarding{}, err
	}
	w := wireOnboarding{Enabled: o.Enabled, Mode: m, DefaultChannelIDs: nonNil(o.DefaultChannelIDs), Prompts: []wireOnboardingPrompt{}}
	for _, p := range o.Prompts {
		pt := p.Type
		if pt == "" {
			pt = "multipleChoice"
		}
		t, err := enumValue(promptTypes, pt, "onboarding prompt type")
		if err != nil {
			return wireOnboarding{}, err
		}
		id := p.ID
		if id == "" {
			id = newSnowflake()
		}
		wp := wireOnboardingPrompt{ID: id, Type: t, Title: p.Title, SingleSelect: p.SingleSelect,
			Required: p.Required, InOnboarding: p.InOnboarding, Options: []wireOnboardingOption{}}
		for _, opt := range p.Options {
			oid := opt.ID
			if oid == "" {
				oid = newSnowflake()
			}
			wo := wireOnboardingOption{ID: oid, Title: opt.Title, RoleIDs: nonNil(opt.RoleIDs), ChannelIDs: nonNil(opt.ChannelIDs)}
			if opt.Description != "" {
				d := opt.Description
				wo.Description = &d
			}
			if opt.Emoji != "" {
				wo.EmojiID, wo.EmojiName = splitEmoji(opt.Emoji)
				if e := emojiToWire(opt.Emoji); e != nil && e.Animated {
					a := true
					wo.EmojiAnimated = &a
				}
			}
			wp.Options = append(wp.Options, wo)
		}
		w.Prompts = append(w.Prompts, wp)
	}
	return w, nil
}

// ───────────────────────────────────────────────────────────── welcome screen

func welcomeScreenFromWire(w wireWelcomeScreen) discord.WelcomeScreen {
	ws := discord.WelcomeScreen{Description: deref(w.Description), Channels: []discord.WelcomeChannel{}}
	if w.Enabled != nil {
		ws.Enabled = *w.Enabled
	}
	for _, c := range w.WelcomeChannels {
		ws.Channels = append(ws.Channels, discord.WelcomeChannel{ChannelID: c.ChannelID, Description: c.Description,
			Emoji: joinEmoji(c.EmojiID, c.EmojiName)})
	}
	return ws
}

func welcomeScreenBody(ws discord.WelcomeScreen) map[string]any {
	chans := make([]wireWelcomeChannel, 0, len(ws.Channels))
	for _, c := range ws.Channels {
		id, name := splitEmoji(c.Emoji)
		chans = append(chans, wireWelcomeChannel{ChannelID: c.ChannelID, Description: c.Description, EmojiID: id, EmojiName: name})
	}
	return map[string]any{"enabled": ws.Enabled, "description": nullIfEmpty(ws.Description), "welcome_channels": chans}
}

// ───────────────────────────────────────────────────────────── expressions

func emojiFromGuildWire(e wireGuildEmoji) discord.Emoji {
	return discord.Emoji{ID: e.ID, Name: e.Name, Animated: e.Animated, RoleIDs: append([]string{}, e.Roles...), Managed: e.Managed}
}

var stickerFormats = map[int]string{1: "png", 2: "apng", 3: "lottie", 4: "gif"}

func stickerFromWire(s wireSticker) discord.Sticker {
	return discord.Sticker{ID: s.ID, Name: s.Name, Description: deref(s.Description), Tags: s.Tags, Format: nameOf(stickerFormats, s.FormatType)}
}

// stickerFileType returns the file name and content type Discord expects for a
// sticker upload (PNG/APNG, GIF or a Lottie JSON).
func stickerFileType(b []byte) (string, string) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		return "sticker.png", "image/png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "sticker.gif", "image/gif"
	case len(bytes.TrimSpace(b)) > 0 && bytes.TrimSpace(b)[0] == '{':
		return "sticker.json", "application/json"
	}
	return "sticker.png", http.DetectContentType(b)
}

// stickerMultipart builds the Create Guild Sticker form (name, description, tags,
// file). Discord documents these as form fields, not a payload_json part.
func stickerMultipart(p discord.StickerParams) (*disgodiscord.MultipartBuffer, error) {
	if len(p.File) == 0 {
		return nil, fmt.Errorf("%w: sticker file is required", discord.ErrBadRequest)
	}
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	for _, f := range [][2]string{{"name", p.Name}, {"description", p.Description}, {"tags", p.Tags}} {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	name, ct := stickerFileType(p.File)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", ct)
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(p.File); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return &disgodiscord.MultipartBuffer{Buffer: buf, ContentType: mw.FormDataContentType()}, nil
}

func soundFromWire(s wireSound) discord.SoundboardSound {
	return discord.SoundboardSound{ID: s.SoundID, Name: s.Name, Volume: s.Volume, Emoji: joinEmoji(s.EmojiID, s.EmojiName)}
}

// soundDataURI encodes an mp3/ogg file as the data URI Discord expects.
func soundDataURI(b []byte) string {
	ct := "audio/mpeg"
	if bytes.HasPrefix(b, []byte("OggS")) {
		ct = "audio/ogg"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func soundBody(p discord.SoundParams, create bool) map[string]any {
	body := map[string]any{"name": p.Name, "volume": p.Volume}
	id, name := splitEmoji(p.Emoji)
	body["emoji_id"], body["emoji_name"] = ptrOrNil(id), ptrOrNil(name)
	if create {
		body["sound"] = soundDataURI(p.Sound)
	}
	return body
}

func ptrOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
