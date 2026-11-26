package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cicord/cicord/internal/config"
)

// Privacy runs the plugin's inventory-aware export or erase hook. Return values
// cross the same size-bounded boundary as portal route results.
func (i *Instance) Privacy(ctx context.Context, action, user string) (json.RawMessage, error) {
	if action != "export" && action != "erase" {
		return nil, errors.New("invalid privacy operation")
	}
	if d := i.Definition(); d == nil || !d.HasPrivacy {
		return nil, ErrNoHandler
	}
	result := <-i.dispatch(ctx, "privacy", action, user, nil)
	if result.err != nil {
		return nil, result.err
	}
	raw := json.RawMessage(result.data)
	if len(raw) > maxRouteResponse || !json.Valid(raw) {
		return nil, errors.New("invalid privacy hook result")
	}
	if action == "erase" {
		var v struct {
			Erased *int `json:"erased"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Erased == nil || *v.Erased < 0 {
			return nil, errors.New("erase must return a nonnegative erased count")
		}
	}
	return raw, nil
}

func opPrivacyPseudonym(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	user, err := decode[string](args)
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.kind != "privacy" || user != inv.privacySubject {
		return nil, errors.New("privacy.pseudonym is scoped to the member of the current privacy hook")
	}
	if i.env.Pseudonym == nil || user == "" || len(user) > 128 {
		return nil, fmt.Errorf("privacy: invalid member or unavailable pseudonym service")
	}
	return func(context.Context) (any, error) { return i.env.Pseudonym(user), nil }, nil
}

// CheckInventory binds static privacy declarations to both executable hooks.
func (d *Definition) CheckInventory(in *config.PrivacyInventory) error {
	if d.HasPrivacy && (in == nil || len(in.Records) == 0) {
		return errors.New("privacy hooks must declare their member records in spec.privacy.records")
	}
	if !d.HasPrivacy && in != nil && len(in.Records) > 0 {
		return errors.New("spec.privacy.records requires privacy.export and privacy.erase hooks")
	}
	seen := map[string]bool{}
	if in != nil {
		for _, r := range in.Records {
			if seen[r.Name] {
				return fmt.Errorf("duplicate privacy record %q", r.Name)
			}
			seen[r.Name] = true
		}
	}
	return nil
}
