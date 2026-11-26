// Package digest computes perceptual image hashes. It runs inside the confined
// media worker (internal/media/worker), never in the runtime process: a crafted
// image can make a decoder allocate a lot of memory or spend minutes of CPU, and
// only the worker has a hard memory ceiling and a deadline that kills it.
package digest

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // GIF decoder: the first frame is hashed
	_ "image/jpeg" // JPEG decoder
	_ "image/png"  // PNG decoder
	"io"
	"math"
	"slices"

	_ "golang.org/x/image/webp" // WebP decoder
)

// MaxPixels is the decoded pixel budget of a hashed image.
const MaxPixels = 4 << 20

// CheckConfig verifies the header of an image without decoding its pixels. It is
// cheap and bounded, so the host also runs it before it starts a worker.
func CheckConfig(cfg image.Config, format string) error {
	if format != "png" && format != "jpeg" && format != "webp" && format != "gif" {
		return errors.New("expected a PNG, JPEG, GIF or WebP image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return errors.New("image exceeds decoded pixel budget")
	}
	return nil
}

// Perceptual decodes an image from r and returns its 64-bit "dhash" or "phash" as
// 16 hexadecimal digits. The reader is streamed, so the encoded bytes never have
// to be held next to the decoded pixels.
func Perceptual(r io.ReadSeeker, algo string) (string, error) {
	if algo != "dhash" && algo != "phash" {
		return "", errors.New("unknown media hash algorithm")
	}
	cfg, format, err := image.DecodeConfig(bufio.NewReaderSize(r, 32<<10))
	if err != nil {
		return "", errors.New("expected a PNG, JPEG, GIF or WebP image")
	}
	if err := CheckConfig(cfg, format); err != nil {
		return "", err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	img, _, err := image.Decode(bufio.NewReaderSize(r, 32<<10))
	if err != nil {
		return "", err
	}
	var h uint64
	switch algo {
	case "dhash":
		a := sample(img, 9, 8)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				h <<= 1
				if a[y*9+x] > a[y*9+x+1] {
					h |= 1
				}
			}
		}
	case "phash":
		a := sample(img, 32, 32)
		var rows [32][8]float64
		var coef [64]float64
		for y := range rows {
			for u := range 8 {
				for x := range 32 {
					rows[y][u] += a[y*32+x] * cosine[u][x]
				}
			}
		}
		for v := range 8 {
			for u := range 8 {
				for y := range 32 {
					coef[v*8+u] += rows[y][u] * cosine[v][y]
				}
				if math.Abs(coef[v*8+u]) < 1e-7 {
					coef[v*8+u] = 0
				}
			}
		}
		sorted := coef
		slices.Sort(sorted[1:])
		median := sorted[32]
		for _, c := range coef {
			h <<= 1
			if c > median {
				h |= 1
			}
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

var cosine = func() (out [8][32]float64) {
	for u := range out {
		for x := range out[u] {
			out[u][x] = math.Cos(math.Pi * float64((2*x+1)*u) / 64)
		}
	}
	return
}()

// Average a regular 4×4 grid inside each output cell. Fixed sampling bounds the work
// independently of image size and smooths JPEG artifacts and resizing differences.
func sample(img image.Image, w, h int) []float64 {
	r := img.Bounds()
	out := make([]float64, w*h)
	for y := range h {
		for x := range w {
			var sum int
			for j := range 4 {
				py := r.Min.Y + (8*y+2*j+1)*r.Dy()/(8*h)
				for i := range 4 {
					px := r.Min.X + (8*x+2*i+1)*r.Dx()/(8*w)
					sum += int(color.GrayModel.Convert(img.At(px, py)).(color.Gray).Y)
				}
			}
			out[y*w+x] = float64(sum) / 16
		}
	}
	return out
}
