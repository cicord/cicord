package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Masterminds/semver/v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// The implicit official registry (ADR-0011, "Update (2026-11-23)").
//
// When registry.Official() has a URL, source "cicord" is the union of the plugins bundled in
// the binary and those of the official remote registry (its signed index), without any
// cicord.yaml entry. A plugin in both comes from the remote registry only when its index
// entry has a valid semver `version` greater than the bundled version; otherwise the
// bundled copy wins (it ships with the binary and needs no network). When the official
// index cannot be read or verified, source "cicord" lists the bundled plugins and carries
// the error.

// Signature states of a source.
const (
	// SignatureVerified: the index is signed by an official key.
	SignatureVerified = "verified"
	// SignatureInvalid: an official URL whose index has no valid official signature.
	SignatureInvalid = "invalid"
)

// remoteWins reports whether the official remote entry replaces the bundled plugin.
func remoteWins(bundledVersion string, bundled bool, e registry.IndexEntry) bool {
	if !bundled {
		return true
	}
	rv, err := semver.NewVersion(e.Version)
	if err != nil {
		return false
	}
	bv, err := semver.NewVersion(bundledVersion)
	if err != nil {
		return true
	}
	return rv.GreaterThan(bv)
}

// OfficialRemote reports whether the official plugin name comes from the official remote
// registry (reg) rather than from the binary. err is set when it can only come from the
// remote registry and that registry cannot be read.
func (s *Service) OfficialRemote(ctx context.Context, name string) (reg config.Registry, remote bool, err error) {
	var bundledVersion string
	bundled := false
	if s.Catalog != nil {
		if e, ok := s.Catalog.Get(name); ok {
			bundled, bundledVersion = true, e.Version
		}
	}
	off, ok := registry.Official()
	if !ok || !registry.NameRe.MatchString(name) {
		if !bundled {
			return config.Registry{}, false, ErrUnknownPlugin
		}
		return config.Registry{}, false, nil
	}
	c := s.index(ctx, off, false)
	if c.err != nil {
		if bundled {
			return off, false, nil
		}
		return off, false, &UnavailableError{Source: off.Name, Err: c.err}
	}
	e, inIndex := c.idx.Plugins[name]
	switch {
	case inIndex && remoteWins(bundledVersion, bundled, e):
		return off, true, nil
	case bundled:
		return off, false, nil
	}
	return off, false, ErrUnknownPlugin
}

// OfficialOrigin is OfficialRemote for the page of an official plugin in a repository: a
// plugin locked in cicord.lock from the official registry ("cicord/<name>", what its
// instances run and what its update review compares with) is described from that
// registry even when the cached index does not list a version newer than the bundled one
// (the index may lag behind the release tags the update check reads). Otherwise the Store
// page and its table could disagree on the update.
func (s *Service) OfficialOrigin(ctx context.Context, repo *config.Repo, name string) (reg config.Registry, remote bool, err error) {
	if off, ok := registry.Official(); ok && registry.NameRe.MatchString(name) && lockedOfficial(repo, name) {
		return off, true, nil
	}
	return s.OfficialRemote(ctx, name)
}

// lockedOfficial reports whether an instance of repo uses cicord/<name> through cicord.lock.
func lockedOfficial(repo *config.Repo, name string) bool {
	if repo == nil {
		return false
	}
	for _, i := range repo.Instances {
		if i.Plugin != nil && i.Plugin.Source == config.OfficialRegistryName+"/"+name {
			return true
		}
	}
	return false
}

// checkSigned refuses an index read from an official URL that carries no verified
// signature (defense in depth: registry.FetchIndex already refuses it, but Fetch may be
// replaced).
func checkSigned(url string, idx *registry.Index, err error) (*registry.Index, error) {
	if err != nil || registry.Trust(url) != registry.TrustOfficial {
		return idx, err
	}
	if idx == nil || idx.SignedBy == "" {
		return nil, fmt.Errorf("%s: %w", registry.Redact(url), registry.ErrBadSignature)
	}
	return idx, nil
}

// signatureOf is Source.Signature for a fetched registry.
func signatureOf(url string, c cached) string {
	if registry.Trust(url) != registry.TrustOfficial {
		return ""
	}
	if c.err == nil && c.idx != nil && c.idx.SignedBy != "" {
		return SignatureVerified
	}
	if errors.Is(c.err, registry.ErrNoOfficialKey) {
		return "" // nothing was read: not an invalid signature (Source.Error explains)
	}
	if errors.Is(c.err, registry.ErrBadSignature) {
		return SignatureInvalid
	}
	return ""
}

// mergeOfficial adds the plugins of the official remote index to source "cicord".
func mergeOfficial(src *Source, off config.Registry, c cached, inst map[string][]Installation) {
	src.URL = registry.Redact(off.URL)
	at := c.at
	src.FetchedAt = &at
	src.Signature = signatureOf(off.URL, c)
	if c.err != nil {
		src.Error = ExplainFetch(c.err)
		return
	}
	byName := map[string]int{}
	for i, p := range src.Plugins {
		byName[p.Name] = i
	}
	for n, e := range c.idx.Plugins {
		if !registry.NameRe.MatchString(n) {
			continue
		}
		i, bundled := byName[n]
		bv := ""
		if bundled {
			bv = src.Plugins[i].Version
		}
		if !remoteWins(bv, bundled, e) {
			continue
		}
		p := fromIndex(off, n, e, inst[off.Name+"/"+n], c.idx.SignedBy != "")
		p.Version = e.Version
		if bundled {
			src.Plugins[i] = p
		} else {
			src.Plugins = append(src.Plugins, p)
		}
	}
	slices.SortFunc(src.Plugins, func(a, b Plugin) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
}
