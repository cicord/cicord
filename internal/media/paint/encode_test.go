package paint

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// The fixed-filter encoder must round-trip exactly what the standard decoder reads
// back from the standard encoder, opaque or translucent.
func TestEncodePNGMatchesStandardEncoder(t *testing.T) {
	for _, alpha := range []uint8{255, 0, 1, 128, 254} {
		dst := image.NewRGBA(image.Rect(0, 0, 67, 9))
		for y := range 9 {
			for x := range 67 {
				a := alpha
				if x%3 == 0 {
					a = uint8(x * 3)
				}
				if alpha == 255 {
					a = 255
				}
				c := color.NRGBA{R: uint8(x * 7), G: uint8(y * 29), B: uint8(x ^ y), A: a}
				dst.Set(x, y, c)
			}
		}
		var mine, std bytes.Buffer
		if err := encodePNG(&mine, dst); err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(&std, dst); err != nil {
			t.Fatal(err)
		}
		a, err := png.Decode(&mine)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := png.Decode(&std)
		for y := range 9 {
			for x := range 67 {
				if ca, cb := color.NRGBAModel.Convert(a.At(x, y)), color.NRGBAModel.Convert(b.At(x, y)); ca != cb {
					t.Fatalf("alpha %d pixel (%d,%d): %v, standard %v", alpha, x, y, ca, cb)
				}
			}
		}
	}
}
