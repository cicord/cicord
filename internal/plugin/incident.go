package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
)

// Strict decoding is intentional: e.g. an unrecognized allow in an otherwise valid
// overwrite must be rejected rather than quietly discarded by json.Unmarshal.
func decodeRestriction[T any](raw json.RawMessage) (T, error) {
	var v T
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, argErr("invalid restriction: %v", err)
	}
	return v, nil
}
func opOverlayApply(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("server.overlays"); err != nil {
		return nil, err
	}
	v, err := decodeRestriction[reconcile.Overlay](args)
	if err != nil {
		return nil, err
	}
	if err = v.Validate(time.Now()); err != nil {
		return nil, argErr("%s", err)
	}
	if i.env.OverlayApply == nil {
		return nil, errors.New("overlays unavailable")
	}
	return func(c context.Context) (any, error) { return nil, i.env.OverlayApply(c, v) }, nil
}
func opOverlayRemove(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("server.overlays"); err != nil {
		return nil, err
	}
	v, err := decode[struct{ ID string }](args)
	if err != nil {
		return nil, err
	}
	if i.env.OverlayRemove == nil {
		return nil, errors.New("overlays unavailable")
	}
	return func(c context.Context) (any, error) { return nil, i.env.OverlayRemove(c, v.ID) }, nil
}
func opOverlayList(i *Instance, _ *invocation, _ json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("server.overlays"); err != nil {
		return nil, err
	}
	if i.env.OverlayList == nil {
		return nil, errors.New("overlays unavailable")
	}
	return func(c context.Context) (any, error) { return i.env.OverlayList(c) }, nil
}
func opIncident(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("server.incident"); err != nil {
		return nil, err
	}
	actions, err := decodeRestriction[discord.IncidentActions](args)
	if err != nil {
		return nil, err
	}
	if actions.InvitesDisabledUntil == nil && actions.DMsDisabledUntil == nil {
		return nil, argErr("an incident end time is required")
	}
	now := time.Now()
	for _, end := range []*time.Time{actions.InvitesDisabledUntil, actions.DMsDisabledUntil} {
		if end != nil && (!end.After(now) || end.After(now.Add(24*time.Hour))) {
			return nil, argErr("incident actions must end within 24 hours")
		}
	}
	if i.env.Incident == nil {
		return nil, errors.New("incident actions unavailable")
	}
	return func(c context.Context) (any, error) { return nil, i.env.Incident(c, actions) }, nil
}
func opRequestSync(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("server.sync"); err != nil {
		return nil, err
	}
	v, err := decode[struct{ Reason string }](args)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(v.Reason) == "" || len(v.Reason) > 512 {
		return nil, argErr("sync requires a reason up to 512 bytes")
	}
	if i.env.RequestSync == nil {
		return nil, errors.New("server sync unavailable")
	}
	return func(c context.Context) (any, error) { return nil, i.env.RequestSync(c, v.Reason) }, nil
}
