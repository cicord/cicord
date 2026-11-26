package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cicord/cicord/internal/state"
)

func opIndex(method string) asyncOp {
	return func(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if i.env.Index == nil {
			return nil, errors.New("full-text storage unavailable")
		}
		a, err := decode[struct {
			Name, Language, ID, Query string
			Document                  state.IndexDocument
			Options                   state.IndexQuery
		}](args)
		if err != nil {
			return nil, err
		}
		if err := i.require("storage.index"); err != nil {
			// A revoked grant must not strand personal data. During an administrator's
			// erase hook, the backend verifies the exact member binding; every other
			// index operation still requires the ordinary capability.
			limited, ok := i.env.Index.(state.SubjectIndex)
			if !ok || method != "remove" || inv == nil || inv.kind != "privacy" || inv.name != "erase" || inv.privacySubject == "" || a.ID == "" {
				return nil, err
			}
			if err := state.ValidateIndex(a.Name, a.Language); err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				return nil, limited.EraseSubject(ctx, inv.privacySubject, a.Name, a.ID)
			}, nil
		}
		if err := state.ValidateIndex(a.Name, a.Language); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			switch method {
			case "upsert":
				return nil, i.env.Index.Upsert(ctx, a.Name, a.Language, a.ID, a.Document)
			case "search":
				// Each result carries up to 256 KiB of meta and a short snippet.
				limit := int64(a.Options.Limit)
				if limit <= 0 {
					limit = 10
				}
				if err := reserveResult(ctx, 2*min(limit, 50)*(264<<10)); err != nil {
					return nil, err
				}
				return i.env.Index.Search(ctx, a.Name, a.Language, a.Query, a.Options)
			case "remove":
				return nil, i.env.Index.Remove(ctx, a.Name, a.ID)
			default:
				return nil, i.env.Index.Clear(ctx, a.Name)
			}
		}, nil
	}
}
