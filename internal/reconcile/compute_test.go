package reconcile

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

const baseYAML = `
apiVersion: cicord.dev/v1alpha1
kind: Server
metadata: {name: test}
spec:
  discordId: "100"
  everyone: {permissions: [ViewChannel]}
  roles:
    admin: {name: Admin, color: "#ff0000", hoist: true, permissions: [Administrator]}
    member: {name: Member, permissions: [SendMessages]}
  hierarchy: [admin, member]
  categories:
    info:
      name: Info
      position: 0
      overwrites: {"@everyone": {deny: [SendMessages]}}
  channels:
    rules: {name: rules, type: text, category: info, position: 0}
    chat:
      name: chat
      type: text
      category: info
      position: 1
      overwrites: {member: {allow: [SendMessages]}}
`

func mustServer(t *testing.T, y string) *config.Server {
	t.Helper()
	var s config.Server
	if err := config.DecodeYAML([]byte(y), &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

var (
	pView  = discord.MustPermissions("ViewChannel")
	pSend  = discord.MustPermissions("SendMessages")
	pAdmin = discord.MustPermissions("Administrator")
)

// fixture is a live state matching baseYAML exactly.
type fixture struct {
	desired *config.Server
	live    Snapshot
	mapped  []state.Resource
	botTop  int
}

func base(t *testing.T) *fixture {
	everyoneDenySend := []discord.Overwrite{{ID: "100", Type: discord.OverwriteRole, Deny: pSend}}
	return &fixture{
		desired: mustServer(t, baseYAML),
		live: Snapshot{
			Server: discord.Server{ID: "100", Name: "Test"},
			Roles: []discord.Role{
				{ID: "100", Name: "@everyone", Permissions: pView},
				{ID: "900", Name: "bot", Position: 3, Managed: true, Permissions: pAdmin},
				{ID: "201", Name: "Admin", Color: 0xff0000, Hoist: true, Permissions: pAdmin, Position: 2},
				{ID: "202", Name: "Member", Permissions: pSend, Position: 1},
			},
			Channels: []discord.Channel{
				{ID: "301", ServerID: "100", Name: "Info", Type: discord.ChannelCategory, Overwrites: everyoneDenySend},
				{ID: "302", ServerID: "100", Name: "rules", Type: discord.ChannelText, ParentID: "301", Overwrites: everyoneDenySend},
				{ID: "303", ServerID: "100", Name: "chat", Type: discord.ChannelText, ParentID: "301", Position: 1,
					Overwrites: []discord.Overwrite{{ID: "202", Type: discord.OverwriteRole, Allow: pSend}}},
			},
			BotRoleIDs: []string{"900"}, HasBot: true,
		},
		mapped: []state.Resource{
			{Server: "test", Kind: state.KindRole, Key: "admin", ID: "201", Owner: state.OwnerGit},
			{Server: "test", Kind: state.KindRole, Key: "member", ID: "202", Owner: state.OwnerGit},
			{Server: "test", Kind: state.KindCategory, Key: "info", ID: "301", Owner: state.OwnerGit},
			{Server: "test", Kind: state.KindChannel, Key: "rules", ID: "302", Owner: state.OwnerGit},
			{Server: "test", Kind: state.KindChannel, Key: "chat", ID: "303", Owner: state.OwnerGit},
		},
		botTop: 3,
	}
}

func (f *fixture) role(id string) *discord.Role {
	for i := range f.live.Roles {
		if f.live.Roles[i].ID == id {
			return &f.live.Roles[i]
		}
	}
	panic(id)
}

func (f *fixture) channel(id string) *discord.Channel {
	for i := range f.live.Channels {
		if f.live.Channels[i].ID == id {
			return &f.live.Channels[i]
		}
	}
	panic(id)
}

func (f *fixture) unmap(kind state.ResourceKind, key string) {
	f.mapped = slices.DeleteFunc(f.mapped, func(r state.Resource) bool { return r.Kind == kind && r.Key == key })
}

func (f *fixture) policy(prune, protect bool) {
	f.desired.Spec.SyncPolicy = config.SyncPolicy{PruneP: &prune, DeletionProtectionP: &protect}
}

func (f *fixture) plan(t *testing.T) Plan {
	t.Helper()
	p, err := computePlan(planInput{Desired: f.desired, Live: f.live, Mapped: f.mapped, BotTop: f.botTop})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sig renders changes as "action kind key [fields]" for compact assertions.
func sig(p Plan) []string {
	var out []string
	for _, c := range p.Changes {
		s := c.Action + " " + c.Kind + " " + c.Key
		if len(c.Fields) > 0 && c.Action != ActionCreate {
			s += " [" + fieldList(c.Fields) + "]"
		}
		out = append(out, s)
	}
	return out
}

func blockedSig(p Plan) []string {
	var out []string
	for _, b := range p.Blocked {
		r := b.Reason
		if i := strings.Index(r, ":"); i > 0 {
			r = r[:i]
		}
		out = append(out, b.Action+" "+b.Kind+" "+b.Key+" "+r)
	}
	return out
}

func TestComputePlan(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(f *fixture)
		want    []string
		blocked []string
	}{
		{name: "no-op", mutate: func(f *fixture) {}},
		{
			name: "key renamed in YAML moves the mapping (no duplicate)",
			mutate: func(f *fixture) {
				f.desired.Spec.Channels["discussion"] = f.desired.Spec.Channels["chat"]
				delete(f.desired.Spec.Channels, "chat")
			},
			want: []string{"adopt channel discussion", "unmanage channel chat"},
		},
		{
			name:   "rename channel",
			mutate: func(f *fixture) { f.channel("303").Name = "old-chat" },
			want:   []string{"update channel chat [name]"},
		},
		{
			name:   "rename role",
			mutate: func(f *fixture) { f.role("202").Name = "Members" },
			want:   []string{"update role member [name]"},
		},
		{
			name: "text channel names are normalized like Discord",
			mutate: func(f *fixture) {
				ch := f.desired.Spec.Channels["chat"]
				ch.Name = "Chat"
				f.desired.Spec.Channels["chat"] = ch
			},
		},
		{
			name:   "role permission change",
			mutate: func(f *fixture) { f.role("202").Permissions = pSend | pView },
			want:   []string{"update role member [permissions]"},
		},
		{
			name:   "role color and hoist",
			mutate: func(f *fixture) { f.role("201").Color = 0x00ff00; f.role("201").Hoist = false },
			want:   []string{"update role admin [color, hoist]"},
		},
		{
			name:   "everyone permissions",
			mutate: func(f *fixture) { f.role("100").Permissions = pView | pSend },
			want:   []string{"update everyone @everyone [permissions]"},
		},
		{
			name:   "everyone not declared is untouched",
			mutate: func(f *fixture) { f.role("100").Permissions = pSend; f.desired.Spec.Everyone = nil },
		},
		{
			name:   "overwrite change",
			mutate: func(f *fixture) { f.channel("303").Overwrites[0].Allow = pSend | pView },
			want:   []string{"update channel chat [overwrites]"},
		},
		{
			name: "unknown overwrite removed",
			mutate: func(f *fixture) {
				f.channel("303").Overwrites = append(f.channel("303").Overwrites, discord.Overwrite{ID: "777", Type: discord.OverwriteMember, Allow: pView})
			},
			want: []string{"update channel chat [overwrites]"},
		},
		{
			name: "empty overwrite entries are ignored",
			mutate: func(f *fixture) {
				f.channel("303").Overwrites = append(f.channel("303").Overwrites, discord.Overwrite{ID: "777", Type: discord.OverwriteMember})
			},
		},
		{
			name:   "syncWithCategory restores category overwrites",
			mutate: func(f *fixture) { f.channel("302").Overwrites = nil },
			want:   []string{"update channel rules [overwrites]"},
		},
		{
			name: "category overwrite change also resyncs synced channels",
			mutate: func(f *fixture) {
				f.desired.Spec.Categories["info"].Overwrites["@everyone"] = config.OverwriteSpec{Deny: []string{"SendMessages", "AddReactions"}}
			},
			want: []string{"update category info [overwrites]", "update channel rules [overwrites]"},
		},
		{
			name:   "parent change",
			mutate: func(f *fixture) { f.channel("303").ParentID = "" },
			want:   []string{"update channel chat [category]"},
		},
		{
			name: "topic slowmode nsfw",
			mutate: func(f *fixture) {
				ch := f.desired.Spec.Channels["chat"]
				ch.Topic = "t"
				ch.Slowmode = 5
				ch.NSFW = true
				f.desired.Spec.Channels["chat"] = ch
			},
			want: []string{"update channel chat [nsfw, slowmode, topic]"},
		},
		{
			name:   "role reorder",
			mutate: func(f *fixture) { f.role("201").Position = 1; f.role("202").Position = 2 },
			want:   []string{"reorder role hierarchy [order]"},
		},
		{
			name: "unmanaged roles keep their place, no reorder needed",
			mutate: func(f *fixture) {
				f.live.Roles = append(f.live.Roles, discord.Role{ID: "250", Name: "Booster", Position: 2})
				f.role("201").Position = 3
				f.role("900").Position = 4
				f.botTop = 4
			},
		},
		{
			name:   "channel reorder",
			mutate: func(f *fixture) { f.channel("302").Position = 1; f.channel("303").Position = 0 },
			want:   []string{"reorder channel info [order]"},
		},
		{
			name: "voice and text channels are ordered separately",
			mutate: func(f *fixture) {
				f.desired.Spec.Channels["vc"] = config.Channel{Name: "Voice", Type: "voice", Category: "info", Position: new(0)}
				f.live.Channels = append(f.live.Channels, discord.Channel{ID: "305", ServerID: "100", Name: "Voice", Type: discord.ChannelVoice, ParentID: "301", Overwrites: f.channel("301").Overwrites})
				f.mapped = append(f.mapped, state.Resource{Server: "test", Kind: state.KindChannel, Key: "vc", ID: "305", Owner: state.OwnerGit})
			},
		},
		{
			name: "category reorder",
			mutate: func(f *fixture) {
				f.desired.Spec.Categories["zeta"] = config.Category{Name: "Zeta", Position: new(1)}
				f.live.Channels = append(f.live.Channels, discord.Channel{ID: "310", ServerID: "100", Name: "Zeta", Type: discord.ChannelCategory})
				f.channel("301").Position = 1
				f.mapped = append(f.mapped, state.Resource{Server: "test", Kind: state.KindCategory, Key: "zeta", ID: "310", Owner: state.OwnerGit})
			},
			want: []string{"reorder category categories [order]"},
		},
		{
			name:   "adopt by name",
			mutate: func(f *fixture) { f.unmap(state.KindRole, "admin") },
			want:   []string{"adopt role admin"},
		},
		{
			name:   "adopt by name with field update",
			mutate: func(f *fixture) { f.unmap(state.KindChannel, "chat"); f.channel("303").Topic = "x" },
			want:   []string{"adopt channel chat [topic]"},
		},
		{
			name: "adopt requires same channel type",
			mutate: func(f *fixture) {
				f.unmap(state.KindChannel, "chat")
				f.channel("303").Type = discord.ChannelVoice
			},
			want: []string{"create channel chat"},
		},
		{
			name: "plugin-owned resource is not adoptable",
			mutate: func(f *fixture) {
				f.unmap(state.KindRole, "member")
				f.mapped = append(f.mapped, state.Resource{Server: "test", Kind: state.KindRole, Key: "vip", ID: "202", Owner: "plugin:bot/x"})
			},
			want: []string{"create role member", "update channel chat [overwrites]"},
		},
		{
			name: "each live resource is adopted once",
			mutate: func(f *fixture) {
				f.unmap(state.KindRole, "admin")
				f.desired.Spec.Roles["admin2"] = config.RoleSpec{Name: "Admin", Color: "#ff0000", Hoist: true, Permissions: []string{"Administrator"}}
			},
			want: []string{"adopt role admin", "create role admin2"},
		},
		{
			name: "mapped resource deleted in Discord is created again",
			mutate: func(f *fixture) {
				f.live.Channels = slices.DeleteFunc(f.live.Channels, func(c discord.Channel) bool { return c.ID == "303" })
			},
			want: []string{"create channel chat"},
		},
		{
			name: "unmapped resources are untouched",
			mutate: func(f *fixture) {
				f.live.Roles = append(f.live.Roles, discord.Role{ID: "260", Name: "Random", Position: 1})
				f.role("202").Position = 2
				f.role("201").Position = 3
				f.role("900").Position = 4
				f.botTop = 4
				f.live.Channels = append(f.live.Channels, discord.Channel{ID: "320", ServerID: "100", Name: "random", Type: discord.ChannelText})
			},
		},
		{
			name: "plugin-owned resources are untouched, even with prune",
			mutate: func(f *fixture) {
				f.policy(true, false)
				f.live.Channels = append(f.live.Channels, discord.Channel{ID: "330", ServerID: "100", Name: "ticket-1", Type: discord.ChannelText, ParentID: "301", Position: 2})
				f.mapped = append(f.mapped, state.Resource{Server: "test", Kind: state.KindChannel, Key: "ticket-1", ID: "330", Owner: "plugin:bot/tickets"})
			},
		},
		{
			name: "plugin owns a declared key",
			mutate: func(f *fixture) {
				f.unmap(state.KindChannel, "chat")
				f.mapped = append(f.mapped, state.Resource{Server: "test", Kind: state.KindChannel, Key: "chat", ID: "303", Owner: "plugin:bot/x"})
			},
			blocked: []string{"update channel chat ownership"},
		},
		{
			name:   "removed key without prune: unmanage",
			mutate: func(f *fixture) { delete(f.desired.Spec.Channels, "chat") },
			want:   []string{"unmanage channel chat"},
		},
		{
			name:    "removed key with prune and deletionProtection: blocked",
			mutate:  func(f *fixture) { f.policy(true, true); delete(f.desired.Spec.Channels, "chat") },
			blocked: []string{"delete channel chat deletionProtection"},
		},
		{
			name: "removed keys with prune: delete (channels, categories, roles)",
			mutate: func(f *fixture) {
				f.policy(true, false)
				f.desired.Spec.Categories = nil
				f.desired.Spec.Channels = nil
				delete(f.desired.Spec.Roles, "member")
				f.desired.Spec.Hierarchy = []string{"admin"}
			},
			want: []string{"delete channel chat", "delete channel rules", "delete category info", "delete role member"},
		},
		{
			name: "removed key already gone from Discord: unmanage",
			mutate: func(f *fixture) {
				f.policy(true, false)
				delete(f.desired.Spec.Channels, "chat")
				f.live.Channels = f.live.Channels[:2]
			},
			want: []string{"unmanage channel chat"},
		},
		{
			name: "removed role above the bot: blocked",
			mutate: func(f *fixture) {
				f.policy(true, false)
				delete(f.desired.Spec.Roles, "admin")
				f.desired.Spec.Hierarchy = []string{"member"}
				f.role("201").Position = 4
			},
			blocked: []string{"delete role admin hierarchy"},
		},
		{
			name:    "role above the bot: update blocked",
			mutate:  func(f *fixture) { f.role("201").Position = 4; f.role("201").Color = 1 },
			blocked: []string{"update role admin hierarchy"},
		},
		{
			name: "role above the bot that must move below: reorder blocked",
			mutate: func(f *fixture) {
				f.desired.Spec.Hierarchy = []string{"member", "admin"}
				f.role("201").Position = 4
			},
			blocked: []string{"reorder role admin hierarchy"},
		},
		{
			name:    "type change without prune: blocked",
			mutate:  func(f *fixture) { f.channel("303").Type = discord.ChannelAnnouncement },
			blocked: []string{"update channel chat type change"},
		},
		{
			name: "type change with prune: replace",
			mutate: func(f *fixture) {
				f.policy(true, false)
				f.channel("303").Type = discord.ChannelAnnouncement
			},
			want: []string{"create channel chat", "delete channel chat"},
		},
		{
			name: "overwrite on a role created by the plan",
			mutate: func(f *fixture) {
				f.desired.Spec.Roles["vip"] = config.RoleSpec{Name: "VIP"}
				f.desired.Spec.Channels["chat"].Overwrites["vip"] = config.OverwriteSpec{Allow: []string{"ViewChannel"}}
			},
			want: []string{"create role vip", "update channel chat [overwrites]"},
		},
		{
			name: "server profile",
			mutate: func(f *fixture) {
				f.desired.Spec.Profile = &config.ServerProfile{Name: "New", VerificationLevel: "high", SystemChannel: "chat"}
				f.live.Server.VerificationLevel = "high"
			},
			want: []string{"update server test [name]", "update server test [systemChannel]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := base(t)
			tt.mutate(f)
			p := f.plan(t)
			if got := sig(p); !slices.Equal(got, tt.want) {
				t.Errorf("changes:\n got %q\nwant %q\n%s", got, tt.want, p)
			}
			if got := blockedSig(p); !slices.Equal(got, tt.blocked) {
				t.Errorf("blocked:\n got %q\nwant %q", got, tt.blocked)
			}
			if _, err := json.Marshal(p); err != nil {
				t.Errorf("plan is not JSON encodable: %v", err)
			}
		})
	}
}

func TestComputePlanFromEmpty(t *testing.T) {
	f := base(t)
	f.live.Roles = f.live.Roles[:2] // @everyone + bot
	f.live.Channels = nil
	f.mapped = nil
	f.role("100").Permissions = 0
	p := f.plan(t)
	want := []string{
		"update everyone @everyone [permissions]",
		"create role admin", "create role member", // hierarchy order: no reorder needed
		"create category info",
		"create channel rules", "create channel chat",
	}
	if got := sig(p); !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q\n%s", got, want, p)
	}
	c := p.Changes[4]
	if c.Summary != `create text channel #rules in "Info"` {
		t.Errorf("summary = %q", c.Summary)
	}
	if c.op.ch.parentKey != "info" || len(c.op.ch.overwrites) != 1 || c.op.ch.overwrites[0].target.id != "100" {
		t.Errorf("rules create spec = %+v", c.op.ch)
	}
	if ow := p.Changes[5].op.ch.overwrites; len(ow) != 1 || ow[0].target.roleKey != "member" {
		t.Errorf("chat overwrites = %+v", ow)
	}
}

func TestComputePlanNoBot(t *testing.T) {
	f := base(t)
	f.live.HasBot, f.live.BotRoleIDs = false, nil
	f.botTop = math.MaxInt
	f.role("201").Position = 4 // above the (unknown) bot: no constraint
	f.role("201").Color = 1
	if got := sig(f.plan(t)); !slices.Equal(got, []string{"update role admin [color]"}) {
		t.Fatalf("got %q", got)
	}
}

func TestComputePlanIcon(t *testing.T) {
	f := base(t)
	f.desired.Spec.Profile = &config.ServerProfile{Icon: "icon.png"}
	in := planInput{Desired: f.desired, Live: f.live, Mapped: f.mapped, BotTop: f.botTop, Icon: []byte("png")}
	p, err := computePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := sig(p); !slices.Equal(got, []string{"update server test [icon]"}) {
		t.Fatalf("got %q", got)
	}
	in.Live.Server.IconURL = "https://cdn/x.png"
	in.IconState = iconState{Hash: p.Changes[0].op.server.iconHash, URL: "https://cdn/x.png"}
	if p, _ = computePlan(in); !p.Empty() {
		t.Fatalf("icon up to date, got %s", p)
	}
	in.Live.Server.IconURL = "https://cdn/changed-by-hand.png"
	if p, _ = computePlan(in); p.Empty() {
		t.Fatal("icon changed in Discord should be re-uploaded")
	}
}

func TestComputePlanInvalidPermission(t *testing.T) {
	f := base(t)
	f.desired.Spec.Roles["member"] = config.RoleSpec{Name: "Member", Permissions: []string{"Nope"}}
	if _, err := computePlan(planInput{Desired: f.desired, Live: f.live, Mapped: f.mapped, BotTop: f.botTop}); err == nil {
		t.Fatal("want error")
	}
}

func TestSlotOrder(t *testing.T) {
	got, changed := slotOrder([]string{"a", "x", "b", "y", "c"}, []string{"c", "a", "b"})
	if !changed || !slices.Equal(got, []string{"c", "x", "a", "y", "b"}) {
		t.Fatalf("got %v %v", got, changed)
	}
	if _, changed := slotOrder([]string{"a", "x", "b"}, []string{"a", "b"}); changed {
		t.Fatal("unchanged order reported as changed")
	}
}

func TestDriftOf(t *testing.T) {
	f := base(t)
	f.channel("303").Name = "old-chat"
	d := DriftOf(f.plan(t))
	if len(d) != 1 || d[0].Kind != "channel" || d[0].Key != "chat" || d[0].Field != "name" || d[0].Expected != "chat" || d[0].Actual != "old-chat" {
		t.Fatalf("drift = %+v", d)
	}
}
