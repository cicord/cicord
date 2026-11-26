package paint

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

func Color(s string) (color.NRGBA, error) {
	h, ok := strings.CutPrefix(s, "#")
	if !ok || len(h) != 6 && len(h) != 8 {
		return color.NRGBA{}, fmt.Errorf("expected #RRGGBB or #RRGGBBAA, got %q", s)
	}
	channels, err := hex.DecodeString(h)
	if err != nil {
		return color.NRGBA{}, err
	}
	if len(channels) == 3 {
		channels = append(channels, 255)
	}
	return color.NRGBA{R: channels[0], G: channels[1], B: channels[2], A: channels[3]}, nil
}
func blend(a, b uint8, t float64) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
func fill(dst *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(dst, r, image.NewUniform(c), image.Point{}, draw.Over)
}

// sources decodes each input once per render, however many layers use it, and
// charges every distinct image to one pixel budget.
type sources struct {
	read   func(string) ([]byte, error)
	images map[string]image.Image
	fonts  map[string]*opentype.Font
	pixels int64
}

func (s *sources) image(key string) (image.Image, error) {
	if img, ok := s.images[key]; ok {
		return img, nil
	}
	b, err := s.read(key)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 || s.pixels+px > MaxSourcePixels {
		return nil, fmt.Errorf("image sources exceed %d decoded pixels in total (%d×%d)", MaxSourcePixels, cfg.Width, cfg.Height)
	}
	s.pixels += px
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	s.images[key] = img
	return img, nil
}

func (s *sources) font(key string) (*opentype.Font, error) {
	if f, ok := s.fonts[key]; ok {
		return f, nil
	}
	b, err := s.read(key)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(b)
	if err != nil {
		return nil, err
	}
	s.fonts[key] = f
	return f, nil
}

func Draw(spec Spec, readInput func(string) ([]byte, error), out io.Writer) error {
	if _, err := spec.Inputs(); err != nil {
		return err
	}
	read := &sources{read: readInput, images: map[string]image.Image{}, fonts: map[string]*opentype.Font{}}
	dst := image.NewRGBA(image.Rect(0, 0, spec.Width, spec.Height))
	bg := spec.Background
	switch {
	case bg.Source != (Source{}):
		if err := picture(dst, dst.Bounds(), Layer{Source: bg.Source}, read); err != nil {
			return err
		}
	case len(bg.Gradient) > 0:
		a, err := Color(bg.Gradient[0])
		if err != nil {
			return err
		}
		b, err := Color(bg.Gradient[1])
		if err != nil {
			return err
		}
		for y := 0; y < spec.Height; y++ {
			t := float64(y) / float64(max(1, spec.Height-1))
			c := color.NRGBA{R: blend(a.R, b.R, t), G: blend(a.G, b.G, t), B: blend(a.B, b.B, t), A: blend(a.A, b.A, t)}
			fill(dst, image.Rect(0, y, spec.Width, y+1), c)
		}
	case bg.Color != "":
		c, err := Color(bg.Color)
		if err != nil {
			return err
		}
		fill(dst, dst.Bounds(), c)
	}
	for _, l := range spec.Layers {
		r := image.Rect(l.X, l.Y, l.X+l.Width, l.Y+l.Height)
		if l.Type == "image" {
			if err := picture(dst, r, l, read); err != nil {
				return err
			}
			continue
		}
		c, err := Color(l.Color)
		if err != nil {
			return err
		}
		switch l.Type {
		case "text":
			if err := label(dst, l, c, read); err != nil {
				return err
			}
		case "rect":
			fill(dst, r, c)
		case "progress":
			if l.TrackColor != "" {
				t, err := Color(l.TrackColor)
				if err != nil {
					return err
				}
				fill(dst, r, t)
			}
			r.Max.X = r.Min.X + int(math.Round(float64(l.Width)*l.Value))
			fill(dst, r, c)
		case "chart":
			chart(dst, r, l, c)
		}
	}
	return encodePNG(out, dst)
}
func picture(dst *image.RGBA, r image.Rectangle, l Layer, read *sources) error {
	key, err := l.Source.Key()
	if err != nil {
		return err
	}
	src, err := read.image(key)
	if err != nil {
		return err
	}
	cfg := image.Config{Width: src.Bounds().Dx(), Height: src.Bounds().Dy()}
	w, h := r.Dx(), r.Dy()
	radius := l.Radius
	switch l.Mask {
	case "":
	case "rounded":
		if radius == 0 {
			radius = min(w, h) / 6
		}
		if radius < 0 || radius > min(w, h)/2 {
			return errors.New("invalid corner radius")
		}
	case "circle":
		radius = min(w, h) / 2
	default:
		return errors.New("unknown image mask")
	}
	fast, _ := src.(image.RGBA64Image)
	clipped := r.Intersect(dst.Bounds())
	bounds := src.Bounds()
	// Source columns depend only on x: compute them once instead of per pixel.
	columns := make([]int, clipped.Dx())
	for x := range columns {
		columns[x] = bounds.Min.X + (clipped.Min.X+x-r.Min.X)*cfg.Width/w
	}
	circle, rounded := l.Mask == "circle", l.Mask == "rounded"
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		py := y - r.Min.Y
		sy := bounds.Min.Y + py*cfg.Height/h
		row := dst.PixOffset(clipped.Min.X, y)
		for n, sx := range columns {
			px := clipped.Min.X + n - r.Min.X
			if circle {
				dx, dy := float64(px)+0.5-float64(w)/2, float64(py)+0.5-float64(h)/2
				if dx*dx+dy*dy > float64(radius*radius) {
					continue
				}
			}
			if rounded {
				dx, dy := max(radius-px-1, px-(w-radius), 0), max(radius-py-1, py-(h-radius), 0)
				if dx*dx+dy*dy > radius*radius {
					continue
				}
			}
			var sr, sg, sb, sa uint32
			if fast != nil {
				// RGBA64At returns a struct: At would box one color per pixel, and the
				// allocations dominated the render time inside the worker.
				c := fast.RGBA64At(sx, sy)
				sr, sg, sb, sa = uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
			} else {
				sr, sg, sb, sa = src.At(sx, sy).RGBA()
			}
			p := dst.Pix[row+4*n : row+4*n+4]
			inv := uint32(65535) - sa
			p[0] = overChannel(sr, p[0], inv)
			p[1] = overChannel(sg, p[1], inv)
			p[2] = overChannel(sb, p[2], inv)
			p[3] = overChannel(sa, p[3], inv)
		}
	}
	return nil
}

func overChannel(src uint32, dst uint8, inverseAlpha uint32) uint8 {
	// src is a premultiplied 16-bit channel and inverseAlpha is 0..65535.
	return uint8((src>>8 + uint32(dst)*inverseAlpha/65535) & 255)
}

func label(dst *image.RGBA, l Layer, c color.Color, read *sources) error {
	key, err := l.Font.Key()
	if err != nil {
		return err
	}
	f, err := read.font(key)
	if err != nil {
		return err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(l.Size), DPI: 72})
	if err != nil {
		return err
	}
	defer face.Close()
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face}
	s := l.Text
	w := d.MeasureString(s).Ceil()
	if l.MaxWidth > 0 && w > l.MaxWidth {
		suffix := "…"
		if l.Ellipsis != nil && !*l.Ellipsis {
			suffix = ""
		}
		lim := fixed.I(l.MaxWidth) - d.MeasureString(suffix)
		end := 0
		var width fixed.Int26_6
		var prev rune
		for j, cur := range s {
			advance, _ := face.GlyphAdvance(cur)
			if j > 0 {
				width += face.Kern(prev, cur)
			}
			width += advance
			if width > lim {
				break
			}
			end = j + utf8.RuneLen(cur)
			prev = cur
		}
		if lim < 0 {
			return nil
		}
		s = s[:end] + suffix
		w = d.MeasureString(s).Ceil()
	}
	x := l.X
	switch l.Align {
	case "", "left":
	case "center":
		x -= w / 2
	case "right":
		x -= w
	default:
		return errors.New("unknown text alignment")
	}
	d.Dot = fixed.P(x, l.Y+face.Metrics().Ascent.Ceil())
	d.DrawString(s)
	return nil
}

func chart(dst *image.RGBA, r image.Rectangle, l Layer, c color.Color) {
	// Normalize before subtraction to keep even the largest finite JSON numbers in range.
	scale := 1.0
	for _, v := range l.Values {
		scale = max(scale, math.Abs(v))
	}
	lo, hi := 0.0, 0.0
	for _, v := range l.Values {
		lo = min(lo, v/scale)
		hi = max(hi, v/scale)
	}
	if lo == hi {
		hi = 1
	}
	height := r.Dy() - 1
	pointY := func(v float64) int { return r.Max.Y - 1 - int(math.Round((v/scale-lo)/(hi-lo)*float64(height))) }
	var prev image.Point
	n := len(l.Values)
	for j, v := range l.Values {
		y := pointY(v)
		if l.Kind == "bar" {
			x := r.Min.X + j*r.Dx()/n
			right := r.Min.X + (j+1)*r.Dx()/n
			fill(dst, image.Rect(x, min(y, pointY(0)), max(x+1, right-1), max(y, pointY(0))+1), c)
			continue
		}
		p := image.Pt(r.Min.X+j*(r.Dx()-1)/max(n-1, 1), y)
		if j == 0 {
			dst.Set(p.X, p.Y, c)
		} else {
			dx, dy := p.X-prev.X, p.Y-prev.Y
			steps := max(dx, int(math.Abs(float64(dy))), 1)
			for k := 0; k <= steps; k++ {
				dst.Set(prev.X+k*dx/steps, prev.Y+k*dy/steps, c)
			}
		}
		prev = p
	}
}
