package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/cicord/cicord/internal/media"
	"github.com/cicord/cicord/internal/media/paint"
)

var cdnHosts = []string{"cdn.discordapp.com", "media.discordapp.net"}

func (i *Instance) downloadMedia(ctx context.Context, raw string, cdnOnly bool) ([]byte, error) {
	if len(raw) > 8192 {
		return nil, argErr("media URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, argErr("invalid media URL")
	}
	cdn := newEgress(cdnHosts, false, i.env.HTTPClient, media.MaxBytes)
	var e *egress
	switch {
	case cdn.checkURL(u) == nil:
		if sim, ok := i.env.Discord.(interface {
			DownloadCDN(context.Context, string, int64) ([]byte, error)
		}); ok {
			return sim.DownloadCDN(ctx, u.String(), media.MaxBytes)
		}
		e = cdn
	case cdnOnly:
		return nil, fmt.Errorf("%w: expected an HTTPS Discord CDN URL", ErrEgressDenied)
	default:
		policy := *i.egress
		policy.allow = append(append([]allowEntry{}, policy.allow...), cdn.allow...)
		policy.maxBytes = media.MaxBytes
		e = &policy
	}
	ctx, cancel := context.WithTimeout(ctx, i.limits.FetchTimeout)
	defer cancel()
	res, err := e.do(ctx, "GET", u, nil, nil, false)
	if err != nil {
		return nil, err
	}
	if res.Status < 200 || res.Status >= 300 {
		return nil, fmt.Errorf("media download returned HTTP %d", res.Status)
	}
	return res.Body, nil
}
func (i *Instance) mediaWork(ctx context.Context, f func() (any, error)) (any, error) {
	select {
	case i.mediaSlots <- struct{}{}:
		defer func() { <-i.mediaSlots }()
		return f()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func opMediaRead(capability string) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require(capability); err != nil {
			return nil, err
		}
		a, err := decode[struct{ URL, Algo string }](args)
		if err != nil {
			return nil, err
		}
		hash := capability == "media.analyze"
		if hash && a.Algo != "sha256" && a.Algo != "phash" && a.Algo != "dhash" {
			return nil, argErr("unknown hash algorithm")
		}
		return func(ctx context.Context) (any, error) {
			return i.mediaWork(ctx, func() (any, error) {
				release, err := media.Acquire(ctx)
				if err != nil {
					return nil, err
				}
				defer release()
				b, err := i.downloadMedia(ctx, a.URL, !hash)
				if err != nil {
					return nil, err
				}
				if !hash {
					return b, nil
				}
				return media.Hash(b, a.Algo)
			})
		}, nil
	}
}
func opMediaDistance(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	if err := i.require("media.analyze"); err != nil {
		return nil, err
	}
	a, err := decode[struct{ A, B string }](args)
	if err != nil {
		return nil, err
	}
	return media.Distance(a.A, a.B)
}
func opRenderImage(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require("render"); err != nil {
		return nil, err
	}
	if len(args) > 64<<10 {
		return nil, argErr("image specification exceeds 64 KiB")
	}
	s, err := decode[paint.Spec](args)
	if err != nil {
		return nil, err
	}
	inputs, err := s.Inputs()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		return i.mediaWork(ctx, func() (any, error) {
			return media.Render(ctx, args, inputs, func(ctx context.Context, key string) ([]byte, error) {
				if p, ok := strings.CutPrefix(key, "asset:"); ok {
					return i.readAsset(p)
				}
				if u, ok := strings.CutPrefix(key, "url:"); ok {
					return i.downloadMedia(ctx, u, true)
				}
				return nil, errors.New("invalid render source")
			})
		})
	}, nil
}
