package plugin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestModerationGrantBoundaries(t *testing.T) {
	for _, tc := range []struct {
		op    asyncOp
		input string
		grant string
	}{
		{opChannelPurge(false), `{"channelId":"100","limit":1}`, capMessagesRead},
		{opChannelBulkDelete, `{"channelId":"100","ids":["123"]}`, capMessagesManage},
		{opServerBans, `{}`, capMembersModerate},
		{opServerUnban, `{"userId":"42"}`, capMembersModerate},
		{opMemberSetRoles, `{"userId":"42","roles":[]}`, capRolesAssign},
	} {
		i := &Instance{grants: map[string]bool{capMessagesRead: true, capMessagesManage: true}}
		delete(i.grants, tc.grant)
		_, err := tc.op(i, nil, json.RawMessage(tc.input))
		var ce *CapabilityError
		if !errors.As(err, &ce) || ce.Capability != tc.grant {
			t.Fatalf("%s: %v", tc.input, err)
		}
	}
	i := &Instance{grants: map[string]bool{capMessagesRead: true, capMessagesManage: true}}
	for _, input := range []string{`{"limit":1,"contains":"abc"}`, `{"limit":5,"attachments":false}`} {
		_, err := opChannelPurge(true)(i, nil, json.RawMessage(input))
		var ce *CapabilityError
		if !errors.As(err, &ce) || ce.Capability != "MessageContent" {
			t.Fatalf("missing intent for %s: %v", input, err)
		}
	}
}

func TestBulkDeleteRejectsOldIDsBeforeMutation(t *testing.T) {
	h := start(t, `import {definePlugin} from "@cicord/sdk";export default definePlugin({});`, func(e *Env) { e.Grants = []string{capMessagesRead, capMessagesManage} })
	run, err := opChannelBulkDelete(h.inst, nil, json.RawMessage(`{"channelId":"100","ids":["123"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t.Context()); err == nil || !strings.Contains(err.Error(), "14 days") {
		t.Fatalf("old message: %v", err)
	}
}

func TestSetRolesKeepsGitMemberAssignment(t *testing.T) {
	h := start(t, `import {definePlugin} from "@cicord/sdk";export default definePlugin({});`, func(e *Env) { e.Grants = []string{capRolesAssign}; e.DeclaredMemberRoles = []string{"member"} })
	h.dc.mu.Lock()
	h.dc.members["42"] = discord.Member{User: discord.User{ID: "42"}, RoleIDs: []string{"200"}}
	h.dc.mu.Unlock()
	run, err := opMemberSetRoles(h.inst, nil, json.RawMessage(`{"userId":"42","roles":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t.Context()); err == nil || !strings.Contains(err.Error(), "declared in git") {
		t.Fatalf("protected role: %v", err)
	}
}
