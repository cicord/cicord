package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cicord/cicord/internal/state"
)

func opIndex(method string) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require("storage.index"); err != nil {
			return nil, err
		}
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
		if err := state.ValidateIndex(a.Name, a.Language); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			switch method {
			case "upsert":
				return nil, i.env.Index.Upsert(ctx, a.Name, a.Language, a.ID, a.Document)
			case "search":
				return i.env.Index.Search(ctx, a.Name, a.Language, a.Query, a.Options)
			case "remove":
				return nil, i.env.Index.Remove(ctx, a.Name, a.ID)
			default:
				return nil, i.env.Index.Clear(ctx, a.Name)
			}
		}, nil
	}
}
