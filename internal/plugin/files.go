package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/config"
)

func opFile(method string) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require("files.read"); err != nil {
			return nil, err
		}
		a, err := decode[struct {
			Path, Glob string
			Offset     int
		}](args)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			if method == "list" {
				if a.Glob != "" {
					if _, err := config.MatchFileGlob(a.Glob, ""); err != nil {
						return nil, err
					}
				}
				names := []string{}
				for name := range i.env.Files {
					if a.Glob != "" {
						ok, _ := config.MatchFileGlob(a.Glob, name)
						if !ok {
							continue
						}
					}
					names = append(names, name)
				}
				sort.Strings(names)
				return names, nil
			}
			data, ok := i.env.Files[a.Path]
			if !ok {
				return nil, errors.New("undeclared instance file")
			}
			if method == "bytes" {
				if a.Offset < 0 || a.Offset > len(data) {
					return nil, errors.New("invalid file offset")
				}
				const chunk = 32 << 10
				return map[string]any{"size": len(data), "data": data[a.Offset:min(len(data), a.Offset+chunk)]}, nil
			}
			if !utf8.Valid(data) {
				return nil, errors.New("file is not UTF-8")
			}
			// The string copy and its JSON encoding coexist until json.Marshal returns.
			if err := reserveResult(ctx, 2*jsonStringLen(data)); err != nil {
				return nil, fmt.Errorf("files.read %q: %w; read it in pages with files.bytes", a.Path, err)
			}
			return string(data), nil
		}, nil
	}
}
