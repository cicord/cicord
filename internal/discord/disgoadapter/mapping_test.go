package disgoadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func ptr[T any](v T) *T { return &v }

// jsonOf marshals v and decodes it back into a generic map/slice for assertions.
func jsonOf(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEnums(t *testing.T) {
	for i, n := range []string{"none", "low", "medium", "high", "highest"} {
		if got := verificationLevelName(i); got != n {
			t.Errorf("verificationLevelName(%d) = %q, want %q", i, got, n)
		}
		v, err := verificationLevelValue(n)
		if err != nil || v != i {
			t.Errorf("verificationLevelValue(%q) = %d, %v", n, v, err)
		}
	}
	if v, _ := notificationLevelValue("onlyMentions"); v != 1 {
		t.Errorf("onlyMentions = %d", v)
	}
	if notificationLevelName(0) != "allMessages" {
		t.Error("allMessages")
	}
	if _, err := verificationLevelValue("extreme"); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("unknown level error = %v", err)
	}
}

func TestPermissions(t *testing.T) {
	p := discord.MustPermissions("Administrator", "ManageServer", "SendPolls")
	s := permString(p)
	if s != "562949953421352" {
		t.Errorf("permString = %s", s)
	}
	if parsePerm(s) != p {
		t.Error("round trip")
	}
	if parsePerm("") != 0 || parsePerm("garbage") != 0 {
		t.Error("invalid input should be 0")
	}
}

func TestEmoji(t *testing.T) {
	cases := []struct {
		in   string
		want *wireEmoji
		back string
	}{
		{"👍", &wireEmoji{Name: "👍"}, "👍"},
		{"party:123456789012345678", &wireEmoji{Name: "party", ID: "123456789012345678"}, "party:123456789012345678"},
		{"<:party:123>", &wireEmoji{Name: "party", ID: "123"}, "party:123"},
		{"<a:dance:456>", &wireEmoji{Name: "dance", ID: "456", Animated: true}, "dance:456"},
		{"", nil, ""},
	}
	for _, c := range cases {
		got := emojiToWire(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("emojiToWire(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if back := emojiFromWire(got); back != c.back {
			t.Errorf("emojiFromWire(%q) = %q, want %q", c.in, back, c.back)
		}
	}
	if p, _ := reactionEmojiPath("👍"); p != "%F0%9F%91%8D" {
		t.Errorf("unicode path = %s", p)
	}
	if p, _ := reactionEmojiPath("<:party:123>"); p != "party:123" {
		t.Errorf("custom path = %s", p)
	}
	if _, err := reactionEmojiPath(" "); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("empty emoji err = %v", err)
	}
}

func TestImageDataURI(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	if got := imageDataURI(png); got != "data:image/png;base64,iVBORw0KGgpyZXN0" {
		t.Errorf("png = %s", got)
	}
	if got := imageDataURI([]byte("GIF89a..")); got[:20] != "data:image/gif;base6" {
		t.Errorf("gif = %s", got)
	}
	if imageDataURI(nil) != "" {
		t.Error("empty should be empty")
	}
}

func TestUserMemberRoleFromWire(t *testing.T) {
	gn := "Ada"
	av := "a_abc"
	u := userFromWire(wireUser{ID: "1", Username: "ada", GlobalName: &gn, Avatar: &av, Bot: true})
	want := discord.User{ID: "1", Username: "ada", GlobalName: "Ada", AvatarURL: "https://cdn.discordapp.com/avatars/1/a_abc.gif", Bot: true}
	if u != want {
		t.Errorf("user = %+v", u)
	}

	joined := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	until := joined.Add(time.Hour)
	nick := "nick"
	m := memberFromWire("9", wireMember{Nick: &nick, Roles: []string{"5", "6"}, JoinedAt: &joined, CommunicationDisabledUntil: &until}, &wireUser{ID: "1", Username: "ada"})
	if m.ServerID != "9" || m.User.ID != "1" || m.Nick != "nick" || !m.HasRole("6") || !m.JoinedAt.Equal(joined) || m.TimeoutUntil == nil || !m.TimeoutUntil.Equal(until) {
		t.Errorf("member = %+v", m)
	}
	// guild_id from the payload is used when no server ID is given.
	m2 := memberFromWire("", wireMember{GuildID: "7", User: &wireUser{ID: "2"}}, nil)
	if m2.ServerID != "7" || m2.User.ID != "2" || m2.TimeoutUntil != nil {
		t.Errorf("member2 = %+v", m2)
	}

	r := roleFromWire(wireRole{ID: "3", Name: "mod", Color: 0xff0000, Hoist: true, Mentionable: true, Permissions: "8", Position: 4, Managed: true})
	if r != (discord.Role{ID: "3", Name: "mod", Color: 0xff0000, Hoist: true, Mentionable: true, Permissions: 8, Position: 4, Managed: true,
		Colors: discord.RoleColors{Primary: 0xff0000}}) {
		t.Errorf("role = %+v", r)
	}
	body := jsonOf(t, roleParamsBody(discord.RoleParams{Name: ptr("x"), Color: ptr(0), Permissions: ptr(discord.Permissions(1 << 3))}))
	if !reflect.DeepEqual(body, map[string]any{"name": "x", "color": 0.0, "permissions": "8"}) {
		t.Errorf("role body = %v", body)
	}
}

func TestServerMapping(t *testing.T) {
	desc, icon, sys := "d", "hash", "11"
	s := serverFromWire(wireGuild{ID: "1", Name: "S", Description: &desc, OwnerID: "2", Icon: &icon,
		VerificationLevel: 2, DefaultMessageNotifications: 1, SystemChannelID: &sys})
	want := discord.Server{ID: "1", Name: "S", Description: "d", OwnerID: "2", IconURL: "https://cdn.discordapp.com/icons/1/hash.png",
		VerificationLevel: "medium", DefaultNotifications: "onlyMentions", SystemChannelID: "11",
		ExplicitContentFilter: "disabled", Features: []string{}}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("server = %+v", s)
	}

	body, err := serverParamsBody(discord.ServerParams{
		Name:                 ptr("New"),
		Description:          ptr(""),
		Icon:                 ptr([]byte{}),
		VerificationLevel:    ptr("highest"),
		DefaultNotifications: ptr("allMessages"),
		SystemChannelID:      ptr(""),
		RulesChannelID:       ptr("12"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := jsonOf(t, body)
	wantBody := map[string]any{
		"name": "New", "description": nil, "icon": nil, "verification_level": 4.0,
		"default_message_notifications": 0.0, "system_channel_id": nil, "rules_channel_id": "12",
	}
	if !reflect.DeepEqual(got, wantBody) {
		t.Errorf("server body = %v", got)
	}
	if b, _ := serverParamsBody(discord.ServerParams{}); len(b) != 0 {
		t.Errorf("empty params should give empty body, got %v", b)
	}
	if _, err := serverParamsBody(discord.ServerParams{DefaultNotifications: ptr("never")}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad notif err = %v", err)
	}
}

func TestChannelMapping(t *testing.T) {
	raw := `{"id":"10","guild_id":"1","name":"general","type":0,"parent_id":"5","position":3,"topic":"hi","nsfw":true,
	  "rate_limit_per_user":5,"permission_overwrites":[{"id":"1","type":0,"allow":"0","deny":"1024"},{"id":"42","type":1,"allow":"2048","deny":"0"}]}`
	var w wireChannel
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	c := channelFromWire(w)
	want := discord.Channel{ID: "10", ServerID: "1", Name: "general", Type: discord.ChannelText, ParentID: "5", Position: 3, Topic: "hi", NSFW: true,
		RateLimitPerUser: 5, Overwrites: []discord.Overwrite{
			{ID: "1", Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")},
			{ID: "42", Type: discord.OverwriteMember, Allow: discord.MustPermissions("SendMessages")},
		}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("channel = %+v", c)
	}
	// Overwrites round trip.
	for _, o := range want.Overwrites {
		if got := overwriteFromWire(overwriteToWire(o)); got != o {
			t.Errorf("overwrite round trip %+v → %+v", o, got)
		}
	}

	// Create: type + name required, "" parent omitted.
	if _, err := channelCreateBody(discord.ChannelParams{Type: discord.ChannelVoice}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("missing name err = %v", err)
	}
	cb, err := channelCreateBody(discord.ChannelParams{Name: ptr("voice"), Type: discord.ChannelVoice, ParentID: ptr(""),
		Bitrate: ptr(64000), UserLimit: ptr(5), Overwrites: &[]discord.Overwrite{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonOf(t, cb); !reflect.DeepEqual(got, map[string]any{
		"name": "voice", "type": 2.0, "bitrate": 64000.0, "user_limit": 5.0, "permission_overwrites": []any{},
	}) {
		t.Errorf("create body = %v", got)
	}

	// Update: "" parent → null; nil fields omitted; Type ignored.
	ubm, err := channelUpdateBody(discord.ChannelParams{Type: discord.ChannelVoice, ParentID: ptr(""), Topic: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	ub := jsonOf(t, ubm)
	if !reflect.DeepEqual(ub, map[string]any{"parent_id": nil, "topic": ""}) {
		t.Errorf("update body = %v", ub)
	}

	pb := jsonOf(t, channelPositionsBody([]discord.ChannelPosition{
		{ID: "1", Position: 0},
		{ID: "2", Position: 1, ParentID: ptr("")},
		{ID: "3", Position: 2, ParentID: ptr("9")},
	}))
	wantPos := []any{
		map[string]any{"id": "1", "position": 0.0},
		map[string]any{"id": "2", "position": 1.0, "parent_id": nil},
		map[string]any{"id": "3", "position": 2.0, "parent_id": "9"},
	}
	if !reflect.DeepEqual(pb, wantPos) {
		t.Errorf("positions = %v", pb)
	}
}

func TestEmbedRoundTrip(t *testing.T) {
	ts := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	e := discord.Embed{
		Title: "T", Description: "D", URL: "https://x", Color: 0x00ff00, Timestamp: &ts,
		Fields:   []discord.EmbedField{{Name: "a", Value: "b", Inline: true}},
		Author:   &discord.EmbedAuthor{Name: "au", URL: "https://a", IconURL: "https://i"},
		Footer:   &discord.EmbedFooter{Text: "f", IconURL: "https://fi"},
		ImageURL: "https://img", ThumbURL: "https://th",
	}
	w := embedToWire(e)
	b, _ := json.Marshal(w)
	var back wireEmbed
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	got := embedFromWire(back)
	if !reflect.DeepEqual(got, e) {
		t.Errorf("embed round trip:\n got %+v\nwant %+v", got, e)
	}
	m := jsonOf(t, w).(map[string]any)
	if m["type"] != "rich" || m["image"].(map[string]any)["url"] != "https://img" || m["timestamp"] != "2025-05-01T12:00:00Z" {
		t.Errorf("embed json = %v", m)
	}
}

func TestComponentsToWire(t *testing.T) {
	comps := []discord.Component{
		{Type: discord.ComponentActionRow, Components: []discord.Component{
			{Type: discord.ComponentButton, Style: discord.ButtonPrimary, CustomID: "ok", Label: "OK", Emoji: "✅"},
			{Type: discord.ComponentButton, Style: discord.ButtonLink, CustomID: "ignored", Label: "Docs", URL: "https://docs"},
		}},
		{Type: discord.ComponentActionRow, Components: []discord.Component{
			{Type: discord.ComponentStringSelect, CustomID: "pick", Placeholder: "Pick", MinValues: ptr(1), MaxValues: ptr(2),
				Options: []discord.SelectOption{{Label: "A", Value: "a", Description: "first", Emoji: "x:1", Default: true}, {Label: "B", Value: "b"}}},
		}},
	}
	got := jsonOf(t, componentsToWire(comps))
	want := []any{
		map[string]any{"type": 1.0, "components": []any{
			map[string]any{"type": 2.0, "style": 1.0, "custom_id": "ok", "label": "OK", "emoji": map[string]any{"name": "✅"}},
			map[string]any{"type": 2.0, "style": 5.0, "label": "Docs", "url": "https://docs"},
		}},
		map[string]any{"type": 1.0, "components": []any{
			map[string]any{"type": 3.0, "custom_id": "pick", "placeholder": "Pick", "min_values": 1.0, "max_values": 2.0, "options": []any{
				map[string]any{"label": "A", "value": "a", "description": "first", "emoji": map[string]any{"name": "x", "id": "1"}, "default": true},
				map[string]any{"label": "B", "value": "b"},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("components json:\n got %v\nwant %v", got, want)
	}

	// Text input: style defaults to short, required is always sent.
	ti := jsonOf(t, componentToWire(discord.Component{Type: discord.ComponentTextInput, CustomID: "why", Label: "Why?", MaxLength: ptr(100)}))
	if !reflect.DeepEqual(ti, map[string]any{"type": 4.0, "custom_id": "why", "label": "Why?", "style": 1.0, "required": false, "max_length": 100.0}) {
		t.Errorf("text input = %v", ti)
	}
}

func TestComponentsRoundTrip(t *testing.T) {
	comps := []discord.Component{
		{Type: discord.ComponentActionRow, Components: []discord.Component{
			{Type: discord.ComponentButton, Style: discord.ButtonDanger, CustomID: "del", Label: "Delete", Disabled: true, Emoji: "trash:99"},
			{Type: discord.ComponentButton, Style: discord.ButtonLink, Label: "Docs", URL: "https://docs"},
		}},
		{Type: discord.ComponentActionRow, Components: []discord.Component{
			{Type: discord.ComponentTextInput, CustomID: "t", Label: "L", TextStyle: 2, Required: true, Value: "v", MinLength: ptr(1)},
		}},
	}
	b, _ := json.Marshal(componentsToWire(comps))
	var ws []wireComponent
	if err := json.Unmarshal(b, &ws); err != nil {
		t.Fatal(err)
	}
	var got []discord.Component
	for _, w := range ws {
		got = append(got, componentFromWire(w))
	}
	if !reflect.DeepEqual(got, comps) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, comps)
	}
}

func TestMessageBodies(t *testing.T) {
	// nil allowed mentions → omitted; all-false → parse: [].
	send := jsonOf(t, messageSendToWire(discord.MessageCreate{Content: "hi", ReplyTo: "77", Flags: discord.MessageFlagEphemeral}))
	if !reflect.DeepEqual(send, map[string]any{"content": "hi", "flags": 64.0, "message_reference": map[string]any{"message_id": "77", "fail_if_not_exists": false}}) {
		t.Errorf("send = %v", send)
	}
	am := jsonOf(t, messageSendToWire(discord.MessageCreate{Content: "x", AllowedMentions: &discord.AllowedMentions{}}))
	if !reflect.DeepEqual(am.(map[string]any)["allowed_mentions"], map[string]any{"parse": []any{}}) {
		t.Errorf("allowed mentions none = %v", am)
	}
	am2 := allowedMentionsToWire(&discord.AllowedMentions{Users: true, Everyone: true})
	if !reflect.DeepEqual(am2.Parse, []string{"users", "everyone"}) {
		t.Errorf("allowed mentions = %v", am2.Parse)
	}

	// Edit: full replace, ephemeral dropped.
	edit := jsonOf(t, messageEditToWire(discord.MessageCreate{Content: "", Flags: discord.MessageFlagEphemeral}))
	if !reflect.DeepEqual(edit, map[string]any{"content": "", "embeds": []any{}, "components": []any{}}) {
		t.Errorf("edit = %v", edit)
	}
}

func TestMessageFromWire(t *testing.T) {
	raw := `{"id":"100","channel_id":"10","guild_id":"1","author":{"id":"2","username":"bob"},
	 "member":{"nick":"Bobby","roles":["5"],"joined_at":"2024-01-01T00:00:00Z"},
	 "content":"hello","embeds":[{"type":"rich","title":"T"}],
	 "components":[{"type":1,"components":[{"type":2,"style":1,"custom_id":"b","label":"B"}]}],
	 "attachments":[{"id":"9","filename":"a.png","url":"https://cdn/a.png","content_type":"image/png","size":10}],
	 "flags":0,"timestamp":"2024-02-02T00:00:00+00:00","edited_timestamp":null}`
	var w wireMessage
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	m := messageFromWire(w)
	if m.ID != "100" || m.ServerID != "1" || m.Author.Username != "bob" || m.Content != "hello" || m.EditedAt != nil {
		t.Errorf("message = %+v", m)
	}
	if m.Member == nil || m.Member.User.ID != "2" || m.Member.Nick != "Bobby" || m.Member.ServerID != "1" {
		t.Errorf("member = %+v", m.Member)
	}
	if len(m.Embeds) != 1 || m.Embeds[0].Title != "T" || len(m.Components) != 1 || m.Components[0].Components[0].CustomID != "b" {
		t.Errorf("embeds/components = %+v %+v", m.Embeds, m.Components)
	}
	if len(m.Attachments) != 1 || m.Attachments[0] != (discord.Attachment{ID: "9", Filename: "a.png", URL: "https://cdn/a.png", ContentType: "image/png", Size: 10}) {
		t.Errorf("attachments = %+v", m.Attachments)
	}
	if !m.CreatedAt.Equal(time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("created = %v", m.CreatedAt)
	}
}

// TestMessageInteraction: a bot's reply to a command says which one (the deprecated
// interaction field, still sent, has the name; interaction_metadata only the user).
func TestMessageInteraction(t *testing.T) {
	decode := func(raw string) discord.Message {
		t.Helper()
		var w wireMessage
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			t.Fatal(err)
		}
		return messageFromWire(w)
	}
	m := decode(`{"id":"1","channel_id":"10","author":{"id":"900","username":"Jockie Music","bot":true},"type":20,
	 "interaction":{"id":"5","type":2,"name":"play song","user":{"id":"6","username":"ann"}},
	 "interaction_metadata":{"id":"5","type":2,"user":{"id":"6","username":"ann"}}}`)
	if m.Interaction == nil || *m.Interaction != (discord.MessageInteraction{Type: discord.InteractionCommand, Name: "play song", UserID: "6"}) {
		t.Errorf("interaction = %+v", m.Interaction)
	}
	m = decode(`{"id":"2","channel_id":"10","author":{"id":"900","username":"Jockie Music","bot":true},
	 "interaction_metadata":{"id":"5","type":3,"user":{"id":"6"}}}`)
	if m.Interaction == nil || *m.Interaction != (discord.MessageInteraction{Type: discord.InteractionComponent, UserID: "6"}) {
		t.Errorf("metadata only = %+v", m.Interaction)
	}
	if m = decode(`{"id":"3","channel_id":"10","author":{"id":"6"}}`); m.Interaction != nil {
		t.Errorf("plain message = %+v", m.Interaction)
	}
}

func TestCommandsToWire(t *testing.T) {
	cmds := []discord.Command{{
		Name: "warn", Description: "Warn a member",
		Options: []discord.CommandOption{
			{Type: discord.OptionUser, Name: "user", Description: "who", Required: true},
			{Type: discord.OptionInteger, Name: "days", Description: "d", MinValue: ptr(1.0), MaxValue: ptr(7.0),
				Choices: []discord.CommandChoice{{Name: "one", Value: 1.0}}},
			{Type: discord.OptionString, Name: "reason", Description: "r", Choices: []discord.CommandChoice{{Name: "spam", Value: "spam"}}},
		},
		DefaultMemberPermissions: ptr(discord.MustPermissions("KickMembers")),
	}, {Name: "ping", Description: "Ping"}}
	b, _ := json.Marshal(commandsToWire(cmds))
	want := `[{"type":1,"name":"warn","description":"Warn a member","options":[` +
		`{"type":6,"name":"user","description":"who","required":true},` +
		`{"type":4,"name":"days","description":"d","choices":[{"name":"one","value":1}],"min_value":1,"max_value":7},` +
		`{"type":3,"name":"reason","description":"r","choices":[{"name":"spam","value":"spam"}]}],` +
		`"default_member_permissions":"2"},{"type":1,"name":"ping","description":"Ping"}]`
	if string(b) != want {
		t.Errorf("commands:\n got %s\nwant %s", b, want)
	}
}

func TestSubcommandsToWire(t *testing.T) {
	cmd := discord.Command{Name: "polls", Description: "Polls", Options: []discord.CommandOption{
		{Type: discord.OptionSubcommand, Name: "close", Description: "Close", Options: []discord.CommandOption{
			{Type: discord.OptionString, Name: "poll", Description: "p", Required: true, Autocomplete: true, MinLength: ptr(1), MaxLength: ptr(20)},
		}},
		{Type: discord.OptionSubcommandGroup, Name: "admin", Description: "Admin", Options: []discord.CommandOption{
			{Type: discord.OptionSubcommand, Name: "purge", Description: "Purge"},
		}},
	}}
	b, _ := json.Marshal(commandToWire(cmd))
	want := `{"type":1,"name":"polls","description":"Polls","options":[` +
		`{"type":1,"name":"close","description":"Close","options":[` +
		`{"type":3,"name":"poll","description":"p","required":true,"min_length":1,"max_length":20,"autocomplete":true}]},` +
		`{"type":2,"name":"admin","description":"Admin","options":[{"type":1,"name":"purge","description":"Purge"}]}]}`
	if string(b) != want {
		t.Errorf("command:\n got %s\nwant %s", b, want)
	}
}

func TestInteractionResponseToWire(t *testing.T) {
	cases := []struct {
		name string
		in   discord.InteractionResponse
		want string
	}{
		{"deferred ephemeral", discord.InteractionResponse{Type: discord.ResponseDeferredMessage, Data: &discord.MessageCreate{Flags: discord.MessageFlagEphemeral}}, `{"type":5,"data":{"flags":64}}`},
		{"deferred", discord.InteractionResponse{Type: discord.ResponseDeferredMessage}, `{"type":5}`},
		{"deferred update", discord.InteractionResponse{Type: discord.ResponseDeferredUpdate}, `{"type":6}`},
		{"message", discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "hi", Flags: 64}}, `{"type":4,"data":{"content":"hi","flags":64}}`},
		{"update", discord.InteractionResponse{Type: discord.ResponseUpdateMessage, Data: &discord.MessageCreate{Content: "new"}}, `{"type":7,"data":{"content":"new","embeds":[],"components":[]}}`},
		{"modal", discord.InteractionResponse{Type: discord.ResponseModal, Modal: &discord.Modal{CustomID: "m", Title: "Report", Components: []discord.Component{
			{Type: discord.ComponentActionRow, Components: []discord.Component{{Type: discord.ComponentTextInput, CustomID: "why", Label: "Why", TextStyle: 2, Required: true}}},
		}}}, `{"type":9,"data":{"custom_id":"m","title":"Report","components":[{"type":1,"components":[{"type":4,"custom_id":"why","label":"Why","style":2,"required":true}]}]}}`},
		{"autocomplete", discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{{Name: "Poll #1", Value: "1"}, {Name: "two", Value: 2.0}}},
			`{"type":8,"data":{"choices":[{"name":"Poll #1","value":"1"},{"name":"two","value":2}]}}`},
		{"autocomplete empty", discord.InteractionResponse{Type: discord.ResponseAutocomplete}, `{"type":8,"data":{"choices":[]}}`},
	}
	for _, c := range cases {
		w, err := interactionResponseToWire(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b, _ := json.Marshal(w)
		if string(b) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, b, c.want)
		}
	}
	tooMany := make([]discord.CommandChoice, 26)
	for _, bad := range []discord.InteractionResponse{{Type: discord.ResponseMessage}, {Type: discord.ResponseModal}, {Type: 3},
		{Type: discord.ResponseAutocomplete, Choices: tooMany}} {
		if _, err := interactionResponseToWire(bad); !errors.Is(err, discord.ErrBadRequest) {
			t.Errorf("bad response %+v err = %v", bad, err)
		}
	}
}

func decodeInteraction(t *testing.T, raw string) discord.Interaction {
	t.Helper()
	var w wireInteraction
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	return interactionFromWire(w)
}

func TestInteractionCommandFromWire(t *testing.T) {
	in := decodeInteraction(t, `{
	  "id":"500","application_id":"600","type":2,"token":"tok","guild_id":"1","channel_id":"10",
	  "member":{"user":{"id":"2","username":"mod"},"roles":["5"],"nick":null,"joined_at":"2024-01-01T00:00:00Z"},
	  "data":{"id":"700","name":"warn","type":1,
	    "options":[
	      {"name":"user","type":6,"value":"3"},
	      {"name":"days","type":4,"value":3},
	      {"name":"ratio","type":10,"value":0.5},
	      {"name":"silent","type":5,"value":true},
	      {"name":"where","type":7,"value":"11"},
	      {"name":"role","type":8,"value":"5"},
	      {"name":"reason","type":3,"value":"spam"}
	    ],
	    "resolved":{
	      "users":{"3":{"id":"3","username":"target","global_name":"Target"}},
	      "members":{"3":{"nick":"tgt","roles":["6"],"joined_at":"2024-01-01T00:00:00Z"}},
	      "roles":{"5":{"id":"5","name":"mod","color":1,"permissions":"8","position":2}},
	      "channels":{"11":{"id":"11","name":"logs","type":0,"permissions":"0"}}
	    }}}`)
	if in.Type != discord.InteractionCommand || in.ID != "500" || in.Token != "tok" || in.ApplicationID != "600" || in.ServerID != "1" || in.ChannelID != "10" {
		t.Errorf("base = %+v", in)
	}
	if in.User.ID != "2" || in.Member == nil || in.Member.ServerID != "1" || !in.Member.HasRole("5") {
		t.Errorf("user/member = %+v %+v", in.User, in.Member)
	}
	if in.CommandName != "warn" {
		t.Errorf("command = %q", in.CommandName)
	}
	wantOpts := []discord.InteractionOption{
		{Name: "user", Type: discord.OptionUser, Value: "3"},
		{Name: "days", Type: discord.OptionInteger, Value: 3.0},
		{Name: "ratio", Type: discord.OptionNumber, Value: 0.5},
		{Name: "silent", Type: discord.OptionBoolean, Value: true},
		{Name: "where", Type: discord.OptionChannel, Value: "11"},
		{Name: "role", Type: discord.OptionRole, Value: "5"},
		{Name: "reason", Type: discord.OptionString, Value: "spam"},
	}
	if !reflect.DeepEqual(in.Options, wantOpts) {
		t.Errorf("options:\n got %#v\nwant %#v", in.Options, wantOpts)
	}
	r := in.Resolved
	if r.Users["3"].DisplayName() != "Target" {
		t.Errorf("resolved user = %+v", r.Users)
	}
	if m := r.Members["3"]; m.User.ID != "3" || m.User.Username != "target" || m.Nick != "tgt" || m.ServerID != "1" {
		t.Errorf("resolved member = %+v", m)
	}
	if ro := r.Roles["5"]; ro.Name != "mod" || ro.Permissions != 8 || ro.Position != 2 {
		t.Errorf("resolved role = %+v", ro)
	}
	if c := r.Channels["11"]; c.Name != "logs" || c.ServerID != "1" || c.Type != discord.ChannelText {
		t.Errorf("resolved channel = %+v", c)
	}
}

func TestInteractionSubcommandNested(t *testing.T) {
	in := decodeInteraction(t, `{"id":"1","type":2,"token":"t","user":{"id":"2","username":"dm"},
	  "data":{"name":"cfg","options":[{"name":"set","type":1,"options":[{"name":"key","type":3,"value":"k"}]}]}}`)
	want := []discord.InteractionOption{{Name: "set", Type: discord.OptionSubcommand, Options: []discord.InteractionOption{{Name: "key", Type: discord.OptionString, Value: "k"}}}}
	if !reflect.DeepEqual(in.Options, want) {
		t.Errorf("options = %#v", in.Options)
	}
	if path, leaf := in.CommandPath(); !reflect.DeepEqual(path, []string{"cfg", "set"}) || len(leaf) != 1 || leaf[0].Name != "key" {
		t.Errorf("path = %v %v", path, leaf)
	}
	if in.Member != nil || in.User.ID != "2" || in.ServerID != "" {
		t.Errorf("DM interaction = %+v", in)
	}

	group := decodeInteraction(t, `{"id":"1","type":2,"token":"t","user":{"id":"2"},
	  "data":{"name":"polls","options":[{"name":"admin","type":2,"options":[{"name":"purge","type":1,"options":[]}]}]}}`)
	if path, leaf := group.CommandPath(); !reflect.DeepEqual(path, []string{"polls", "admin", "purge"}) || len(leaf) != 0 {
		t.Errorf("group path = %v %v", path, leaf)
	}
}

func TestAutocompleteFromWire(t *testing.T) {
	in := decodeInteraction(t, `{"id":"1","type":4,"token":"t","guild_id":"9","member":{"user":{"id":"2"},"roles":[]},
	  "data":{"name":"polls","options":[{"name":"close","type":1,"options":[
	    {"name":"count","type":4,"value":"1","focused":true},{"name":"note","type":3,"value":"x"}]}]}}`)
	if in.Type != discord.InteractionAutocomplete || in.CommandName != "polls" {
		t.Fatalf("interaction = %+v", in)
	}
	f, ok := in.FocusedOption()
	if !ok || f.Name != "count" || f.Value != "1" || !f.Focused {
		t.Errorf("focused = %+v %v", f, ok)
	}
	empty := decodeInteraction(t, `{"id":"1","type":4,"token":"t","data":{"name":"p","options":[{"name":"n","type":10,"value":"","focused":true}]}}`)
	if f, _ := empty.FocusedOption(); f.Value != "" {
		t.Errorf("empty focused = %#v", f.Value)
	}
}

func TestInteractionComponentFromWire(t *testing.T) {
	in := decodeInteraction(t, `{"id":"1","type":3,"token":"t","guild_id":"1","channel_id":"10",
	  "member":{"user":{"id":"2","username":"u"},"roles":[]},
	  "message":{"id":"100","channel_id":"10","author":{"id":"600","username":"bot","bot":true},"content":"pick","timestamp":"2024-01-01T00:00:00Z",
	    "components":[{"type":1,"components":[{"type":3,"custom_id":"pick","options":[{"label":"A","value":"a"}]}]}]},
	  "data":{"custom_id":"pick","component_type":3,"values":["a","b"]}}`)
	if in.Type != discord.InteractionComponent || in.CustomID != "pick" || in.ComponentType != discord.ComponentStringSelect {
		t.Errorf("component = %+v", in)
	}
	if !reflect.DeepEqual(in.Values, []string{"a", "b"}) {
		t.Errorf("values = %v", in.Values)
	}
	if in.Message == nil || in.Message.ID != "100" || !in.Message.Author.Bot || in.Message.Components[0].Components[0].Options[0].Value != "a" {
		t.Errorf("message = %+v", in.Message)
	}
	btn := decodeInteraction(t, `{"id":"1","type":3,"token":"t","data":{"custom_id":"ok","component_type":2}}`)
	if btn.Values == nil || len(btn.Values) != 0 || btn.ComponentType != discord.ComponentButton {
		t.Errorf("button values = %#v", btn.Values)
	}
	// Discord sends the clicked component's numeric id as data.id (and in the message's
	// components): a button click must still decode.
	numeric := decodeInteraction(t, `{"id":"1","type":3,"token":"t",
	  "message":{"id":"100","channel_id":"10","author":{"id":"9","username":"bot","bot":true},
	    "components":[{"type":1,"id":1,"components":[{"type":2,"id":2,"custom_id":"rr:opt-in:0","style":2,"label":"Changelog ping"}]}]},
	  "data":{"id":2,"custom_id":"rr:opt-in:0","component_type":2}}`)
	if numeric.CustomID != "rr:opt-in:0" || numeric.ComponentType != discord.ComponentButton {
		t.Errorf("button with a numeric data.id = %+v", numeric)
	}
}

func TestFlexID(t *testing.T) {
	for raw, want := range map[string]flexID{`"123456789012345678"`: "123456789012345678", `7`: "7", `null`: ""} {
		var f flexID
		if err := json.Unmarshal([]byte(raw), &f); err != nil || f != want {
			t.Errorf("flexID(%s) = %q, %v", raw, f, err)
		}
	}
	var f flexID
	if err := json.Unmarshal([]byte(`{}`), &f); err == nil {
		t.Error("flexID accepted an object")
	}
}

func TestInteractionModalFromWire(t *testing.T) {
	in := decodeInteraction(t, `{"id":"1","type":5,"token":"t","guild_id":"1","member":{"user":{"id":"2","username":"u"}},
	  "data":{"custom_id":"report","components":[
	    {"type":1,"components":[{"type":4,"custom_id":"why","value":"because"}]},
	    {"type":18,"id":3,"component":{"type":4,"custom_id":"more","value":"details"}},
	    {"type":18,"component":{"type":3,"custom_id":"cat","values":["x","y"]}}
	  ]}}`)
	if in.Type != discord.InteractionModalSubmit || in.CustomID != "report" {
		t.Errorf("modal = %+v", in)
	}
	want := map[string]string{"why": "because", "more": "details", "cat": "x,y"}
	if !reflect.DeepEqual(in.ModalFields, want) {
		t.Errorf("fields = %v", in.ModalFields)
	}
}

func TestOptionValueEdgeCases(t *testing.T) {
	if v := optionValue(4, json.RawMessage(`"12"`)); v != 12.0 {
		t.Errorf("string integer = %v", v)
	}
	if v := optionValue(3, nil); v != nil {
		t.Errorf("missing = %v", v)
	}
	if v := optionValue(9, json.RawMessage(`"123"`)); v != "123" {
		t.Errorf("mentionable = %v", v)
	}
}

func TestAllowedMentionsToWire(t *testing.T) {
	cases := []struct {
		name string
		in   discord.AllowedMentions
		want map[string]any
	}{
		{"none", discord.AllowedMentions{}, map[string]any{"parse": []any{}}},
		{"parse", discord.AllowedMentions{Users: true, Roles: true}, map[string]any{"parse": []any{"users", "roles"}}},
		{"role list", discord.AllowedMentions{RoleIDs: []string{"42"}, UserIDs: []string{}},
			map[string]any{"parse": []any{}, "roles": []any{"42"}}},
		{"users parsed, roles listed", discord.AllowedMentions{Users: true, RoleIDs: []string{"42", "43"}, RepliedUser: true},
			map[string]any{"parse": []any{"users"}, "roles": []any{"42", "43"}, "replied_user": true}},
		// A parsed type is never also listed (Discord answers 400 to that).
		{"parsed wins", discord.AllowedMentions{Roles: true, RoleIDs: []string{"42"}}, map[string]any{"parse": []any{"roles"}}},
	}
	for _, c := range cases {
		got := jsonOf(t, allowedMentionsToWire(&c.in))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	send := jsonOf(t, messageSendToWire(discord.MessageCreate{Content: "<@&42>", AllowedMentions: &discord.AllowedMentions{RoleIDs: []string{"42"}}}))
	if am := send.(map[string]any)["allowed_mentions"]; !reflect.DeepEqual(am, map[string]any{"parse": []any{}, "roles": []any{"42"}}) {
		t.Errorf("send allowed_mentions = %v", am)
	}
}

// The fields plugins rely on for permission checks (role position and mentionable, server
// owner) decode from Discord's JSON names.
func TestRoleAndServerFromJSON(t *testing.T) {
	var r wireRole
	if err := json.Unmarshal([]byte(`{"id":"3","name":"mod","position":7,"mentionable":true,"permissions":"1099511627776"}`), &r); err != nil {
		t.Fatal(err)
	}
	role := roleFromWire(r)
	if role.Position != 7 || !role.Mentionable || !role.Permissions.Has(discord.MustPermissions("ModerateMembers")) {
		t.Errorf("role = %+v", role)
	}
	var g wireGuild
	if err := json.Unmarshal([]byte(`{"id":"1","name":"S","owner_id":"99"}`), &g); err != nil {
		t.Fatal(err)
	}
	if s := serverFromWire(g); s.OwnerID != "99" {
		t.Errorf("owner = %q", s.OwnerID)
	}
}

func TestLocalizedContextMenuWire(t *testing.T) {
	b, err := json.Marshal(commandToWire(discord.Command{Type: discord.MessageMenu, Name: "Open ticket on message", CommandLabels: discord.CommandLabels{Names: map[string]string{"fr": "Ouvrir un ticket sur ce message"}}}))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"type": float64(3), "name": "Open ticket on message", "name_localizations": map[string]any{"fr": "Ouvrir un ticket sur ce message"}}; !reflect.DeepEqual(wire, want) {
		t.Fatalf("context menu wire = %s", b)
	}
}

func TestLocalizedChoiceWire(t *testing.T) {
	b, err := json.Marshal(commandOptionToWire(discord.CommandOption{Type: discord.OptionInteger, Name: "duration", Description: "d",
		Choices: []discord.CommandChoice{{Name: "1 hour", Value: float64(60), Names: map[string]string{"fr": "1 heure"}}, {Name: "1 day", Value: float64(1440)}}}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"choices":[{"name":"1 hour","name_localizations":{"fr":"1 heure"},"value":60},{"name":"1 day","value":1440}]`)) {
		t.Fatalf("choice wire = %s", b)
	}
}
