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
	j.Emitted(t.Context(), "SendMessage", "not-an-id", time.Now())
	if len(entries) != 1 || entries[0].Target != "" {
		t.Fatalf("unexpected journal data: %+v", entries)
	}
	if got := j.Reason(strings.Repeat("é", 600)); len([]rune(got)) != 512 || !strings.HasPrefix(got, "cicord: a/b: ") {
		t.Fatal("invalid audit reason")
	}
}
