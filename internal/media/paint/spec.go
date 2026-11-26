// Package paint implements Cicord's declarative card renderer.
package paint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

type Source struct {
	Asset string `json:"asset,omitempty"`
	URL   string `json:"url,omitempty"`
}

func (s Source) Key() (string, error) {
	if s.Asset != "" && s.URL == "" {
		return "asset:" + s.Asset, nil
	}
	if s.URL != "" && s.Asset == "" {
		return "url:" + s.URL, nil
	}
	return "", errors.New("image source must specify an asset or a Discord CDN URL")
}

type Background struct {
	Color    string
	Gradient []string `json:"gradient,omitempty"`
	Source
}

func (b *Background) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		return json.Unmarshal(raw, &b.Color)
	}
	type plain Background
	return json.Unmarshal(raw, (*plain)(b))
}

type Font struct {
	Builtin string
	Asset   string
}

func (f *Font) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &f.Builtin)
	}
	type plain Font
	return json.Unmarshal(b, (*plain)(f))
}
func (f Font) Key() (string, error) {
	if f.Asset != "" {
		return "asset:" + f.Asset, nil
	}
	switch f.Builtin {
	case "", "Inter":
		return "font:Inter", nil
	case "Noto", "emoji":
		return "font:" + f.Builtin, nil
	}
	return "", errors.New("unknown built-in font")
}

type Layer struct {
	Type, Text, Color, Align, Mask, TrackColor, Kind string
	X, Y, Width, Height, Size, MaxWidth, Radius      int
	Ellipsis                                         *bool
	Value                                            float64
	Values                                           []float64
	Source                                           Source
	Font                                             Font
}
type Spec struct {
	Width, Height int
	Background    Background
	Layers        []Layer
}

// Render envelope. One RGBA canvas pixel is 4 bytes, and the canvas, the decoded
// sources and the PNG encoder share the worker's 16 MiB: a 2048×2048 canvas alone
// would fill it. Sides stay up to 2048 for banners, the area is capped.
const (
	MaxSide         = 2048
	MaxCanvasPixels = 1 << 19 // e.g. 1024×512, 724×724, 2048×256
	MaxSourcePixels = 1 << 19 // all distinct image sources of one render, decoded once each
)

func Path(key string) string { sum := sha256.Sum256([]byte(key)); return hex.EncodeToString(sum[:]) }

// Inputs verifies size/count bounds before the host resolves any external resources.
func (s Spec) Inputs() ([]string, error) {
	if s.Width < 1 || s.Height < 1 || s.Width > MaxSide || s.Height > MaxSide || len(s.Layers) > 64 {
		return nil, errors.New("image size must be 1..2048, with at most 64 layers")
	}
	if s.Width*s.Height > MaxCanvasPixels {
		return nil, fmt.Errorf("image area %d×%d exceeds %d pixels (for example 1024×512 or 2048×256)", s.Width, s.Height, MaxCanvasPixels)
	}
	var out []string
	seen := map[string]bool{}
	add := func(k string, err error) error {
		if err == nil && !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
		return err
	}
	if s.Background.Source != (Source{}) {
		if err := add(s.Background.Key()); err != nil {
			return nil, err
		}
	}
	if g := s.Background.Gradient; g != nil && len(g) != 2 {
		return nil, errors.New("gradient needs exactly two colors")
	}
	for _, l := range s.Layers {
		if l.X < -2048 || l.X > 2048 || l.Y < -2048 || l.Y > 2048 {
			return nil, errors.New("layer coordinates outside bounds")
		}
		if l.Type == "text" {
			if l.Size < 1 || l.Size > 256 || len(l.Text) > 4096 || l.MaxWidth < 0 || l.MaxWidth > 2048 {
				return nil, errors.New("invalid text size, width or length")
			}
			if err := add(l.Font.Key()); err != nil {
				return nil, err
			}
			continue
		}
		if l.Width < 1 || l.Height < 1 || l.Width > 2048 || l.Height > 2048 {
			return nil, errors.New("invalid layer dimensions")
		}
		switch l.Type {
		case "image":
			if err := add(l.Source.Key()); err != nil {
				return nil, err
			}
		case "progress":
			if l.Value < 0 || l.Value > 1 {
				return nil, errors.New("progress value must be 0..1")
			}
		case "chart":
			if len(l.Values) == 0 || len(l.Values) > 256 || l.Kind != "bar" && l.Kind != "line" {
				return nil, errors.New("chart requires bar/line and 1..256 values")
			}
		case "rect":
		default:
			return nil, fmt.Errorf("unknown layer type %q", l.Type)
		}
	}
	return out, nil
}
