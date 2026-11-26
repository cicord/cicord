package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/media/digest"
)

// scanBomb repeats the smallest scan of a progressive JPEG (with the Huffman tables
// in front of it) up to the 8 MiB download limit. Go's decoder walks every block of
// the image for each scan, so the native decode takes minutes (review finding B1).
func scanBomb(t *testing.T, data []byte) []byte {
	t.Helper()
	type seg struct {
		start, end int
		marker     byte
	}
	var segs []seg
	for i := 2; i < len(data); {
		if data[i] != 0xFF {
			t.Fatalf("unexpected byte at %d", i)
		}
		m := data[i+1]
		if m == 0xD9 {
			segs = append(segs, seg{i, i + 2, m})
			break
		}
		end := i + 2 + int(binary.BigEndian.Uint16(data[i+2:]))
		if m == 0xDA {
			for data[end] != 0xFF || data[end+1] == 0 || data[end+1] >= 0xD0 && data[end+1] <= 0xD7 {
				end++
			}
		}
		segs = append(segs, seg{i, end, m})
		i = end
	}
	small := -1
	for n, s := range segs {
		if s.marker == 0xDA && (small < 0 || s.end-s.start < segs[small].end-segs[small].start) {
			small = n
		}
	}
	start := segs[small].start
	for n := small; n > 0 && segs[n-1].marker == 0xC4; n-- {
		start = segs[n-1].start
	}
	scan := data[start:segs[small].end]
	last := segs[len(segs)-1].start
	var out bytes.Buffer
	out.Write(data[:last])
	for out.Len()+len(scan) < MaxBytes-16 {
		out.Write(scan)
	}
	out.Write(data[last:])
	return out.Bytes()
}

func TestHashScanBombIsKilledByTheWorkerDeadline(t *testing.T) {
	legit, err := os.ReadFile("testdata/progressive-444.jpg")
	if err != nil {
		t.Fatal(err)
	}
	// A legitimate 4-megapixel progressive 4:4:4 JPEG, the decoder's worst memory
	// case, still fits the hash worker, and the worker agrees with a native decode.
	native, err := digest.Perceptual(bytes.NewReader(legit), "phash")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Hash(t.Context(), legit, "phash"); err != nil || got != native {
		t.Fatalf("legitimate image: %q, %v (native %q)", got, err, native)
	}
	var small bytes.Buffer
	_ = png.Encode(&small, image.NewGray(image.Rect(0, 0, 64, 64)))
	want, err := digest.Perceptual(bytes.NewReader(small.Bytes()), "phash")
	if err != nil {
		t.Fatal(err)
	}

	bomb := scanBomb(t, legit)
	// Hold every hash slot but one, so the second phase also proves the bomb released it.
	var held []func()
	for range cap(hashSlots) - 1 {
		hashSlots <- struct{}{}
		held = append(held, func() { <-hashSlots })
	}
	for range cap(slots) - 1 {
		release, err := Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, release)
	}
	defer func() {
		for _, r := range held {
			r()
		}
	}()
	start := time.Now()
	_, err = Hash(t.Context(), bomb, "dhash")
	if err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("scan bomb: %v", err)
	}
	if took := time.Since(start); took > hashDeadline+2*time.Second {
		t.Fatalf("scan bomb held the worker for %s", took)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if got, err := Hash(ctx, small.Bytes(), "phash"); err != nil || got != want {
		t.Fatalf("hash after the bomb: %q, %v", got, err)
	}
}

// The host only parses the header, so oversized or foreign data never reaches a decoder.
func TestHashRejectsBeforeDecoding(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		algo string
	}{
		{"unknown algorithm", []byte("x"), "md5"},
		{"oversized", make([]byte, MaxBytes+1), "phash"},
		{"not an image", []byte("GIF89a garbage"), "dhash"},
	} {
		if _, err := Hash(t.Context(), c.data, c.algo); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}
