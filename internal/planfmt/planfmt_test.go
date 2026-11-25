package planfmt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/reconcile"
)

const samplePlan = `{
  "server": "ma-commu",
  "changes": [
    {"action": "create", "kind": "role", "key": "vip", "summary": "create role \"VIP\"",
     "fields": {"name": {"from": null, "to": "VIP"}, "color": {"from": null, "to": "#ff0000"}, "hoist": {"from": null, "to": false},
                "permissions": {"from": null, "to": ["SendMessages"]}}},
    {"action": "update", "kind": "channel", "key": "general", "id": "1", "summary": "rename channel #général → #discussions, update slowmode",
     "fields": {"name": {"from": "général", "to": "discussions"}, "slowmode": {"from": 0, "to": 10}}},
    {"action": "update", "kind": "role", "key": "modo", "id": "2", "summary": "update role \"Modo\": permissions",
     "fields": {"permissions": {"from": ["KickMembers", "ManageMessages"], "to": ["KickMembers", "BanMembers"]}}},
    {"action": "update", "kind": "category", "key": "staff", "id": "3", "summary": "update category \"Staff\": overwrites",
     "fields": {"overwrites": {"from": {"@everyone": {"deny": ["ViewChannel"]}, "old": {"allow": ["ViewChannel"]}},
                               "to": {"@everyone": {"deny": ["ViewChannel", "SendMessages"]}, "modo": {"allow": ["ViewChannel"]}}}}},
    {"action": "reorder", "kind": "role", "key": "hierarchy", "summary": "reorder roles: admin > modo",
     "fields": {"order": {"from": ["modo", "admin"], "to": ["admin", "modo"]}}},
    {"action": "adopt", "kind": "channel", "key": "rules", "id": "4", "summary": "adopt channel #règlement (4)"},
    {"action": "delete", "kind": "channel", "key": "old", "id": "5", "summary": "delete channel #old"},
    {"action": "unmanage", "kind": "role", "key": "legacy", "id": "6", "summary": "stop managing role \"Legacy\""}
  ],
  "blocked": [
    {"action": "delete", "kind": "role", "key": "gone", "id": "7", "reason": "deletionProtection"},
    {"action": "update", "kind": "role", "key": "top", "id": "8", "reason": "hierarchy: above the bot",
     "fields": {"color": {"from": "#000000", "to": "#ffffff"}}}
  ]
}`

func sample(t *testing.T) Plan {
	t.Helper()
	var p Plan
	if err := json.Unmarshal([]byte(samplePlan), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestText(t *testing.T) {
	var b strings.Builder
	Text(&b, []Plan{sample(t), {Server: "other"}})
	out := b.String()
	for _, want := range []string{
		"Server ma-commu: 8 change(s), 2 blocked",
		`  + create role "VIP"`,
		`      color:       "#ff0000"`,
		`      permissions: [SendMessages]`,
		"  ~ rename channel #général → #discussions, update slowmode",
		`      name:     "général" → "discussions"`,
		"      slowmode: 0 → 10",
		"      permissions: +BanMembers -ManageMessages",
		"      overwrites:",
		"        ~ @everyone: deny +SendMessages",
		"        + modo: allow ViewChannel",
		"        - old: allow ViewChannel",
		"  > reorder roles: admin > modo",
		"      order: modo, admin → admin, modo",
		"  = adopt channel #règlement (4)",
		"  - delete channel #old",
		`  # stop managing role "Legacy"`,
		"  ! blocked: delete role gone (deletionProtection)",
		"  ! blocked: update role top (hierarchy: above the bot)",
		`      color: "#000000" → "#ffffff"`,
		"Server other: no changes",
		"Plan: 1 to create, 3 to update, 1 to delete, 1 to reorder, 1 to adopt, 1 to stop managing. 2 operation(s) blocked.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// false / empty values of a create are not listed
	if strings.Contains(out, "hoist") {
		t.Errorf("zero create field shown:\n%s", out)
	}
}

func TestSummaryAndPending(t *testing.T) {
	empty := Plan{Server: "x", Blocked: []Blocked{{Action: "sync", Kind: "server", Key: "x", Reason: "no connected bot"}}}
	if Pending([]Plan{empty}) {
		t.Error("blocked-only plan is pending")
	}
	if got := SummaryLine(empty); got != "No changes. Discord matches the configuration. 1 operation(s) blocked." {
		t.Errorf("summary = %q", got)
	}
	if !Pending([]Plan{sample(t)}) {
		t.Error("plan not pending")
	}
	var b strings.Builder
	Text(&b, []Plan{empty})
	if !strings.Contains(b.String(), "! blocked: sync server x (no connected bot)") {
		t.Errorf("blocked server:\n%s", b.String())
	}
}

func TestFromReconcile(t *testing.T) {
	ps, err := FromReconcile(reconcile.Plan{Server: "s", Changes: []reconcile.Change{{
		Action: reconcile.ActionCreate, Kind: "channel", Key: "c", Summary: "create text channel #c",
		Fields: map[string]reconcile.FieldChange{"userLimit": {To: 5}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Blocked == nil || ps[0].Changes[0].Fields["userLimit"].To != float64(5) {
		t.Fatalf("converted = %+v", ps)
	}
}

func TestMarkdown(t *testing.T) {
	r := Result{Valid: true, Plans: []Plan{sample(t)}, Diagnostics: []config.Diagnostic{{Severity: config.SeverityWarning, File: "bots/b/bot.yaml", Message: "privileged intent"}}}
	md := Markdown(r, MarkdownOptions{Marker: "<!-- cicord-plan:x -->", Title: "Cicord plan for `x`", Footer: "footer"})
	for _, want := range []string{
		"<!-- cicord-plan:x -->\n### Cicord plan for `x`",
		"**Plan: 1 to create",
		"<summary>Warnings (1)</summary>",
		"<details open><summary>Server <code>ma-commu</code>: 8 change(s), 2 blocked</summary>",
		"```diff\n+ create role \"VIP\"",
		"\nfooter\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	bad := Markdown(Result{Valid: false, Diagnostics: []config.Diagnostic{{Severity: config.SeverityError, File: "servers/x.yaml", Line: 3, Message: "unknown role \"x\""}}}, MarkdownOptions{})
	if !strings.Contains(bad, "**Validation failed**") || !strings.Contains(bad, "<details open><summary>Errors (1)</summary>") ||
		!strings.Contains(bad, `servers/x.yaml:3: error: unknown role "x"`) {
		t.Errorf("invalid:\n%s", bad)
	}
	// truncation stays below the limit
	big := Plan{Server: "big"}
	for i := 0; i < 3000; i++ {
		big.Changes = append(big.Changes, Change{Action: "create", Kind: "channel", Key: "c", Summary: strings.Repeat("é", 30)})
	}
	long := Markdown(Result{Valid: true, Plans: []Plan{big}}, MarkdownOptions{MaxBytes: 10000})
	if len(long) > 10000 || !strings.Contains(long, "truncated") {
		t.Errorf("truncated length %d", len(long))
	}
}

func TestMemberRoles(t *testing.T) {
	const alice, bob, carol = "123456789012345678", "223456789012345678", "323456789012345678"
	ps, err := FromReconcile(reconcile.Plan{
		Server: "s",
		Changes: []reconcile.Change{{
			Action: reconcile.ActionUpdate, Kind: reconcile.KindMemberRole, Key: "maintainer/" + alice, ID: alice,
			Summary: `give role "Maintainer" to @Alice (123456789012345678)`,
			Fields:  map[string]reconcile.FieldChange{"assigned": {From: false, To: true}},
		}},
		Blocked: []reconcile.Blocked{{
			Action: reconcile.ActionUpdate, Kind: reconcile.KindMemberRole, Key: "owner/" + bob, ID: bob,
			Reason:  `hierarchy: role "Owner" is at or above the bot's highest role`,
			Summary: `give role "Owner" to @Bob (223456789012345678)`,
			Fields:  map[string]reconcile.FieldChange{"assigned": {From: false, To: true}},
		}},
		Notes: []reconcile.Note{{Kind: reconcile.KindMemberRole, Key: "maintainer/" + carol,
			Message: `roles.maintainer.members: user 323456789012345678 is not in the server; they get the role "Maintainer" once they join`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	Text(&b, ps)
	out := b.String()
	for _, want := range []string{
		"Server s: 1 change(s), 1 blocked",
		`  ~ give role "Maintainer" to @Alice (123456789012345678)`,
		`  ! blocked: give role "Owner" to @Bob (223456789012345678) (hierarchy: role "Owner" is at or above the bot's highest role)`,
		`  note: roles.maintainer.members: user 323456789012345678 is not in the server; they get the role "Maintainer" once they join`,
		"Plan: 0 to create, 1 to update, 0 to delete. 1 operation(s) blocked.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "assigned") {
		t.Errorf("redundant field lines:\n%s", out)
	}
	md := Markdown(Result{Valid: true, Plans: ps}, MarkdownOptions{})
	if !strings.Contains(md, "1 change(s), 1 blocked, 1 note(s)</summary>") || !strings.Contains(md, "\nnote: roles.maintainer.members: user 323456789012345678") {
		t.Errorf("markdown:\n%s", md)
	}
	// a plan with only notes has no changes
	var nb strings.Builder
	Text(&nb, []Plan{{Server: "n", Notes: ps[0].Notes}})
	if !strings.Contains(nb.String(), "Server n: no changes, 1 note(s)") || Pending([]Plan{{Server: "n", Notes: ps[0].Notes}}) {
		t.Errorf("notes only:\n%s", nb.String())
	}
}
