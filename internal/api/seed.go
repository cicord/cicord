package api

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
)

// SimSeed is the result of SeedSimWorld.
type SimSeed struct {
	Owner discord.User
	// Users are extra users (not members of any server).
	Users []discord.User
	// Secrets maps each bot's token secretRef to its simulated token.
	Secrets map[string]string
	// Bots maps bot keys to bot users.
	Bots map[string]discord.User
}

// SeedSimWorld fills a simulator world from a repository (cicord dev --sim): an "owner"
// user owning every server (created with its discordId), every bot (with its
// applicationId) invited with Administrator to its servers, and a few extra users.
func SeedSimWorld(w *sim.World, repo *config.Repo, extraUsers ...string) (*SimSeed, error) {
	seed := &SimSeed{Secrets: map[string]string{}, Bots: map[string]discord.User{}}
	seed.Owner = w.CreateUser("owner")
	keys := make([]string, 0, len(repo.Servers))
	for k := range repo.Servers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		srv := repo.Servers[k]
		name := k
		if srv.Spec.Profile != nil && srv.Spec.Profile.Name != "" {
			name = srv.Spec.Profile.Name
		}
		if _, err := w.CreateServerWithID(srv.Spec.DiscordID, name, seed.Owner.ID); err != nil {
			return nil, fmt.Errorf("server %s: %w", k, err)
		}
		if err := seedSimServer(w, srv); err != nil {
			return nil, fmt.Errorf("server %s: %w", k, err)
		}
	}
	keys = keys[:0]
	for k := range repo.Bots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := repo.Bots[k]
		name := k
		if b.Spec.Profile != nil && b.Spec.Profile.Username != "" {
			name = b.Spec.Profile.Username
		}
		token, u, err := w.CreateBotWithApplicationID(name, b.Spec.ApplicationID)
		if err != nil {
			return nil, fmt.Errorf("bot %s: %w", k, err)
		}
		seed.Secrets[b.Spec.Token.SecretRef] = token
		seed.Bots[k] = u
		for _, sk := range b.Spec.Servers {
			srv := repo.Servers[sk]
			if srv == nil {
				continue
			}
			if _, err := w.AddBotToServer(srv.Spec.DiscordID, u.ID, discord.MustPermissions("Administrator")); err != nil {
				return nil, fmt.Errorf("bot %s → server %s: %w", k, sk, err)
			}
		}
	}
	if len(extraUsers) == 0 {
		extraUsers = []string{"alice", "bob", "charlie"}
	}
	for _, n := range extraUsers {
		seed.Users = append(seed.Users, w.CreateUser(n))
	}
	return seed, nil
}

// Simulator annotations on a Server resource (cicord dev --sim only).
const (
	// AnnotationSimBoosts is the number of boosts of the simulated server ("14" = level 3).
	AnnotationSimBoosts = "sim.cicord.dev/boosts"
	// AnnotationSimFeatures adds feature flags to the simulated server ("PARTNERED,VERIFIED").
	AnnotationSimFeatures = "sim.cicord.dev/features"
)

func seedSimServer(w *sim.World, srv *config.Server) error {
	ann := srv.Metadata.Annotations
	if v := ann[AnnotationSimFeatures]; v != "" {
		if err := w.SetFeatures(srv.Spec.DiscordID, sim.ParseFeatureList(v), nil); err != nil {
			return err
		}
	}
	if v := ann[AnnotationSimBoosts]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("annotation %s: %q is not a boost count", AnnotationSimBoosts, v)
		}
		if err := w.SetBoosts(srv.Spec.DiscordID, n); err != nil {
			return err
		}
	}
	return nil
}
