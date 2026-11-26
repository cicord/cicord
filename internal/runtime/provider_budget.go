package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

func budgetOrigin(repo *config.Repo, p *config.LoadedPlugin) string {
	if strings.HasPrefix(p.Source, ".") {
		rel, e := filepath.Rel(repo.Root, p.Dir)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return "local/" + filepath.ToSlash(rel)
	}
	registry, _, _ := strings.Cut(p.Source, "/")
	for _, r := range repo.Project.Spec.Registries {
		if r.Name == registry {
			return r.URL + "\x00" + p.Source
		}
	}
	return p.Source
}

func (r *Runtime) reserveProvider(ctx context.Context, in config.Instance, secret string, units int) (func(context.Context, int) error, error) {
	repo := r.Repo()
	if repo == nil || repo.Servers[in.Server] == nil {
		return nil, errors.New("provider config unavailable")
	}
	guild := repo.Servers[in.Server].Spec.DiscordID
	origin := budgetOrigin(repo, in.Plugin)
	if origin == "" {
		return nil, errors.New("provider origin unavailable")
	}
	provider := ""
	for _, s := range in.Plugin.Manifest.Spec.Secrets {
		if s.Name == secret && s.Budget != nil {
			provider = s.Budget.Provider
		}
	}
	if provider != "anthropic-messages" {
		return nil, errors.New("unsupported provider budget")
	}
	// A lower cap on any enabled sibling wins, including across bot identities.
	limit := 0
	for _, sibling := range repo.Instances {
		if repo.Servers[sibling.Server].Spec.DiscordID != guild || budgetOrigin(repo, sibling.Plugin) != origin || sibling.Config["enabled"] == false {
			continue
		}
		for _, decl := range sibling.Plugin.Manifest.Spec.Secrets {
			if decl.Budget == nil || decl.Budget.Provider != provider {
				continue
			}
			n, ok := decl.BudgetLimit(sibling.Config)
			if !ok {
				return nil, errors.New("invalid daily provider limit")
			}
			if limit == 0 || n < limit {
				limit = n
			}
		}
	}
	if limit == 0 {
		return nil, errors.New("provider budget disabled")
	}
	scope := fmt.Sprintf("cicord:provider-budget:%x", sha256.Sum256([]byte(guild+"\x00"+origin+"\x00"+provider)))
	return reserveDaily(ctx, r.opts.Store, scope, time.Now().UTC().Format("2006-01-02"), units, limit)
}

// Every process uses the same database transaction and server UTC date. Lost
// responses and crashes leave the maximum request cost reserved until rollover.
func reserveDaily(ctx context.Context, store state.Store, scope, day string, units, limit int) (func(context.Context, int) error, error) {
	atomic, ok := store.(state.AtomicScope)
	if !ok || units <= 0 || limit <= 0 || units > math.MaxInt32 {
		return nil, errors.New("budget storage unavailable")
	}
	err := atomic.WithAtomicScope(ctx, scope, func(kv state.KV) error {
		var total int
		raw, err := kv.Get(ctx, day)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil {
			if err := json.Unmarshal(raw, &total); err != nil || total < 0 {
				return errors.New("invalid budget counter")
			}
		}
		if total > limit-units {
			return plugin.ErrBudgetExhausted
		}
		return kv.Set(ctx, day, json.RawMessage(strconv.Itoa(total+units)), 72*time.Hour)
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, used int) error {
		if used == units {
			return nil
		}
		_, err := store.KV(scope).Increment(ctx, day, float64(used-units))
		return err
	}, nil
}
