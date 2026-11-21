package reconcile

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/discord"
)

func TestHumanize(t *testing.T) {
	// Real text shapes: disgoadapter.mapError + apply.go prefixes + withCommunityHint.
	announcement := `reconcile ma-commu: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request ` +
		`(HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2, 4, 6, 13, 14, 15}."}]}}) ` +
		`(announcement channels require the Community feature: Server Settings → Enable Community, then sync again)`
	cases := []struct {
		name     string
		raw      string
		code     string
		message  string // substring
		hint     string // substring
		resource string // action/kind/key
	}{
		{"announcement needs community", announcement, ErrCodeCommunityRequired,
			"Announcement and stage channels need the Community feature", "Enable Community", "create/channel/news"},
		{"community without reconcile hint",
			`create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2}."}]}})`,
			ErrCodeCommunityRequired, "Community", "Enable Community", "create/channel/news"},
		{"rules channel needs community",
			`reconcile s: 1 change(s) failed: update server s: discord UpdateServer: discord: bad request (HTTP 400 50035: Invalid Form Body {"rules_channel_id":{"_errors":[{"code":"GUILD_RULES_CHANNEL_INVALID","message":"x"}]}})`,
			ErrCodeCommunityRequired, "Community feature", "Enable Community", "update/server/s"},
		{"missing permissions on a role",
			`reconcile s: 1 change(s) failed: update role modo: discord UpdateRole: discord: missing permissions (HTTP 403 50013: Missing Permissions)`,
			ErrCodeMissingPermissions, "needs the Manage Roles permission to update the role “modo”", "Manage Roles", "update/role/modo"},
		{"missing permissions on a channel",
			`create channel general: discord CreateChannel: discord: missing permissions (HTTP 403 50013: Missing Permissions)`,
			ErrCodeMissingPermissions, "Manage Channels", "Manage Channels", "create/channel/general"},
		{"missing permissions on the server",
			`update server ma-commu: discord UpdateServer: discord: missing permissions (HTTP 403 50013: Missing Permissions)`,
			ErrCodeMissingPermissions, "Manage Server permission to update the server settings", "", "update/server/ma-commu"},
		{"missing access", `update channel staff: discord UpdateChannel: discord: missing permissions (HTTP 403 50001: Missing Access)`,
			ErrCodeMissingAccess, "cannot see the channel “staff”", "View Channel", "update/channel/staff"},
		{"sim grant", `reconcile s: 1 change(s) failed: create role admin: discord: missing permissions: cannot grant permissions you do not have: Administrator, BanMembers`,
			ErrCodeMissingPermissions, "cannot grant permissions it does not have: Administrator, BanMembers", "Roles", "create/role/admin"},
		{"sim hierarchy", `update role top: discord: missing permissions: cannot move role Top at or above your highest role`,
			ErrCodeRoleHierarchy, "not high enough to change the role “top”", "drag the bot's role", "update/role/top"},
		{"unknown channel", `update channel news: discord UpdateChannel: discord: not found (HTTP 404 10003: Unknown Channel)`,
			ErrCodeUnknownResource, "The channel “news” no longer exists on Discord", "recreate", "update/channel/news"},
		{"unknown guild", `discord GetServer: discord: not found (HTTP 404 10004: Unknown Guild)`,
			ErrCodeUnknownResource, "bot was removed", "discordId", ""},
		{"form body summary", `create role vip: discord CreateRole: discord: bad request (HTTP 400 50035: Invalid Form Body {"name":{"_errors":[{"code":"BASE_TYPE_MAX_LENGTH","message":"Must be 100 or fewer in length."}]},"permissions":{"_errors":[{"code":"X","message":"Bad."}]}})`,
			ErrCodeInvalidFormBody, "Discord rejected the role “vip”: name: Must be 100 or fewer in length; permissions: Bad", "“name”, “permissions”", "create/role/vip"},
		{"nested form body", `update channel c: discord UpdateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"permission_overwrites":{"0":{"id":{"_errors":[{"code":"X","message":"Unknown."}]}}}})`,
			ErrCodeInvalidFormBody, "permission_overwrites.0.id: Unknown", "", "update/channel/c"},
		{"limit", `create role r: discord CreateRole: discord: bad request (HTTP 400 30005: Maximum number of guild roles reached (250))`,
			ErrCodeLimitReached, "Maximum number of guild roles reached (250)", "Remove unused", "create/role/r"},
		{"invalid token", `discord gateway: discord: invalid token (close 4004: Authentication failed.)`,
			ErrCodeInvalidToken, "rejected the bot token", "Developer Portal", ""},
		{"intents", `discord gateway: discord: missing permissions: privileged intents not enabled in the developer portal (close 4014: Disallowed intent(s).)`,
			ErrCodeIntentsDisallowed, "privileged intents", "Privileged Gateway Intents", ""},
		{"sim intents", `discord: missing permissions: disallowed intents ServerMembers, MessageContent (not enabled for the application)`,
			ErrCodeIntentsDisallowed, ": ServerMembers, MessageContent", "", ""},
		{"rate limited", `update role a: discord UpdateRole: HTTP 429 0: You are being rate limited.`, ErrCodeRateLimited, "rate limiting", "", "update/role/a"},
		{"discord down", `discord ListRoles: HTTP 502 0: Bad Gateway`, ErrCodeDiscordUnavailable, "HTTP 502", "discordstatus.com", ""},
		{"timeout", `reconcile s: 1 change(s) failed: create role a: discord CreateRole: context deadline exceeded`, ErrCodeTimeout, "did not answer in time", "", "create/role/a"},
		{"no bot", `no connected bot is configured for this server`, ErrCodeNoBot, "No connected bot", "spec.servers", ""},
		{"not connected", `not connected`, ErrCodeBotNotConnected, "not connected", "reconnects", ""},
		{"unresolved", `create channel a: unresolved category "info"`, ErrCodeUnresolvedRef, "refers to the category \"info\"", "", "create/channel/a"},
		{"rejected revision", `revision a91e7c3 rejected: bots/moderator/bot.yaml: unknown role "xbox"`, ErrCodeRevisionRejected,
			`Revision a91e7c3 was rejected: bots/moderator/bot.yaml: unknown role "xbox"`, "last valid revision", ""},
		{"fetch", `fetch: git fetch: exit status 128`, ErrCodeFetchFailed, "could not be fetched", "credentials", ""},
		{"sim bad request", `update channel v: discord: bad request: bitrate must be 8000-384000`, ErrCodeBadRequest,
			"Discord refused to update the channel “v”: bitrate must be 8000-384000", "", "update/channel/v"},
		{"self-heal prefix", `self-heal: reconcile s: 1 change(s) failed: delete role old: discord DeleteRole: discord: missing permissions (HTTP 403 50013: Missing Permissions)`,
			ErrCodeMissingPermissions, "delete the role “old”", "", "delete/role/old"},
		{"boost via field", `update server s: discord UpdateServer: discord: bad request (HTTP 400 50035: Invalid Form Body {"banner":{"_errors":[{"code":"X","message":"Not allowed."}]}})`,
			ErrCodeBoostRequired, "boost level", "Boost the server", "update/server/s"},
		{"role named Server Booster", `update role booster: discord: missing permissions: cannot move role Server Booster at or above your highest role`,
			ErrCodeRoleHierarchy, "not high enough", "", "update/role/booster"},
		{"role named Premium", `update role premium: discord UpdateRole: discord: missing permissions (HTTP 403 50013: Missing Permissions) role Premium`,
			ErrCodeMissingPermissions, "Manage Roles", "", "update/role/premium"},
		{"unknown", `something odd happened {"a":1}`, ErrCodeUnknown, "something odd happened", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Humanize(tc.raw)
			if h == nil {
				t.Fatal("nil")
			}
			if h.Code != tc.code {
				t.Errorf("code = %q, want %q (message %q)", h.Code, tc.code, h.Message)
			}
			if !strings.Contains(h.Message, tc.message) {
				t.Errorf("message = %q, want it to contain %q", h.Message, tc.message)
			}
			if strings.Contains(h.Message, "{") && tc.code != ErrCodeLimitReached && tc.code != ErrCodeInvalidFormBody {
				t.Errorf("message leaks JSON: %q", h.Message)
			}
			if tc.hint != "" && !strings.Contains(h.Hint, tc.hint) {
				t.Errorf("hint = %q, want it to contain %q", h.Hint, tc.hint)
			}
			if h.Details != strings.TrimSpace(tc.raw) {
				t.Errorf("details must keep the raw text")
			}
			got := ""
			if h.Resource != nil {
				got = h.Resource.Action + "/" + h.Resource.Kind + "/" + h.Resource.Key
			}
			if got != tc.resource {
				t.Errorf("resource = %q, want %q", got, tc.resource)
			}
		})
	}
}

func TestHumanizeDetails(t *testing.T) {
	if Humanize("  ") != nil || HumanizeErr(nil) != nil {
		t.Fatal("empty input must give nil")
	}
	h := Humanize(`reconcile ma-commu: 3 change(s) failed: create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[{"code":"BASE_TYPE_CHOICES","message":"Value must be one of {0, 2}."}]}})
create channel stage: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {})
update role a: x`)
	if h.Failures != 3 || h.HTTPStatus != 400 || h.DiscordCode != 50035 {
		t.Fatalf("failures/status/code = %d/%d/%d", h.Failures, h.HTTPStatus, h.DiscordCode)
	}
	if len(h.Fields) != 1 || h.Fields[0].Path != "type" || h.Fields[0].Code != "BASE_TYPE_CHOICES" {
		t.Fatalf("fields = %+v", h.Fields)
	}
	// withCommunityHint output is recognized.
	err := withCommunityHint(discord.ChannelAnnouncement, fmt.Errorf("discord CreateChannel: %w (HTTP 400 50035: Invalid Form Body)", discord.ErrBadRequest))
	if got := HumanizeErr(fmt.Errorf("create channel news: %w", err)); got.Code != ErrCodeCommunityRequired {
		t.Fatalf("withCommunityHint: %q", got.Code)
	}
	if got := HumanizeErr(errors.New("x")); got.Message != "x" {
		t.Fatalf("plain: %q", got.Message)
	}
}

func TestExplainBlocked(t *testing.T) {
	cases := []struct {
		b       Blocked
		code    string
		message string
	}{
		{Blocked{Action: ActionDelete, Kind: KindRole, Key: "old", Reason: "deletionProtection"}, ErrCodeDeletionProtection, "The role “old” was removed from the YAML"},
		{Blocked{Action: ActionUpdate, Kind: KindRole, Key: "top", Reason: reasonHierarchy}, ErrCodeRoleHierarchy, "at or above the bot's highest role"},
		{Blocked{Action: ActionUpdate, Kind: KindChannel, Key: "x", Reason: "ownership: key is owned by plugin:tickets"}, ErrCodeOwnership, "managed by plugin:tickets"},
		{Blocked{Action: ActionUpdate, Kind: KindRole, Key: "bot", Reason: "managed: role belongs to an integration"}, ErrCodeManagedRole, "integration"},
		{Blocked{Action: ActionUpdate, Kind: KindChannel, Key: "news", Reason: "type change: text → forum requires recreating the channel"}, ErrCodeTypeChange, "cannot change the type of the channel “news”"},
		{Blocked{Action: "sync", Kind: KindServer, Key: "s", Reason: "no connected bot for this server"}, ErrCodeNoBot, "No connected bot"},
		{Blocked{Action: ActionUpdate, Kind: KindRole, Key: "z", Reason: "weird"}, ErrCodeUnknown, "Weird"},
	}
	for _, tc := range cases {
		h := ExplainBlocked(tc.b)
		if h.Code != tc.code || !strings.Contains(h.Message, tc.message) {
			t.Errorf("%s: got %q %q", tc.b.Reason, h.Code, h.Message)
		}
		if h.Details != tc.b.Reason {
			t.Errorf("details = %q", h.Details)
		}
	}
}
