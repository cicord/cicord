package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"math/bits"
	"testing/fstest"

	"github.com/cicord/cicord/internal/media/digest"
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

// Hash digests an untrusted image. The host only parses its header; the pixels
// are decoded in the confined worker, under a memory ceiling and a deadline, so a
// decompression or scan bomb costs at most one slot for hashDeadline.
func Hash(ctx context.Context, data []byte, algo string) (string, error) {
	if algo != "sha256" && algo != "dhash" && algo != "phash" {
		return "", errors.New("unknown media hash algorithm")
	}
	if len(data) > MaxBytes {
		return "", errors.New("image larger than 8 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("expected a PNG, JPEG, GIF or WebP image")
	}
	if err := digest.CheckConfig(cfg, format); err != nil {
		return "", err
	}
	if algo == "sha256" {
		s := sha256.Sum256(data)
		return hex.EncodeToString(s[:]), nil
	}
	w, err := hasher()
	if err != nil {
		return "", err
	}
	select {
	case hashSlots <- struct{}{}:
		defer func() { <-hashSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	release, err := Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	out, err := w.run(ctx, hashDeadline, nil, fstest.MapFS{"image": &fstest.MapFile{Data: data}}, 64, "CICORD_MEDIA_HASH", algo, "GOMEMLIMIT", "80MiB")
	if err != nil {
		return "", fmt.Errorf("image hash %w", err)
	}
	if len(out) != 16 {
		return "", errors.New("image hash failed: unexpected worker output")
	}
	return string(out), nil
}
