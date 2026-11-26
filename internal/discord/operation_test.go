package discord

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAuditTargetNeverIncludesRouteSecrets(t *testing.T) {
	cases := []struct{ route, path, id string }{
		{"/guilds/{guild.id}/roles/{role.id}", "/guilds/1/roles/2?token=example", "2"},
		{"/webhooks/{webhook.id}/{webhook.token}", "/webhooks/1/private-test-value", ""},
		{"/interactions/{interaction.id}/{interaction.token}/callback", "/interactions/2/123456789/callback", ""},
		{"/channels/{channel.id}/messages/{message.id}", "/channels/1/messages/bad", ""},
	}
	for _, c := range cases {
		if got := AuditTarget(c.route, c.path); got != c.id {
			t.Errorf("target %q, want %q", got, c.id)
		}
	}
	var entries []Operation
	j := OperationScope{Origin: Operation{Bot: "a", Instance: "b"}, Record: func(_ context.Context, v Operation) { entries = append(entries, v) }}
	j.Emitted(t.Context(), "GetUser", "123", time.Now())
	j.Emitted(t.Context(), "SendMessage", "123456", time.Now())   // no audit entry
	j.Emitted(t.Context(), "UpdateRole", "not-an-id", time.Now()) // no snowflake target
	j.Emitted(t.Context(), "SetRolePositions", "123456", time.Now())
	if len(entries) != 0 {
		t.Fatalf("unexpected journal data: %+v", entries)
	}
	if got := j.Reason(strings.Repeat("é", 600)); len([]rune(got)) != 512 || !strings.HasPrefix(got, "cicord: a/b: ") {
		t.Fatal("invalid audit reason")
	}
}

// CORE-R7 and CORE-R4: only effects attribution can match are journaled, one per
// audit entry Discord writes for the call.
func TestCallEffectsFollowDiscordAuditEntries(t *testing.T) {
	for _, row := range []struct {
		method             string
		fields, overwrites []string
		want               []Effect
	}{
		{"UpdateChannel", []string{"name", "permission_overwrites"}, []string{"5", "6"}, []Effect{{11, "9", ""}, {AuditOverwriteCreate, "9", "5"}, {AuditOverwriteCreate, "9", "6"}, {AuditOverwriteCreate, "9", OverwriteRemovals}}},
		{"UpdateChannel", []string{"permission_overwrites"}, []string{"5"}, []Effect{{AuditOverwriteCreate, "9", "5"}, {AuditOverwriteCreate, "9", OverwriteRemovals}}},
		{"UpdateMember", []string{"roles", "nick"}, nil, []Effect{{AuditMemberRoleUpdate, "9", ""}, {AuditMemberUpdate, "9", ""}}},
		{"UpdateMember", []string{"channel_id"}, nil, nil},
		{"CreateWebhook", nil, nil, []Effect{{50, "9", ""}}},
		{"EditEvent", nil, nil, []Effect{{101, "9", ""}}},
		{"DeleteSoundboardSound", nil, nil, []Effect{{132, "9", ""}}},
		{"SendMessage", nil, nil, nil},
	} {
		got := CallEffects(row.method, "9", row.fields, row.overwrites)
		if len(got) != len(row.want) {
			t.Errorf("%s %v: %+v, want %+v", row.method, row.fields, got, row.want)
			continue
		}
		for n := range got {
			if got[n] != row.want[n] {
				t.Errorf("%s %v: %+v, want %+v", row.method, row.fields, got, row.want)
			}
		}
	}
	for _, code := range []int{AuditOverwriteCreate, AuditOverwriteUpdate, AuditOverwriteDelete} {
		if JournalAction(code) != AuditOverwriteCreate {
			t.Fatalf("overwrite %d is not in the overwrite family", code)
		}
	}
	var recorded, forgotten []Operation
	j := OperationScope{Record: func(_ context.Context, op Operation) { recorded = append(recorded, op) }, Forget: func(_ context.Context, op Operation) { forgotten = append(forgotten, op) }}
	withdraw := j.Journal(t.Context(), "UpdateChannel", CallEffects("UpdateChannel", "123456", []string{"permission_overwrites"}, []string{"234567"}), time.Now())
	withdraw(t.Context())
	if len(recorded) != 2 || recorded[0].Detail != "234567" || recorded[1].Detail != OverwriteRemovals || recorded[0].Nonce == "" || len(forgotten) != 2 || forgotten[0].Nonce != recorded[0].Nonce {
		t.Fatalf("recorded %+v, forgotten %+v", recorded, forgotten)
	}
}
