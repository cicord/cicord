package catalog

import (
	"strings"
	"testing"
)

func TestOfficialCatalog(t *testing.T) {
	c := Official()
	es, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"welcome", "onboarding", "tickets", "polls", "levels", "moderation", "reaction-roles", "audit-log", "announcements"}
	if len(es) != len(want) {
		t.Fatalf("got %d entries", len(es))
	}
	for i, e := range es {
		if e.Name != want[i] || e.Description == "" || len(e.Capabilities) == 0 {
			t.Fatalf("entry %d = %+v", i, e)
		}
	}
	lv, _ := c.Get("levels")
	if !strings.Contains(strings.Join(lv.PrivilegedIntents, ","), "MessageContent") || len(lv.Commands) == 0 {
		t.Fatalf("levels = %+v", lv)
	}
	files, err := c.Files("tickets")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["plugins/tickets/plugin.yaml"]; !ok {
		t.Fatalf("files = %v", SortedPaths(files))
	}
	if strings.Contains(string(files["plugins/tickets/tsconfig.json"]), "spec/sdk") {
		t.Fatal("tsconfig still points at the Cicord repository")
	}
	for p := range files {
		if strings.Contains(p, "node_modules") || strings.Contains(p, "/test/") {
			t.Fatalf("unexpected file %s", p)
		}
	}
}
