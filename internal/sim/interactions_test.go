package sim

import (
	"strings"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/discord"
)

func TestSetServerCommandsValidation(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	opt := func(name string, req bool) discord.CommandOption {
		return discord.CommandOption{Type: discord.OptionString, Name: name, Description: "d", Required: req}
	}
	many := make([]discord.CommandOption, 26)
	for i := range many {
		many[i] = opt("o"+strings.Repeat("x", i), false)
	}
	cases := []struct {
		name string
		cmds []discord.Command
		err  error
	}{
		{"ok", []discord.Command{{Name: "ping", Description: "Ping"}}, nil},
		{"unicode ok", []discord.Command{{Name: "café-1_x", Description: "d"}}, nil},
		{"empty list", nil, nil},
		{"uppercase", []discord.Command{{Name: "Ping", Description: "d"}}, discord.ErrBadRequest},
		{"space", []discord.Command{{Name: "pi ng", Description: "d"}}, discord.ErrBadRequest},
		{"too long", []discord.Command{{Name: strings.Repeat("a", 33), Description: "d"}}, discord.ErrBadRequest},
		{"32 chars", []discord.Command{{Name: strings.Repeat("a", 32), Description: "d"}}, nil},
		{"empty name", []discord.Command{{Name: "", Description: "d"}}, discord.ErrBadRequest},
		{"no description", []discord.Command{{Name: "a"}}, discord.ErrBadRequest},
		{"long description", []discord.Command{{Name: "a", Description: strings.Repeat("d", 101)}}, discord.ErrBadRequest},
		{"duplicate", []discord.Command{{Name: "a", Description: "d"}, {Name: "a", Description: "d"}}, discord.ErrBadRequest},
		{"required after optional", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{opt("x", false), opt("y", true)}}}, discord.ErrBadRequest},
		{"required before optional", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{opt("x", true), opt("y", false)}}}, nil},
		{"26 options", []discord.Command{{Name: "a", Description: "d", Options: many}}, discord.ErrBadRequest},
		{"duplicate option", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{opt("x", false), opt("x", false)}}}, discord.ErrBadRequest},
		{"bad option name", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{opt("X", false)}}}, discord.ErrBadRequest},
		{"choices on boolean", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{{Type: discord.OptionBoolean, Name: "b", Description: "d", Choices: []discord.CommandChoice{{Name: "y", Value: "y"}}}}}}, discord.ErrBadRequest},
		{"choice type mismatch", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{{Type: discord.OptionInteger, Name: "n", Description: "d", Choices: []discord.CommandChoice{{Name: "one", Value: "1"}}}}}}, discord.ErrBadRequest},
		{"min above max", []discord.Command{{Name: "a", Description: "d", Options: []discord.CommandOption{{Type: discord.OptionNumber, Name: "n", Description: "d", MinValue: ptr(5.0), MaxValue: ptr(1.0)}}}}, discord.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, tc.cmds), tc.err)
		})
	}
	// Bulk overwrite.
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, []discord.Command{{Name: "a", Description: "d"}, {Name: "b", Description: "d"}}), nil)
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, []discord.Command{{Name: "c", Description: "d"}}), nil)
	if cmds := f.w.Commands(f.appID, f.srv.ID); len(cmds) != 1 || cmds[0].Name != "c" {
		t.Fatalf("commands = %+v", cmds)
	}
	// Wrong application.
	isErr(t, f.sess.SetServerCommands(ctx, "123", f.srv.ID, nil), discord.ErrForbidden)
}

// interactionFixture registers commands and answers interactions with respond.
func interactionFixture(t *testing.T, respond func(f *fixture, ix discord.Interaction), opts ...Option) *fixture {
	t.Helper()
	f := newFixture(t, fixtureOpts{world: opts, handler: func(f *fixture, ev discord.Event) {
		if e, ok := ev.(discord.InteractionCreateEvent); ok && respond != nil {
			respond(f, e.Interaction)
		}
	}})
	cmds := []discord.Command{
		{Name: "ping", Description: "Ping"},
		{Name: "admin", Description: "Admins only", DefaultMemberPermissions: ptr(discord.MustPermissions("ManageServer"))},
		{Name: "give", Description: "Give", Options: []discord.CommandOption{
			{Type: discord.OptionUser, Name: "user", Description: "who", Required: true},
			{Type: discord.OptionInteger, Name: "amount", Description: "how much", Required: true, MinValue: ptr(1.0), MaxValue: ptr(100.0)},
			{Type: discord.OptionString, Name: "kind", Description: "kind", Choices: []discord.CommandChoice{{Name: "Gold", Value: "gold"}, {Name: "Silver", Value: "silver"}}},
			{Type: discord.OptionBoolean, Name: "silent", Description: "quiet"},
			{Type: discord.OptionRole, Name: "role", Description: "role"},
			{Type: discord.OptionChannel, Name: "where", Description: "channel"},
		}},
	}
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, cmds), nil)
	return f
}

func reply(f *fixture, ix discord.Interaction, r discord.InteractionResponse) error {
	return f.sess.RespondInteraction(ctx, ix.ID, ix.Token, r)
}

func TestInvokeCommandValidation(t *testing.T) {
	f := interactionFixture(t, nil)
	cases := []struct {
		name string
		cmd  string
		opts map[string]any
		err  error
	}{
		{"ok", "give", map[string]any{"user": f.alice.ID, "amount": 5}, nil},
		{"unknown command", "nope", nil, discord.ErrNotFound},
		{"missing required", "give", map[string]any{"user": f.alice.ID}, discord.ErrBadRequest},
		{"unknown option", "give", map[string]any{"user": f.alice.ID, "amount": 1, "x": 1}, discord.ErrBadRequest},
		{"wrong type", "give", map[string]any{"user": f.alice.ID, "amount": "5"}, discord.ErrBadRequest},
		{"non-integer", "give", map[string]any{"user": f.alice.ID, "amount": 1.5}, discord.ErrBadRequest},
		{"below min", "give", map[string]any{"user": f.alice.ID, "amount": 0}, discord.ErrBadRequest},
		{"not a choice", "give", map[string]any{"user": f.alice.ID, "amount": 1, "kind": "bronze"}, discord.ErrBadRequest},
		{"unknown user", "give", map[string]any{"user": "1", "amount": 1}, discord.ErrBadRequest},
		{"needs permission", "admin", nil, discord.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, tc.cmd, tc.opts)
			isErr(t, h.Err(), tc.err)
		})
	}
	// The owner may use the admin-only command.
	isErr(t, f.w.InvokeCommand(f.owner.ID, f.srv.ID, f.general.ID, f.appID, "admin", nil).Err(), nil)
}

func TestInteractionPayload(t *testing.T) {
	f := interactionFixture(t, nil)
	role, _ := f.w.AdminCreateRole(f.srv.ID, discord.RoleParams{Name: ptr("vip")})
	h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "give", map[string]any{
		"where": f.general, "role": role.ID, "amount": int64(7), "user": f.owner, "silent": true,
	})
	isErr(t, h.Err(), nil)
	f.w.WaitIdle()
	evs := eventsOf[discord.InteractionCreateEvent](f.rec)
	if len(evs) != 1 {
		t.Fatalf("got %d interactions", len(evs))
	}
	ix := evs[0].Interaction
	if ix.ApplicationID != f.appID || ix.Token == "" || ix.Member == nil || ix.User.ID != f.alice.ID || ix.CommandName != "give" {
		t.Fatalf("interaction = %+v", ix)
	}
	// Options follow the definition order and use contract types.
	want := []string{"user", "amount", "silent", "role", "where"}
	for i, o := range ix.Options {
		if o.Name != want[i] {
			t.Fatalf("option %d = %s, want %s", i, o.Name, want[i])
		}
	}
	if v, ok := ix.Options[1].Value.(float64); !ok || v != 7 {
		t.Fatalf("amount = %#v, want float64(7)", ix.Options[1].Value)
	}
	if ix.Options[0].Value != f.owner.ID || ix.Resolved.Users[f.owner.ID].ID != f.owner.ID || ix.Resolved.Members[f.owner.ID].User.ID != f.owner.ID {
		t.Fatal("user option not resolved")
	}
	if ix.Resolved.Roles[role.ID].Name != "vip" || ix.Resolved.Channels[f.general.ID].Name != "general" {
		t.Fatal("role/channel not resolved")
	}
	if h.Interaction().ID != ix.ID {
		t.Fatal("handle interaction ID")
	}
}

func TestInteractionsGoOnlyToTheirApplication(t *testing.T) {
	f := interactionFixture(t, nil)
	tok2, bot2, _ := f.w.CreateBot("other")
	_, _ = f.w.AddBotToServer(f.srv.ID, bot2.ID, discord.MustPermissions("Administrator"))
	rec2 := &recorder{}
	s2 := f.w.MustConnect(tok2)
	isErr(t, s2.Open(ctx, discord.MustIntents("Servers"), rec2.handle), nil)
	defer s2.Close(ctx)
	f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	f.w.WaitIdle()
	if len(eventsOf[discord.InteractionCreateEvent](rec2)) != 0 {
		t.Fatal("other application received the interaction")
	}
	if len(eventsOf[discord.InteractionCreateEvent](f.rec)) != 1 {
		t.Fatal("application did not receive the interaction")
	}
	// Interactions ignore intents: a session with no intents still gets them.
	f2 := interactionFixture(t, nil)
	_ = f2.sess.Close(ctx)
	s3 := f2.w.MustConnect(f2.token)
	rec3 := &recorder{}
	isErr(t, s3.Open(ctx, 0, rec3.handle), nil)
	defer s3.Close(ctx)
	f2.w.InvokeCommand(f2.alice.ID, f2.srv.ID, f2.general.ID, f2.appID, "ping", nil)
	f2.w.WaitIdle()
	if len(eventsOf[discord.InteractionCreateEvent](rec3)) != 1 {
		t.Fatal("interaction should be delivered regardless of intents")
	}
}

func TestInteractionLifecycle(t *testing.T) {
	type step func(f *fixture, ix discord.Interaction, t *testing.T)
	msg := func(content string, flags discord.MessageFlags) discord.InteractionResponse {
		return discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: content, Flags: flags}}
	}
	cases := []struct {
		name  string
		bot   step
		check func(t *testing.T, f *fixture, h *InteractionHandle)
	}{
		{"message response", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, msg("pong", 0)), nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			st := h.Wait()
			if !st.Acknowledged || st.ResponseType != discord.ResponseMessage || st.Message == nil || st.Message.Content != "pong" || st.Ephemeral || st.Failed {
				t.Fatalf("state = %+v", st)
			}
			if ms := f.w.Messages(f.general.ID); len(ms) != 1 || ms[0].Author.ID != f.bot.ID {
				t.Fatalf("channel messages = %+v", ms)
			}
		}},
		{"double response", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, msg("one", 0)), nil)
			expectErr(t, reply(f, ix, msg("two", 0)), discord.ErrBadRequest)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			if st := h.Wait(); st.Message.Content != "one" {
				t.Fatalf("state = %+v", st)
			}
		}},
		{"ephemeral response", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, msg("secret", discord.MessageFlagEphemeral)), nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			st := h.Wait()
			if !st.Ephemeral || st.Message == nil || st.Message.Flags&discord.MessageFlagEphemeral == 0 {
				t.Fatalf("state = %+v", st)
			}
			if len(f.w.Messages(f.general.ID)) != 0 {
				t.Fatal("ephemeral messages must not appear in the channel")
			}
			if len(eventsOf[discord.MessageCreateEvent](f.rec)) != 0 {
				t.Fatal("ephemeral messages must not produce gateway events")
			}
		}},
		{"deferred then edit", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredMessage}), nil)
			_, err := f.sess.EditOriginalResponse(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "done"})
			expectErr(t, err, nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			st, ok := h.WaitUntil(time.Second, func(s InteractionState) bool { return s.Edited })
			if !ok || !st.Deferred || st.Message.Content != "done" || st.Message.Flags&msgFlagLoading != 0 {
				t.Fatalf("state = %+v", st)
			}
		}},
		{"deferred ephemeral then followup replaces thinking message", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredMessage, Data: &discord.MessageCreate{Flags: discord.MessageFlagEphemeral}}), nil)
			_, err := f.sess.CreateFollowup(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "first"})
			expectErr(t, err, nil)
			_, err = f.sess.CreateFollowup(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "second"})
			expectErr(t, err, nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			st, ok := h.WaitUntil(time.Second, func(s InteractionState) bool { return len(s.Followups) == 2 })
			if !ok || !st.Ephemeral || st.Message.Content != "first" || st.Followups[0].ID != st.Message.ID || st.Followups[1].Content != "second" {
				t.Fatalf("state = %+v", st)
			}
			if ms := f.w.Messages(f.general.ID); len(ms) != 1 || ms[0].Content != "second" {
				t.Fatalf("channel = %+v", ms)
			}
		}},
		{"followups after message", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, msg("a", 0)), nil)
			_, err := f.sess.CreateFollowup(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "b", Flags: discord.MessageFlagEphemeral})
			expectErr(t, err, nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) {
			st, _ := h.WaitUntil(time.Second, func(s InteractionState) bool { return len(s.Followups) == 1 })
			if len(st.Followups) != 1 || st.Followups[0].Flags&discord.MessageFlagEphemeral == 0 {
				t.Fatalf("state = %+v", st)
			}
		}},
		{"edit and followup before ack", func(f *fixture, ix discord.Interaction, t *testing.T) {
			_, err := f.sess.EditOriginalResponse(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "x"})
			expectErr(t, err, discord.ErrNotFound)
			_, err = f.sess.CreateFollowup(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "x"})
			expectErr(t, err, discord.ErrNotFound)
			expectErr(t, reply(f, ix, msg("ok", 0)), nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) { h.Wait() }},
		{"update response on a command", func(f *fixture, ix discord.Interaction, t *testing.T) {
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredUpdate}), discord.ErrBadRequest)
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage}), discord.ErrBadRequest)
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseModal, Modal: &discord.Modal{CustomID: "m"}}), discord.ErrBadRequest)
			expectErr(t, reply(f, ix, msg("ok", 0)), nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) { h.Wait() }},
		{"wrong token", func(f *fixture, ix discord.Interaction, t *testing.T) {
			isErr(t, f.sess.RespondInteraction(ctx, ix.ID, "bad", msg("x", 0)), discord.ErrNotFound)
			expectErr(t, reply(f, ix, msg("ok", 0)), nil)
		}, func(t *testing.T, f *fixture, h *InteractionHandle) { h.Wait() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := interactionFixture(t, func(f *fixture, ix discord.Interaction) { tc.bot(f, ix, t) })
			h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
			isErr(t, h.Err(), nil)
			f.w.WaitIdle()
			tc.check(t, f, h)
		})
	}
}

func TestInteractionDeadline(t *testing.T) {
	f := interactionFixture(t, nil, WithInteractionDeadline(30*time.Millisecond))
	h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	isErr(t, h.Err(), nil)
	st := h.Wait()
	if !st.Failed || st.Acknowledged {
		t.Fatalf("state = %+v, want failed", st)
	}
	ix := h.Interaction()
	err := f.sess.RespondInteraction(ctx, ix.ID, ix.Token, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "late"}})
	isErr(t, err, discord.ErrNotFound)
	if len(f.w.Messages(f.general.ID)) != 0 {
		t.Fatal("late response must not post")
	}
	// A slow handler that answers after the deadline also fails.
	f2 := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		time.Sleep(60 * time.Millisecond)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "late"}}), discord.ErrNotFound)
	}, WithInteractionDeadline(30*time.Millisecond))
	h2 := f2.w.InvokeCommand(f2.alice.ID, f2.srv.ID, f2.general.ID, f2.appID, "ping", nil)
	f2.w.WaitIdle()
	if !h2.Failed() {
		t.Fatal("slow handler should fail the interaction")
	}
	// Deferring in time keeps the interaction alive.
	f3 := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredMessage}), nil)
		time.Sleep(400 * time.Millisecond)
		_, err := f.sess.EditOriginalResponse(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "slow but fine"})
		expectErr(t, err, nil)
	}, WithInteractionDeadline(250*time.Millisecond))
	h3 := f3.w.InvokeCommand(f3.alice.ID, f3.srv.ID, f3.general.ID, f3.appID, "ping", nil)
	f3.w.WaitIdle()
	if st := h3.State(); st.Failed || !st.Edited || st.Message.Content != "slow but fine" {
		t.Fatalf("state = %+v", st)
	}
}

func buttonsMsg(ids ...string) discord.MessageCreate {
	var btns []discord.Component
	for _, id := range ids {
		btns = append(btns, discord.Component{Type: discord.ComponentButton, CustomID: id, Label: id, Style: discord.ButtonPrimary})
	}
	return discord.MessageCreate{Content: "pick", Components: []discord.Component{{Type: discord.ComponentActionRow, Components: btns}}}
}

func TestButtonUpdateMessage(t *testing.T) {
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		if ix.Type != discord.InteractionComponent {
			return
		}
		switch ix.CustomID {
		case "yes":
			upd := buttonsMsg("again")
			upd.Content = "you said yes"
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseUpdateMessage, Data: &upd}), nil)
		case "again":
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredUpdate}), nil)
			_, err := f.sess.EditOriginalResponse(ctx, f.appID, ix.Token, discord.MessageCreate{Content: "final"})
			expectErr(t, err, nil)
		}
	})
	m, err := f.sess.SendMessage(ctx, f.general.ID, buttonsMsg("yes", "no"))
	isErr(t, err, nil)
	h := f.w.ClickButton(f.alice.ID, m, "yes")
	isErr(t, h.Err(), nil)
	st := h.Wait()
	if st.ResponseType != discord.ResponseUpdateMessage || st.Message.ID != m.ID || st.Message.Content != "you said yes" {
		t.Fatalf("state = %+v", st)
	}
	if h.Interaction().Message == nil || h.Interaction().Message.ID != m.ID {
		t.Fatal("component interaction should carry the message")
	}
	// The stale copy no longer has "no": the current message is used.
	isErr(t, f.w.ClickButton(f.alice.ID, m, "no").Err(), discord.ErrNotFound)
	h2 := f.w.ClickButton(f.alice.ID, m, "again")
	isErr(t, h2.Err(), nil)
	if st, ok := h2.WaitUntil(time.Second, func(s InteractionState) bool { return s.Edited }); !ok || st.Message.Content != "final" {
		t.Fatalf("state = %+v", st)
	}
	f.w.WaitIdle()
	if got, _ := f.w.Message(m.ID); got.Content != "final" {
		t.Fatalf("message = %+v", got)
	}

	// Disabled buttons and human messages cannot be clicked.
	dis := buttonsMsg("off")
	dis.Components[0].Components[0].Disabled = true
	dm, _ := f.sess.SendMessage(ctx, f.general.ID, dis)
	isErr(t, f.w.ClickButton(f.alice.ID, dm, "off").Err(), discord.ErrBadRequest)
	hm, _ := f.w.SendMessage(f.alice.ID, f.general.ID, "plain")
	isErr(t, f.w.ClickButton(f.alice.ID, hm, "x").Err(), discord.ErrBadRequest)
}

func TestSelectMenu(t *testing.T) {
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		if ix.Type == discord.InteractionComponent {
			_ = reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: strings.Join(ix.Values, ","), Flags: discord.MessageFlagEphemeral}})
		}
	})
	sel := discord.Component{Type: discord.ComponentStringSelect, CustomID: "color", MaxValues: ptr(2), Options: []discord.SelectOption{
		{Label: "Red", Value: "red"}, {Label: "Blue", Value: "blue"}, {Label: "Green", Value: "green"},
	}}
	m, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Content: "pick", Components: []discord.Component{{Type: discord.ComponentActionRow, Components: []discord.Component{sel}}}})
	isErr(t, err, nil)
	cases := []struct {
		name   string
		values []string
		err    error
	}{
		{"one", []string{"red"}, nil},
		{"two", []string{"red", "blue"}, nil},
		{"none", nil, discord.ErrBadRequest},
		{"too many", []string{"red", "blue", "green"}, discord.ErrBadRequest},
		{"unknown", []string{"pink"}, discord.ErrBadRequest},
		{"duplicate", []string{"red", "red"}, discord.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := f.w.Select(f.alice.ID, m, "color", tc.values)
			isErr(t, h.Err(), tc.err)
			if tc.err == nil {
				if st := h.Wait(); st.Message.Content != strings.Join(tc.values, ",") || !st.Ephemeral {
					t.Fatalf("state = %+v", st)
				}
			}
		})
	}
	isErr(t, f.w.ClickButton(f.alice.ID, m, "color").Err(), discord.ErrNotFound) // a select is not a button
}

func TestEphemeralComponentsOnlyForTheirUser(t *testing.T) {
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		if ix.Type == discord.InteractionCommand {
			m := buttonsMsg("ok")
			m.Flags = discord.MessageFlagEphemeral
			_ = reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &m})
		} else {
			_ = reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredUpdate})
		}
	})
	h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	st := h.Wait()
	isErr(t, f.w.ClickButton(f.owner.ID, *st.Message, "ok").Err(), discord.ErrForbidden)
	h2 := f.w.ClickButton(f.alice.ID, *st.Message, "ok")
	isErr(t, h2.Err(), nil)
	if st2 := h2.Wait(); !st2.Acknowledged || !st2.Ephemeral {
		t.Fatalf("state = %+v", st2)
	}
}

func TestModalFlow(t *testing.T) {
	modal := &discord.Modal{CustomID: "feedback", Title: "Feedback", Components: []discord.Component{
		{Type: discord.ComponentActionRow, Components: []discord.Component{{Type: discord.ComponentTextInput, CustomID: "text", Label: "Your feedback", TextStyle: 2, Required: true, MaxLength: ptr(10)}}},
		{Type: discord.ComponentActionRow, Components: []discord.Component{{Type: discord.ComponentTextInput, CustomID: "email", Label: "Email", TextStyle: 1}}},
	}}
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		switch ix.Type {
		case discord.InteractionCommand:
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseModal, Modal: modal}), nil)
		case discord.InteractionModalSubmit:
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseModal, Modal: modal}), discord.ErrBadRequest)
			expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "thanks: " + ix.ModalFields["text"]}}), nil)
		}
	})
	// Submitting a modal that was never shown fails.
	isErr(t, f.w.SubmitModal(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "feedback", nil).Err(), discord.ErrBadRequest)

	h := f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	st := h.Wait()
	if st.ResponseType != discord.ResponseModal || st.Modal == nil || st.Modal.CustomID != "feedback" || st.Message != nil {
		t.Fatalf("state = %+v", st)
	}
	isErr(t, h.SubmitModal(map[string]string{"email": "a@b"}).Err(), discord.ErrBadRequest)                              // required missing
	isErr(t, h.SubmitModal(map[string]string{"text": "far too long text"}).Err(), discord.ErrBadRequest)                 // max length
	isErr(t, h.SubmitModal(map[string]string{"text": "ok", "nope": "x"}).Err(), discord.ErrBadRequest)                   // unknown field
	isErr(t, f.w.SubmitModal(f.owner.ID, f.srv.ID, f.general.ID, f.appID, "feedback", nil).Err(), discord.ErrBadRequest) // other user
	sub := h.SubmitModal(map[string]string{"text": "great"})
	isErr(t, sub.Err(), nil)
	if st := sub.Wait(); st.Message == nil || st.Message.Content != "thanks: great" {
		t.Fatalf("state = %+v", st)
	}
	if got := sub.Interaction().ModalFields; got["text"] != "great" || got["email"] != "" {
		t.Fatalf("fields = %+v", got)
	}
	// The modal is closed after submission.
	isErr(t, h.SubmitModal(map[string]string{"text": "again"}).Err(), discord.ErrBadRequest)
}

func TestInvokeCommandUserChecks(t *testing.T) {
	f := interactionFixture(t, nil)
	stranger := f.w.CreateUser("stranger")
	isErr(t, f.w.InvokeCommand(stranger.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil).Err(), discord.ErrForbidden)
	isErr(t, f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, "1", "ping", nil).Err(), discord.ErrNotFound)
	other := f.w.CreateServer("o", f.owner.ID, WithGeneralChannel())
	og, _ := f.w.ChannelByName(other.ID, "general")
	isErr(t, f.w.InvokeCommand(f.alice.ID, f.srv.ID, og.ID, f.appID, "ping", nil).Err(), discord.ErrBadRequest)
	h := f.w.InvokeCommand(stranger.ID, f.srv.ID, f.general.ID, f.appID, "ping", nil)
	if h.ID() != "" || h.State().Acknowledged {
		t.Fatal("failed handle should be empty")
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("failed handle should be done")
	}
}
