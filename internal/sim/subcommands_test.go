package sim

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func subcommandSet() []discord.Command {
	str := func(name string, req bool) discord.CommandOption {
		return discord.CommandOption{Type: discord.OptionString, Name: name, Description: "d", Required: req}
	}
	return []discord.Command{{Name: "polls", Description: "Polls", Options: []discord.CommandOption{
		{Type: discord.OptionSubcommand, Name: "close", Description: "Close", Options: []discord.CommandOption{
			{Type: discord.OptionString, Name: "poll", Description: "p", Required: true, Autocomplete: true},
			{Type: discord.OptionInteger, Name: "n", Description: "n", Autocomplete: true},
		}},
		{Type: discord.OptionSubcommand, Name: "list", Description: "List"},
		{Type: discord.OptionSubcommandGroup, Name: "admin", Description: "Admin", Options: []discord.CommandOption{
			{Type: discord.OptionSubcommand, Name: "rename", Description: "Rename", Options: []discord.CommandOption{
				{Type: discord.OptionString, Name: "title", Description: "t", Required: true, MinLength: ptr(2), MaxLength: ptr(5)},
				str("note", false),
			}},
		}},
	}}}
}

func TestSubcommandValidation(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	sub := func(name string, opts ...discord.CommandOption) discord.CommandOption {
		return discord.CommandOption{Type: discord.OptionSubcommand, Name: name, Description: "d", Options: opts}
	}
	group := func(name string, opts ...discord.CommandOption) discord.CommandOption {
		return discord.CommandOption{Type: discord.OptionSubcommandGroup, Name: name, Description: "d", Options: opts}
	}
	plain := discord.CommandOption{Type: discord.OptionString, Name: "s", Description: "d"}
	cmd := func(opts ...discord.CommandOption) []discord.Command {
		return []discord.Command{{Name: "c", Description: "d", Options: opts}}
	}
	cases := []struct {
		name string
		cmds []discord.Command
		bad  bool
	}{
		{"full set", subcommandSet(), false},
		{"mixed with plain options", cmd(sub("a"), plain), true},
		{"group in a group", cmd(group("g", group("h", sub("a")))), true},
		{"plain option in a group", cmd(group("g", plain)), true},
		{"subcommand in a subcommand", cmd(sub("a", sub("b"))), true},
		{"empty group", cmd(group("g")), true},
		{"required subcommand", cmd(discord.CommandOption{Type: discord.OptionSubcommand, Name: "a", Description: "d", Required: true}), true},
		{"duplicate names", cmd(sub("a"), group("a", sub("b"))), true},
		{"min length on integer", cmd(discord.CommandOption{Type: discord.OptionInteger, Name: "n", Description: "d", MinLength: ptr(1)}), true},
		{"max length 0", cmd(discord.CommandOption{Type: discord.OptionString, Name: "s", Description: "d", MaxLength: ptr(0)}), true},
		{"min above max length", cmd(discord.CommandOption{Type: discord.OptionString, Name: "s", Description: "d", MinLength: ptr(3), MaxLength: ptr(2)}), true},
		{"autocomplete on boolean", cmd(discord.CommandOption{Type: discord.OptionBoolean, Name: "b", Description: "d", Autocomplete: true}), true},
		{"autocomplete with choices", cmd(discord.CommandOption{Type: discord.OptionString, Name: "s", Description: "d", Autocomplete: true,
			Choices: []discord.CommandChoice{{Name: "a", Value: "a"}}}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want error
			if tc.bad {
				want = discord.ErrBadRequest
			}
			isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, tc.cmds), want)
		})
	}
	// Stored nested and deep-copied.
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, subcommandSet()), nil)
	got := f.w.Commands(f.appID, f.srv.ID)
	if !reflect.DeepEqual(got, subcommandSet()) {
		t.Fatalf("commands = %+v", got)
	}
	got[0].Options[2].Options[0].Options[0].Name = "changed"
	if f.w.Commands(f.appID, f.srv.ID)[0].Options[2].Options[0].Options[0].Name != "title" {
		t.Error("Commands() shares nested options")
	}
}

func TestInvokeSubcommand(t *testing.T) {
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "ok"}}), nil)
	})
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, subcommandSet()), nil)
	invoke := func(name string, opts map[string]any) *InteractionHandle {
		return f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, name, opts)
	}
	for _, tc := range []struct {
		name string
		opts map[string]any
		err  error
	}{
		{"polls", nil, discord.ErrBadRequest},       // needs a subcommand
		{"polls admin", nil, discord.ErrBadRequest}, // needs a subcommand of the group
		{"polls nope", nil, discord.ErrNotFound},
		{"polls list extra", nil, discord.ErrNotFound},
		{"polls close", nil, discord.ErrBadRequest},                                       // missing required option
		{"polls admin rename", map[string]any{"title": "x"}, discord.ErrBadRequest},       // below minLength
		{"polls admin rename", map[string]any{"title": "toolong"}, discord.ErrBadRequest}, // above maxLength
		{"polls list", map[string]any{"poll": "1"}, discord.ErrBadRequest},                // option of another subcommand
	} {
		isErr(t, invoke(tc.name, tc.opts).Err(), tc.err)
	}

	h := invoke("polls admin rename", map[string]any{"title": "abc"})
	isErr(t, h.Err(), nil)
	if st := h.Wait(); !st.Acknowledged {
		t.Fatalf("state = %+v", st)
	}
	ix := h.Interaction()
	want := []discord.InteractionOption{{Name: "admin", Type: discord.OptionSubcommandGroup, Options: []discord.InteractionOption{
		{Name: "rename", Type: discord.OptionSubcommand, Options: []discord.InteractionOption{{Name: "title", Type: discord.OptionString, Value: "abc"}}},
	}}}
	if ix.Type != discord.InteractionCommand || ix.CommandName != "polls" || !reflect.DeepEqual(ix.Options, want) {
		t.Errorf("interaction = %+v", ix)
	}
	list := invoke("polls list", nil)
	isErr(t, list.Err(), nil)
	if path, opts := list.Interaction().CommandPath(); strings.Join(path, " ") != "polls list" || len(opts) != 0 {
		t.Errorf("list path = %v %v", path, opts)
	}
}

func TestAutocomplete(t *testing.T) {
	var answer func(f *fixture, ix discord.Interaction)
	f := interactionFixture(t, func(f *fixture, ix discord.Interaction) {
		if answer != nil {
			answer(f, ix)
		}
	})
	isErr(t, f.sess.SetServerCommands(ctx, f.appID, f.srv.ID, subcommandSet()), nil)
	ac := func(name, focused, partial string, opts map[string]any) *InteractionHandle {
		return f.w.Autocomplete(f.alice.ID, f.srv.ID, f.general.ID, f.appID, name, focused, partial, opts)
	}
	isErr(t, ac("polls close", "nope", "", nil).Err(), discord.ErrBadRequest)
	isErr(t, ac("polls admin rename", "title", "", nil).Err(), discord.ErrBadRequest) // not an autocomplete option
	isErr(t, ac("polls close", "poll", "", map[string]any{"x": 1}).Err(), discord.ErrBadRequest)

	answer = func(f *fixture, ix discord.Interaction) {
		focused, ok := ix.FocusedOption()
		if !ok || focused.Name != "poll" || focused.Value != "pi" {
			t.Errorf("focused = %+v", focused)
		}
		// Only an autocomplete result is accepted.
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseDeferredMessage}), discord.ErrBadRequest)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{{Name: "n", Value: 1.0}}}), discord.ErrBadRequest)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: make([]discord.CommandChoice, 26)}), discord.ErrBadRequest)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{{Name: "Pizza", Value: "1"}}}), nil)
	}
	h := ac("polls close", "poll", "pi", map[string]any{"n": 3})
	isErr(t, h.Err(), nil)
	st := h.Wait()
	if !st.Acknowledged || st.ResponseType != discord.ResponseAutocomplete || !reflect.DeepEqual(h.Choices(), []discord.CommandChoice{{Name: "Pizza", Value: "1"}}) {
		t.Fatalf("state = %+v", st)
	}
	ix := h.Interaction()
	_, opts := ix.CommandPath()
	if ix.Type != discord.InteractionAutocomplete || len(opts) != 2 || opts[1].Value != 3.0 || opts[1].Focused {
		t.Errorf("options = %+v", opts)
	}

	// Integer option: the partial text stays a string.
	answer = func(f *fixture, ix discord.Interaction) {
		if o, _ := ix.FocusedOption(); o.Value != "1." {
			t.Errorf("focused integer = %#v", o.Value)
		}
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{{Name: "s", Value: "1"}}}), discord.ErrBadRequest)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{{Name: "one", Value: 1.0}}}), nil)
	}
	h = ac("polls close", "n", "1.", nil)
	if h.Wait(); len(h.Choices()) != 1 {
		t.Errorf("choices = %+v", h.Choices())
	}

	// A command interaction does not take an autocomplete result.
	answer = func(f *fixture, ix discord.Interaction) {
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseAutocomplete}), discord.ErrBadRequest)
		expectErr(t, reply(f, ix, discord.InteractionResponse{Type: discord.ResponseMessage, Data: &discord.MessageCreate{Content: "ok"}}), nil)
	}
	f.w.InvokeCommand(f.alice.ID, f.srv.ID, f.general.ID, f.appID, "polls list", nil).Wait()
}

func TestServerJoinLeaveEvents(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	other := f.w.CreateServer("Other", f.owner.ID)
	// A second bot connected to the same world must not see the first bot's join.
	token2, bot2, _ := f.w.CreateBot("other-bot")
	rec2 := &recorder{}
	s2 := f.w.MustConnect(token2)
	isErr(t, s2.Open(ctx, discord.MustIntents("Servers", "ServerMembers"), rec2.handle), nil)
	t.Cleanup(func() { _ = s2.Close(ctx) })
	if _, err := f.w.AddBotToServer(other.ID, bot2.ID, 0); err != nil {
		t.Fatal(err)
	}
	f.w.WaitIdle()
	f.rec.reset()
	rec2.reset()

	if _, err := f.w.AddBotToServer(other.ID, f.bot.ID, 0); err != nil {
		t.Fatal(err)
	}
	f.w.WaitIdle()
	joins := eventsOf[discord.ServerJoinEvent](f.rec)
	if len(joins) != 1 || joins[0].Server.ID != other.ID || joins[0].Server.Name != "Other" {
		t.Fatalf("joins = %+v", joins)
	}
	if n := len(eventsOf[discord.ServerJoinEvent](rec2)); n != 0 {
		t.Errorf("other bot got %d joins", n)
	}

	// Kicked: the bot is no longer a member, but still gets serverLeave.
	isErr(t, f.w.AdminClient(other.ID).KickMember(ctx, other.ID, f.bot.ID, "bye"), nil)
	f.w.WaitIdle()
	leaves := eventsOf[discord.ServerLeaveEvent](f.rec)
	if len(leaves) != 1 || leaves[0].ServerID != other.ID {
		t.Fatalf("leaves = %+v", leaves)
	}
	if n := len(eventsOf[discord.ServerLeaveEvent](rec2)); n != 0 {
		t.Errorf("other bot got %d leaves", n)
	}
	// A human leaving is no server leave.
	isErr(t, f.w.RemoveMember(f.srv.ID, f.alice.ID), nil)
	f.w.WaitIdle()
	if n := len(eventsOf[discord.ServerLeaveEvent](f.rec)); n != 1 {
		t.Errorf("leaves after a member left = %d", n)
	}
}

func TestGetMessage(t *testing.T) {
	f := newFixture(t, fixtureOpts{intents: discord.MustIntents("Servers")})
	m, err := f.w.SendMessage(f.alice.ID, f.general.ID, "secret words")
	isErr(t, err, nil)
	got, err := f.sess.GetMessage(ctx, f.general.ID, m.ID)
	isErr(t, err, nil)
	if got.ID != m.ID || got.Author.ID != f.alice.ID {
		t.Errorf("message = %+v", got)
	}
	_, err = f.sess.GetMessage(ctx, f.general.ID, "123")
	isErr(t, err, discord.ErrNotFound)
	_, err = f.sess.GetMessage(ctx, "999", m.ID)
	isErr(t, err, discord.ErrNotFound)
}
