package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/state"
)

// ───────────────────────────────────────────────────────────── image / file state

// imageKVKey is the KV key of a file's upload state ("icon" keeps its historical key).
func imageKVKey(name string) string {
	if name == "icon" {
		return iconKVKey
	}
	return "image:" + name
}

// RecordFile records that a repository file is what Discord shows now, as the
// reconciler does after uploading it, so that the next plan does not upload it again
// (cicord import downloads the files of a server). kv is the reconciler KV of the server
// (scope "reconcile:<key>"); name is the logical file name ("icon", "banner",
// "splash", "role:<key>", "emoji:<key>", "sticker:<key>" or "sound:<key>"); url is the
// live CDN URL of a server or role image ("" for expressions).
func RecordFile(ctx context.Context, kv state.KV, name string, data []byte, url string) error {
	raw, _ := json.Marshal(map[string]string{"hash": hashOf(data), "url": url})
	return kv.Set(ctx, imageKVKey(name), raw, 0)
}

func (r *Reconciler) loadImageState(ctx context.Context, name string) (iconState, error) {
	if name == "icon" {
		return r.loadIconState(ctx)
	}
	if r.opts.KV == nil {
		r.emu.Lock()
		defer r.emu.Unlock()
		return r.imgMem[name], nil
	}
	raw, err := r.opts.KV.Get(ctx, imageKVKey(name))
	if errors.Is(err, state.ErrNotFound) {
		return iconState{}, nil
	}
	if err != nil {
		return iconState{}, err
	}
	var v struct {
		Hash string `json:"hash"`
		URL  string `json:"url"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return iconState{}, nil
	}
	return iconState{Hash: v.Hash, URL: v.URL}, nil
}

func (r *Reconciler) saveImageState(ctx context.Context, name string, s iconState) error {
	if name == "icon" {
		return r.saveIconState(ctx, s)
	}
	if r.opts.KV == nil {
		r.emu.Lock()
		if r.imgMem == nil {
			r.imgMem = map[string]iconState{}
		}
		r.imgMem[name] = s
		r.emu.Unlock()
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"hash": s.Hash, "url": s.URL})
	return r.opts.KV.Set(ctx, imageKVKey(name), raw, 0)
}

func (a *applier) roleIconSaved(ctx context.Context, key string, o *op, role discord.Role) error {
	if o.roleIcon == nil {
		return nil
	}
	return a.r.saveImageState(ctx, "role:"+key, iconState{Hash: o.roleIconHash, URL: role.IconURL})
}

// ───────────────────────────────────────────────────────────── apply

func (a *applier) resolveKeys(rk state.ResourceKind, keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		id, ok := a.ids[rk][k]
		if !ok {
			return nil, fmt.Errorf("unresolved %s %q", rk, k)
		}
		out = append(out, id)
	}
	return out, nil
}

func (a *applier) resolveKey(rk state.ResourceKind, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	id, ok := a.ids[rk][key]
	if !ok {
		return "", fmt.Errorf("unresolved %s %q", rk, key)
	}
	return id, nil
}

func (a *applier) runParity(ctx context.Context, c Change) error {
	o := c.op
	cl := a.r.opts.Client
	switch c.Kind {
	case KindWidget:
		id, err := a.resolveKey(state.KindChannel, o.widget.channelKey)
		if err != nil {
			return err
		}
		_, err = cl.UpdateServerWidget(ctx, a.serverID, discord.ServerWidget{Enabled: o.widget.enabled, ChannelID: id}, a.reason())
		return err

	case KindWelcomeScreen:
		ws := discord.WelcomeScreen{Enabled: o.welcome.enabled, Description: o.welcome.description, Channels: []discord.WelcomeChannel{}}
		for _, ch := range o.welcome.channels {
			id, err := a.resolveKey(state.KindChannel, ch.Channel)
			if err != nil {
				return err
			}
			ws.Channels = append(ws.Channels, discord.WelcomeChannel{ChannelID: id, Description: ch.Description, Emoji: ch.Emoji})
		}
		_, err := cl.UpdateWelcomeScreen(ctx, a.serverID, ws, a.reason())
		return err

	case KindOnboarding:
		return a.onboarding(ctx, o.onboarding)

	case KindAutoMod:
		r := o.automod.rule
		var err error
		if r.ExemptRoles, err = a.resolveKeys(state.KindRole, o.automod.exemptRoles); err != nil {
			return err
		}
		if r.ExemptChannels, err = a.resolveKeys(state.KindChannel, o.automod.exemptChannels); err != nil {
			return err
		}
		r.Actions = append([]discord.AutoModAction{}, r.Actions...)
		for i := range r.Actions {
			if r.Actions[i].Type == discord.AutoModActionAlert {
				if r.Actions[i].ChannelID, err = a.resolveKey(state.KindChannel, o.automod.alertKey); err != nil {
					return err
				}
			}
		}
		switch c.Action {
		case ActionCreate:
			rule, err := cl.CreateAutoModRule(ctx, a.serverID, r, a.reason())
			if err != nil {
				return err
			}
			return a.record(ctx, state.KindAutoMod, c.Key, rule.ID)
		case ActionAdopt:
			if err := a.record(ctx, state.KindAutoMod, c.Key, c.ID); err != nil {
				return err
			}
			if len(c.Fields) == 0 {
				return nil
			}
		}
		_, err = cl.UpdateAutoModRule(ctx, a.serverID, c.ID, r, a.reason())
		return err

	case KindEmoji, KindSticker, KindSound:
		return a.expression(ctx, c)
	}
	return fmt.Errorf("reconcile: unsupported change %s %s", c.Action, c.Kind)
}

func (a *applier) expression(ctx context.Context, c Change) error {
	o := c.op
	w := o.expr
	cl := a.r.opts.Client
	var roles *[]string
	if c.Kind == KindEmoji {
		ids, err := a.resolveKeys(state.KindRole, w.roles)
		if err != nil {
			return err
		}
		roles = &ids
	}
	saveHash := func() error {
		if w.hash == "" {
			return nil
		}
		return a.r.saveImageState(ctx, w.file, iconState{Hash: w.hash})
	}
	if c.Action == ActionCreate {
		var id string
		var err error
		switch c.Kind {
		case KindEmoji:
			var e discord.Emoji
			e, err = cl.CreateEmoji(ctx, a.serverID, discord.EmojiParams{Name: w.name, Image: w.data, RoleIDs: roles}, a.reason())
			id = e.ID
		case KindSticker:
			var s discord.Sticker
			s, err = cl.CreateSticker(ctx, a.serverID, discord.StickerParams{Name: w.name, Description: w.desc, Tags: w.tags, File: w.data}, a.reason())
			id = s.ID
		case KindSound:
			var s discord.SoundboardSound
			s, err = cl.CreateSoundboardSound(ctx, a.serverID, discord.SoundParams{Name: w.name, Sound: w.data, Volume: w.vol, Emoji: w.emoji}, a.reason())
			id = s.ID
		}
		if err != nil {
			return err
		}
		if err := a.record(ctx, o.rk, c.Key, id); err != nil {
			return err
		}
		return saveHash()
	}
	if c.Action == ActionAdopt {
		if err := a.record(ctx, o.rk, c.Key, c.ID); err != nil {
			return err
		}
		if st, _ := a.r.loadImageState(ctx, w.file); st.Hash == "" {
			if err := saveHash(); err != nil { // the adopted file is assumed to be the declared one
				return err
			}
		}
		if len(c.Fields) == 0 {
			return nil
		}
	}
	var err error
	switch c.Kind {
	case KindEmoji:
		_, err = cl.UpdateEmoji(ctx, a.serverID, c.ID, discord.EmojiParams{Name: w.name, RoleIDs: roles}, a.reason())
	case KindSticker:
		_, err = cl.UpdateSticker(ctx, a.serverID, c.ID, discord.StickerParams{Name: w.name, Description: w.desc, Tags: w.tags}, a.reason())
	case KindSound:
		_, err = cl.UpdateSoundboardSound(ctx, a.serverID, c.ID, discord.SoundParams{Name: w.name, Volume: w.vol, Emoji: w.emoji}, a.reason())
	}
	return err
}

var onboardingSeq atomic.Int64

// newSnowflake returns a fresh client-side snowflake (Discord accepts client generated
// IDs for new onboarding prompts and options, like its own client).
func newSnowflake() string {
	const epoch = 1420070400000
	ms := time.Now().UnixMilli() - epoch
	return strconv.FormatInt(ms<<22|(onboardingSeq.Add(1)&0xFFF), 10)
}

// onboarding replaces the onboarding, keeping the IDs of live prompts and options
// matched by title (so members' answers stay attached).
func (a *applier) onboarding(ctx context.Context, w *onboardingWant) error {
	cl := a.r.opts.Client
	live, err := cl.GetOnboarding(ctx, a.serverID)
	if err != nil {
		return err
	}
	sp := w.spec
	o := discord.Onboarding{Enabled: sp.Enabled, Mode: orDefault(sp.Mode, "default"), Prompts: []discord.OnboardingPrompt{}}
	if o.DefaultChannelIDs, err = a.resolveKeys(state.KindChannel, sp.DefaultChannels); err != nil {
		return err
	}
	usedP := map[string]bool{}
	for _, pr := range sp.Prompts {
		dp := discord.OnboardingPrompt{Type: promptType(pr.Type), Title: pr.Title, SingleSelect: pr.SingleSelect,
			Required: pr.Required, InOnboarding: pr.IsInOnboarding(), Options: []discord.OnboardingOption{}}
		var lp *discord.OnboardingPrompt
		for i := range live.Prompts {
			if !usedP[live.Prompts[i].ID] && live.Prompts[i].Title == pr.Title {
				lp = &live.Prompts[i]
				usedP[lp.ID] = true
				break
			}
		}
		if lp != nil {
			dp.ID = lp.ID
		}
		usedO := map[string]bool{}
		for _, op := range pr.Options {
			do := discord.OnboardingOption{Title: op.Title, Description: op.Description, Emoji: op.Emoji}
			if do.RoleIDs, err = a.resolveKeys(state.KindRole, op.Roles); err != nil {
				return err
			}
			if do.ChannelIDs, err = a.resolveKeys(state.KindChannel, op.Channels); err != nil {
				return err
			}
			if lp != nil {
				for _, lo := range lp.Options {
					if !usedO[lo.ID] && lo.Title == op.Title {
						do.ID, usedO[lo.ID] = lo.ID, true
						break
					}
				}
			}
			dp.Options = append(dp.Options, do)
		}
		o.Prompts = append(o.Prompts, dp)
	}
	_, err = cl.UpdateOnboarding(ctx, a.serverID, o, a.reason())
	return err
}
