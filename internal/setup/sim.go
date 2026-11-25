package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
)

// SimEnv is the simulated Discord of `cicord dev --sim --setup`: a world with an owner, a
// community server that already has roles and channels (so the import has something to
// adopt), and helpers standing in for what a person does on discord.com (create a bot,
// toggle the privileged intents, authorize the invite, drag the bot role, send a
// message). It also implements Probe on the world.
type SimEnv struct {
	World *sim.World
	Owner discord.User

	mu      sync.Mutex
	servers []string                   // servers created by the environment
	intents map[string]discord.Intents // application ID → enabled privileged intents
	names   map[string]string          // application ID → application name
}

// NewSimEnv seeds a world for the setup wizard.
func NewSimEnv(w *sim.World) (*SimEnv, error) {
	e := &SimEnv{World: w, intents: map[string]discord.Intents{}, names: map[string]string{}}
	e.Owner = w.CreateUser("owner")
	if _, err := e.CreateServer("Pixel Pals"); err != nil {
		return nil, err
	}
	return e, nil
}

// CreateServer creates a server owned by the owner with a realistic layout.
func (e *SimEnv) CreateServer(name string) (discord.Server, error) {
	w := e.World
	srv := w.CreateServer(name, e.Owner.ID, sim.WithGeneralChannel())
	perm := discord.MustPermissions
	for _, r := range []discord.RoleParams{
		{Name: ptr("Admin"), Color: ptr(0xE74C3C), Hoist: ptr(true), Permissions: ptr(perm("Administrator"))},
		{Name: ptr("Moderator"), Color: ptr(0x3498DB), Hoist: ptr(true), Permissions: ptr(perm("KickMembers", "BanMembers", "ManageMessages", "ModerateMembers"))},
		{Name: ptr("Member"), Color: ptr(0x2ECC71)},
	} {
		if _, err := w.AdminCreateRole(srv.ID, r); err != nil {
			return srv, err
		}
	}
	cat, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Community"), Type: discord.ChannelCategory})
	if err != nil {
		return srv, err
	}
	for _, c := range []discord.ChannelParams{
		{Name: ptr("rules"), Type: discord.ChannelText, ParentID: ptr(cat.ID), Topic: ptr("Be kind.")},
		{Name: ptr("announcements"), Type: discord.ChannelText, ParentID: ptr(cat.ID)},
		{Name: ptr("Lounge"), Type: discord.ChannelVoice, ParentID: ptr(cat.ID)},
	} {
		if _, err := w.AdminCreateChannel(srv.ID, c); err != nil {
			return srv, err
		}
	}
	// A private staff category (the inspection shows it hidden from a bot without
	// Administrator) and an AutoMod rule (readable with Manage Server only).
	var mod string
	for _, r := range w.Roles(srv.ID) {
		if r.Name == "Moderator" {
			mod = r.ID
		}
	}
	private := []discord.Overwrite{
		{ID: srv.ID, Type: discord.OverwriteRole, Deny: perm("ViewChannel")},
		{ID: mod, Type: discord.OverwriteRole, Allow: perm("ViewChannel")},
	}
	staff, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("Staff"), Type: discord.ChannelCategory, Overwrites: &private})
	if err != nil {
		return srv, err
	}
	if _, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptr("mod-log"), Type: discord.ChannelText, ParentID: ptr(staff.ID), Overwrites: &private}); err != nil {
		return srv, err
	}
	for _, n := range []string{"alice", "bob"} {
		u := w.CreateUser(n)
		if err := w.AddMember(srv.ID, u.ID); err != nil {
			return srv, err
		}
	}
	// A server icon and a custom emoji: the import downloads them into the repository.
	ctx, admin := context.Background(), w.AdminClient(srv.ID)
	icon := solidPNG(128, color.RGBA{0x58, 0x65, 0xF2, 0xFF})
	if _, err := admin.UpdateServer(ctx, srv.ID, discord.ServerParams{Icon: &icon}); err != nil {
		return srv, err
	}
	if _, err := admin.CreateAutoModRule(ctx, srv.ID, discord.AutoModRule{Name: "No invites", Trigger: discord.AutoModKeyword,
		Keywords: []string{"discord.gg/*"}, Enabled: true, Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlock}}}, ""); err != nil {
		return srv, err
	}
	if _, err := admin.CreateEmoji(ctx, srv.ID, discord.EmojiParams{Name: "pixel", Image: solidPNG(32, color.RGBA{0xEB, 0x45, 0x9E, 0xFF})}, ""); err != nil {
		return srv, err
	}
	e.mu.Lock()
	e.servers = append(e.servers, srv.ID)
	e.mu.Unlock()
	return srv, nil
}

func ptr[T any](v T) *T { return &v }

// solidPNG is a square PNG of one color.
func solidPNG(size int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// CreateBot creates an application with its bot (privileged intents off, like a new
// application) and returns its token.
func (e *SimEnv) CreateBot(name string) (token string, appID string) {
	token, _, appID = e.World.CreateBot(name)
	_ = e.SetIntents(appID, 0) // a new application has every privileged intent off
	e.mu.Lock()
	e.names[appID] = name
	e.mu.Unlock()
	return token, appID
}

// SetIntents toggles the privileged intents of an application ("developer portal").
func (e *SimEnv) SetIntents(appID string, in discord.Intents) error {
	in &= discord.PrivilegedIntents
	if err := e.World.SetPrivilegedIntents(appID, in); err != nil {
		return err
	}
	e.mu.Lock()
	e.intents[appID] = in
	e.mu.Unlock()
	return nil
}

func (e *SimEnv) botUser(token string) (discord.User, string, error) {
	s, err := e.World.Connect(context.Background(), CleanToken(token))
	if err != nil {
		return discord.User{}, "", err
	}
	defer s.Close(context.Background())
	return s.Self(), s.ApplicationID(), nil
}

// Invite adds the bot to a server with Administrator, like the OAuth invite. low places
// the bot's role at the bottom of the hierarchy (to rehearse the "drag the role" fix).
func (e *SimEnv) Invite(token, serverID string, low bool) error {
	return e.InviteWith(token, serverID, discord.MustPermissions("Administrator"), low)
}

// InviteWith is Invite with the permissions of the invite link (View Channels for the
// inspection invite).
func (e *SimEnv) InviteWith(token, serverID string, perms discord.Permissions, low bool) error {
	u, _, err := e.botUser(token)
	if err != nil {
		return err
	}
	role, err := e.World.AddBotToServer(serverID, u.ID, perms)
	if err != nil {
		return err
	}
	if low && role.ID != "" {
		return e.World.AdminSetRolePositions(serverID, []discord.RolePosition{{ID: role.ID, Position: 1}})
	}
	return nil
}

// SetBotPermissions sets the permissions of the bot's managed role, like authorizing an
// invite link again with other permissions.
func (e *SimEnv) SetBotPermissions(token, serverID string, perms discord.Permissions) error {
	u, _, err := e.botUser(token)
	if err != nil {
		return err
	}
	m, ok := e.World.Member(serverID, u.ID)
	if !ok {
		return errors.New("the bot is not in this server")
	}
	for _, r := range e.World.Roles(serverID) {
		if r.Managed && m.HasRole(r.ID) {
			_, err := e.World.AdminUpdateRole(serverID, r.ID, discord.RoleParams{Permissions: &perms})
			return err
		}
	}
	return errors.New("the bot has no role in this server")
}

// RaiseBotRole moves the bot's managed role to the top ("drag it above every role").
func (e *SimEnv) RaiseBotRole(token, serverID string) error {
	u, _, err := e.botUser(token)
	if err != nil {
		return err
	}
	m, ok := e.World.Member(serverID, u.ID)
	if !ok {
		return errors.New("the bot is not in this server")
	}
	roles := e.World.Roles(serverID)
	top := 0
	for _, r := range roles {
		if r.Position > top {
			top = r.Position
		}
	}
	for _, r := range roles {
		if r.Managed && m.HasRole(r.ID) {
			return e.World.AdminSetRolePositions(serverID, []discord.RolePosition{{ID: r.ID, Position: top}})
		}
	}
	return errors.New("the bot has no role in this server")
}

// SendMessage posts a message as the owner in the first text channel of a server.
func (e *SimEnv) SendMessage(serverID, content string) error {
	for _, c := range e.World.Channels(serverID) {
		if c.Type == discord.ChannelText {
			_, err := e.World.SendMessage(e.Owner.ID, c.ID, content)
			return err
		}
	}
	return fmt.Errorf("server %s has no text channel", serverID)
}

// Servers lists the servers of the environment (to invite the bot to).
func (e *SimEnv) Servers() []discord.Server {
	e.mu.Lock()
	ids := append([]string(nil), e.servers...)
	e.mu.Unlock()
	var out []discord.Server
	for _, id := range ids {
		if s, ok := e.World.Server(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// Identify implements Probe.
func (e *SimEnv) Identify(ctx context.Context, token string) (Identity, error) {
	u, appID, err := e.botUser(token)
	if err != nil {
		return Identity{}, err
	}
	e.mu.Lock()
	in, known := e.intents[appID]
	name := e.names[appID]
	e.mu.Unlock()
	if !known {
		in = discord.PrivilegedIntents // the simulator's default for seeded bots
	}
	if name == "" {
		name = u.Username
	}
	return Identity{BotUserID: u.ID, BotUsername: u.Username, BotAvatarURL: u.AvatarURL, ApplicationID: appID,
		ApplicationName: name, Flags: FlagsFromIntents(in)}, nil
}

// Servers implements Probe (Servers method on the token).
func (e *SimEnv) ServersOf(ctx context.Context, token string) ([]ServerSummary, error) {
	u, _, err := e.botUser(token)
	if err != nil {
		return nil, err
	}
	out := []ServerSummary{}
	for _, s := range e.Servers() {
		if _, ok := e.World.Member(s.ID, u.ID); ok {
			out = append(out, ServerSummary{ID: s.ID, Name: s.Name, IconURL: s.IconURL})
		}
	}
	return out, nil
}

// NewSimProbe returns a Probe on a world that was not seeded by NewSimEnv (`cicord dev
// --sim` Diagnostics): privileged intents are reported enabled (the simulator default).
func NewSimProbe(w *sim.World) Probe {
	return simProbe{&SimEnv{World: w, intents: map[string]discord.Intents{}, names: map[string]string{}}}
}

// Probe returns the environment as a Probe.
func (e *SimEnv) Probe() Probe { return simProbe{e} }

type simProbe struct{ e *SimEnv }

func (p simProbe) Identify(ctx context.Context, token string) (Identity, error) {
	return p.e.Identify(ctx, token)
}
func (p simProbe) Servers(ctx context.Context, token string) ([]ServerSummary, error) {
	return p.e.ServersOf(ctx, token)
}
