package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/postgres"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state/sqlite"
)

func TestProviderBudgetSharedAcrossBots(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testSharedBudget(t, store)
}

func TestProviderBudgetPostgres(t *testing.T) {
	raw := os.Getenv("CICORD_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("PostgreSQL fixture unavailable")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal("connect to PostgreSQL fixture")
	}
	defer conn.Close(ctx)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "budget_test_" + hex.EncodeToString(nonce[:])
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	dsn, err := url.Parse(raw)
	if err != nil {
		t.Fatal("invalid fixture URL")
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	st, err := postgres.Open(ctx, dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	testSharedBudget(t, st)
}

func testSharedBudget(t *testing.T, store state.Store) {
	t.Helper()
	decl := config.PluginSecret{Name: "KEY", Budget: &config.ProviderBudget{Provider: "anthropic-messages", DailyLimitConfig: "limit"}}
	manifest := &config.Plugin{Spec: config.PluginSpec{Secrets: []config.PluginSecret{decl}}}
	p := &config.LoadedPlugin{Source: "cicord/ai-assistant", Manifest: manifest}
	a := config.Instance{Bot: "a", Server: "server", Plugin: p, Config: map[string]any{"limit": float64(500)}}
	b := a
	b.Bot = "b"
	b.Config = map[string]any{"limit": float64(2000)}
	foreign := b
	foreign.Plugin = &config.LoadedPlugin{Source: "other/ai-assistant", Manifest: manifest}
	repo := &config.Repo{Project: &config.Project{}, Instances: []config.Instance{a, b, foreign}, Servers: map[string]*config.Server{"server": {Spec: config.ServerSpec{DiscordID: "guild"}}}}
	rt := &Runtime{active: &active{repo: repo}, opts: Options{Store: store}}
	ctx := t.Context()
	receipts := make(chan func(context.Context, int) error, 20)
	var wg sync.WaitGroup
	for n := range 20 {
		wg.Go(func() {
			owner := a
			if n%2 == 0 {
				owner = b
			}
			f, err := rt.reserveProvider(ctx, owner, "KEY", 100)
			if err == nil {
				receipts <- f
			} else if !errors.Is(err, plugin.ErrBudgetExhausted) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := len(receipts); got != 5 {
		t.Fatalf("held %d, want five", got)
	}
	if _, err := rt.reserveProvider(ctx, foreign, "KEY", 100); err != nil {
		t.Fatal("separate origin was affected", err)
	}
	f := <-receipts
	if err := f(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.reserveProvider(ctx, b, "KEY", 81); !errors.Is(err, plugin.ErrBudgetExhausted) {
		t.Fatal("over cap after reconciliation", err)
	}
	if _, err := rt.reserveProvider(ctx, b, "KEY", 80); err != nil {
		t.Fatal("refund missing", err)
	}
}
