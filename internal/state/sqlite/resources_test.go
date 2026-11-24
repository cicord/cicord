package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func TestResources(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		rm := s.Resources()
		c := ctx(t)

		if _, err := rm.Get(c, "main", state.KindRole, "mod"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("Get missing: err=%v, want ErrNotFound", err)
		}
		if _, err := rm.GetByID(c, "main", "1"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("GetByID missing: err=%v, want ErrNotFound", err)
		}

		t0 := clk.Now()
		in := []state.Resource{
			{Server: "main", Kind: state.KindRole, Key: "mod", ID: "10", Owner: state.OwnerGit, UpdatedAt: t0},
			{Server: "main", Kind: state.KindChannel, Key: "general", ID: "20", Owner: state.OwnerGit, UpdatedAt: t0},
			{Server: "main", Kind: state.KindCategory, Key: "info", ID: "30", Owner: state.OwnerGit, UpdatedAt: t0},
			{Server: "main", Kind: state.KindChannel, Key: "announce", ID: "21", Owner: "plugin:bot/tickets", UpdatedAt: t0},
			{Server: "other", Kind: state.KindRole, Key: "mod", ID: "99", Owner: state.OwnerGit, UpdatedAt: t0},
		}
		for _, r := range in {
			must(t, rm.Put(c, r))
		}

		got, err := rm.Get(c, "main", state.KindChannel, "announce")
		must(t, err)
		if w := in[3]; got.Server != w.Server || got.Kind != w.Kind || got.Key != w.Key || got.ID != w.ID || got.Owner != w.Owner {
			t.Errorf("Get = %+v, want %+v", got, w)
		}
		assertTime(t, "UpdatedAt", got.UpdatedAt, t0)

		// Same key in another server is a separate resource.
		got, err = rm.Get(c, "other", state.KindRole, "mod")
		must(t, err)
		if got.ID != "99" {
			t.Errorf("other/mod ID = %q", got.ID)
		}

		// GetByID is scoped to the server.
		got, err = rm.GetByID(c, "main", "20")
		must(t, err)
		if got.Kind != state.KindChannel || got.Key != "general" {
			t.Errorf("GetByID(main, 20) = %+v", got)
		}
		if _, err := rm.GetByID(c, "other", "20"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("GetByID(other, 20): err=%v, want ErrNotFound", err)
		}

		// List: ordered by kind, then key; only the requested server.
		list, err := rm.List(c, "main")
		must(t, err)
		want := []string{"category/info", "channel/announce", "channel/general", "role/mod"}
		if len(list) != len(want) {
			t.Fatalf("List returned %d resources, want %d: %+v", len(list), len(want), list)
		}
		for i, r := range list {
			if k := string(r.Kind) + "/" + r.Key; k != want[i] {
				t.Errorf("List[%d] = %s, want %s", i, k, want[i])
			}
		}

		// Put replaces on (server, kind, key).
		clk.Advance(time.Minute)
		must(t, rm.Put(c, state.Resource{Server: "main", Kind: state.KindRole, Key: "mod", ID: "11", Owner: state.OwnerGit}))
		got, err = rm.Get(c, "main", state.KindRole, "mod")
		must(t, err)
		if got.ID != "11" {
			t.Errorf("after replace ID = %q, want 11", got.ID)
		}
		assertTime(t, "replaced UpdatedAt", got.UpdatedAt, clk.Now())
		if _, err := rm.GetByID(c, "main", "10"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("old ID still resolvable: err=%v", err)
		}
		if list, _ := rm.List(c, "main"); len(list) != 4 {
			t.Errorf("replace changed resource count to %d", len(list))
		}

		// Delete, including a missing resource.
		must(t, rm.Delete(c, "main", state.KindRole, "mod"))
		must(t, rm.Delete(c, "main", state.KindRole, "mod"))
		if _, err := rm.Get(c, "main", state.KindRole, "mod"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("Get after Delete: err=%v", err)
		}
		if _, err := rm.Get(c, "other", state.KindRole, "mod"); err != nil {
			t.Errorf("Delete leaked into another server: %v", err)
		}

		if list, err := rm.List(c, "nobody"); err != nil || len(list) != 0 {
			t.Errorf("List(empty server) = %v, %v", list, err)
		}
	})
}

func TestResourcePutValidation(t *testing.T) {
	s := mustOpen(t, ":memory:")
	for _, r := range []state.Resource{
		{Kind: state.KindRole, Key: "k", ID: "1"},
		{Server: "s", Key: "k", ID: "1"},
		{Server: "s", Kind: state.KindRole, ID: "1"},
		{Server: "s", Kind: state.KindRole, Key: "k"},
	} {
		if err := s.Resources().Put(ctx(t), r); err == nil {
			t.Errorf("Put(%+v) succeeded", r)
		}
	}
}
