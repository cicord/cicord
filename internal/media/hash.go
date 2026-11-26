package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"math/bits"
	"slices"

	_ "golang.org/x/image/webp"
)

const MaxBytes = 8 << 20

func Distance(a, b string) (int, error) {
	if len(a) != len(b) {
		return 0, errors.New("distance requires equal-length hexadecimal digests")
	}
	x, err := hex.DecodeString(a)
	if err != nil {
		return 0, err
	}
	y, err := hex.DecodeString(b)
	if err != nil {
		return 0, err
	}
	d := 0
	for i, h := range x {
		d += bits.OnesCount8(h ^ y[i])
	}
	return d, nil
}

func Hash(data []byte, algo string) (string, error) {
	if len(data) > MaxBytes {
		return "", errors.New("image larger than 8 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp" && format != "gif") {
		return "", errors.New("expected a PNG, JPEG, GIF or WebP image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 4<<20 {
		return "", errors.New("image exceeds decoded pixel budget")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var h uint64
	switch algo {
	case "sha256":
		s := sha256.Sum256(data)
		return hex.EncodeToString(s[:]), nil
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
	default:
		return "", errors.New("unknown media hash algorithm")
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
