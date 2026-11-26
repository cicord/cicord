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
	"strings"
	"testing"
	"time"

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
		h, err := Hash(t.Context(), a.Bytes(), algo)
		if err != nil {
			t.Fatal(err)
		}
		j, err := Hash(t.Context(), b.Bytes(), algo)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Distance(h, j)
		t.Logf("%s %s %s distance=%d", algo, h, j, d)
		if err != nil || d > 12 {
			t.Errorf("%s %s %s distance %d: %v", algo, h, j, d, err)
		}
		// Digests computed by the former in-process decoder (0.2.0-rc.1): moving the
		// decode into the worker must not change stored fingerprints.
		want := map[string][2]string{"dhash": {"0000300000000000", "0000300000000000"}, "phash": {"82673f3e3c60c327", "82673f3e3c40c367"}}[algo]
		if h != want[0] || j != want[1] {
			t.Errorf("%s digests changed: %s %s, want %v", algo, h, j, want)
		}
	}
	if _, err := Hash(t.Context(), []byte("not an image"), "sha256"); err == nil {
		t.Fatal("hashed non-image")
	}
	if d, err := Distance("ff", "00"); err != nil || d != 8 {
		t.Errorf("one-byte Hamming distance = %d, %v", d, err)
	}
	if _, err := Distance("bad", "bad"); err == nil {
		t.Fatal("accepted invalid hashes")
	}
}

// The advertised envelope renders, and anything outside it is refused by
// validation with a clear message, not by an out-of-memory failure (SDK-11).
func TestRenderEnvelope(t *testing.T) {
	src := image.NewRGBA64(image.Rect(0, 0, 724, 724))
	for y := range 724 {
		for x := range 724 {
			src.Set(x, y, color.RGBA64{R: uint16(x * 90), G: uint16(y * 90), B: uint16(x ^ y), A: 50000})
		}
	}
	var sixteenBit bytes.Buffer
	if err := png.Encode(&sixteenBit, src); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat(`{"type":"text","x":10,"y":20,"text":"Welcome ☺ to the server","font":"emoji","size":96,"color":"#ffffff","maxWidth":1000},{"type":"text","x":10,"y":200,"text":"Welcome ééé","font":"Noto","size":96,"color":"#ffffff"},{"type":"text","x":10,"y":400,"text":"Welcome","font":"Inter","size":96,"color":"#ffffff"},`, 4)
	assets := map[string][]byte{"asset:bg.png": sixteenBit.Bytes()}
	for _, spec := range []string{
		`{"width":1024,"height":512,"background":{"asset":"bg.png"},"layers":[` + text + `{"type":"image","x":0,"y":0,"width":200,"height":200,"source":{"asset":"bg.png"},"mask":"rounded"}]}`,
		`{"width":2048,"height":256,"background":{"asset":"bg.png"}}`,
	} {
		if _, err := renderTest(t, spec, assets); err != nil {
			t.Fatalf("envelope render failed: %v", err)
		}
	}
	for spec, want := range map[string]string{
		`{"width":2048,"height":2048,"background":"#123456"}`:                                                 "exceeds 524288 pixels",
		`{"width":2049,"height":1,"background":"#123456"}`:                                                    "1..2048",
		`{"width":64,"height":64,"background":{"asset":"big.png"}}`:                                           "exceed 524288 decoded pixels",
		`{"width":64,"height":64,"background":{"asset":"bg.png"},"layers":[` + imageLayer("other.png") + `]}`: "exceed 524288 decoded pixels",
	} {
		big := image.NewGray(image.Rect(0, 0, 1024, 513))
		var b bytes.Buffer
		_ = png.Encode(&b, big)
		_, err := renderTest(t, spec, map[string][]byte{"asset:bg.png": sixteenBit.Bytes(), "asset:big.png": b.Bytes(), "asset:other.png": b.Bytes()})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.60s: %v, want %q", spec, err, want)
		}
	}
}

func imageLayer(asset string) string {
	return fmt.Sprintf(`{"type":"image","x":0,"y":0,"width":10,"height":10,"source":{"asset":%q}}`, asset)
}

func TestRenderDeadlineAndRecovery(t *testing.T) {
	heavy := strings.Repeat(fmt.Sprintf(`{"type":"text","x":0,"y":0,"text":%q,"font":"Noto","size":256,"color":"#123456"},`, strings.Repeat("Ê", 2000)), 63)
	start := time.Now()
	_, err := renderTest(t, `{"width":724,"height":724,"layers":[`+heavy+`{"type":"rect","x":0,"y":0,"width":1,"height":1,"color":"#000000"}]}`, nil)
	if err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("heavy render: %v", err)
	}
	if took := time.Since(start); took > renderDeadline+time.Second {
		t.Fatalf("heavy render held the worker for %s", took)
	}
	if _, err := renderTest(t, `{"width":32,"height":32,"background":"#123456"}`, nil); err != nil {
		t.Fatalf("render after an expired worker: %v", err)
	}
}

// Inputs are resolved before a slot is taken, so a slow download cannot hold one.
func TestRenderLoadsInputsOutsideTheSlot(t *testing.T) {
	var held []func()
	for range cap(slots) {
		release, err := Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, release)
	}
	loaded := make(chan struct{})
	go func() {
		<-loaded
		for _, r := range held {
			r()
		}
	}()
	raw := `{"width":16,"height":16,"background":{"url":"https://cdn.discordapp.com/a.png"}}`
	var s paint.Spec
	_ = json.Unmarshal([]byte(raw), &s)
	in, _ := s.Inputs()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := Render(ctx, []byte(raw), in, func(context.Context, string) ([]byte, error) {
		close(loaded) // every slot is still held here: the load did not need one
		return b.Bytes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
