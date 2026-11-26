package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/cicord/cicord/internal/media/paint"
)

func renderTest(t *testing.T, raw string, assets map[string][]byte) ([]byte, error) {
	t.Helper()
	var s paint.Spec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	in, err := s.Inputs()
	if err != nil {
		return nil, err
	}
	return Render(context.Background(), []byte(raw), in, func(_ context.Context, key string) ([]byte, error) {
		v, ok := assets[key]
		if !ok {
			return nil, fmt.Errorf("missing %s", key)
		}
		return v, nil
	})
}
func TestRenderAllLayers(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, src); err != nil {
		t.Fatal(err)
	}
	raw := `{"width":512,"height":224,"background":{"gradient":["#000000","#ffffff"]},"layers":[{"type":"image","x":5,"y":8,"width":60,"height":60,"source":{"asset":"avatar.png"},"mask":"circle"},{"type":"text","x":80,"y":20,"text":"Welcome to Cicord","font":"Inter","size":25,"color":"#aaaaaa","maxWidth":80},{"type":"rect","x":100,"y":5,"width":10,"height":6,"color":"#00ff00"},{"type":"progress","x":10,"y":90,"width":100,"height":10,"color":"#0000ff","value":0.5,"trackColor":"#ff0000"},{"type":"chart","kind":"bar","x":10,"y":130,"width":100,"height":30,"color":"#00ff00","values":[4,9,-5]},{"type":"chart","kind":"line","x":140,"y":130,"width":100,"height":30,"color":"#00ff00","values":[4,9,5]}]}`
	b, err := renderTest(t, raw, map[string][]byte{"asset:avatar.png": imageData.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		x, y int
		c    color.RGBA
	}{{30, 30, color.RGBA{R: 255, A: 255}}, {100, 5, color.RGBA{G: 255, A: 255}}, {12, 95, color.RGBA{B: 255, A: 255}}, {109, 95, color.RGBA{R: 255, A: 255}}} {
		if got := color.RGBAModel.Convert(img.At(v.x, v.y)); got != v.c {
			t.Errorf("pixel (%d,%d)=%v, want %v", v.x, v.y, got, v.c)
		}
	}
	for _, font := range []string{"Noto", "emoji"} {
		_, err := renderTest(t, fmt.Sprintf(`{"width":200,"height":70,"layers":[{"type":"text","x":1,"y":1,"text":"Hello ☺","size":22,"color":"#000000","font":%q}]}`, font), nil)
		if err != nil {
			t.Errorf("%s: %v", font, err)
		}
	}
}
func TestPerceptualReencoding(t *testing.T) {
	gen := func(size int) *image.RGBA {
		im := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := range size {
			for x := range size {
				u, v := float64(x)/float64(size), float64(y)/float64(size)
				c := color.RGBA{R: uint8(200 * u), G: uint8(210 * v), B: 130, A: 255}
				if (u-.3)*(u-.3)+(v-.4)*(v-.4) < .03 {
					c = color.RGBA{R: 240, G: 30, B: 45, A: 255}
				}
				if u > .55 && u < .92 && v > .65 && v < .8 {
					c = color.RGBA{G: 240, A: 255}
				}
				im.SetRGBA(x, y, c)
			}
		}
		return im
	}
	var a, b bytes.Buffer
	_ = png.Encode(&a, gen(320))
	_ = jpeg.Encode(&b, gen(144), &jpeg.Options{Quality: 70})
	for _, algo := range []string{"dhash", "phash"} {
		h, err := Hash(a.Bytes(), algo)
		if err != nil {
			t.Fatal(err)
		}
		j, err := Hash(b.Bytes(), algo)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Distance(h, j)
		t.Logf("%s %s %s distance=%d", algo, h, j, d)
		if err != nil || d > 12 {
			t.Errorf("%s %s %s distance %d: %v", algo, h, j, d, err)
		}
	}
	if _, err := Hash([]byte("not an image"), "sha256"); err == nil {
		t.Fatal("hashed non-image")
	}
	if _, err := Distance("bad", "bad"); err == nil {
		t.Fatal("accepted invalid hashes")
	}
}

func TestRenderMemoryBudgetAndRecovery(t *testing.T) {
	// The 2048-square RGBA canvas consumes 16 MiB by itself. The Go worker's
	// heap and encoder must fit inside the same ceiling, so this render fails.
	if _, err := renderTest(t, `{"width":2048,"height":2048,"layers":[{"type":"rect","x":0,"y":0,"width":2000,"height":2000,"color":"#123456"}]}`, nil); err == nil {
		t.Fatal("renderer exceeded the memory budget")
	}
	if _, err := renderTest(t, `{"width":32,"height":32,"background":"#123456"}`, nil); err != nil {
		t.Fatalf("render after exhausted worker: %v", err)
	}
}
