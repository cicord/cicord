// Command worker is compiled to WASI so an untrusted font or image cannot exceed
// the renderer's hard memory and execution budgets. It renders a card by default;
// CICORD_MEDIA_HASH=dhash|phash makes it hash /inputs/image instead.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cicord/cicord/internal/media/digest"
	"github.com/cicord/cicord/internal/media/paint"
)

func main() {
	var err error
	if algo := os.Getenv("CICORD_MEDIA_HASH"); algo != "" {
		err = hash(algo)
	} else {
		var s paint.Spec
		err = json.NewDecoder(os.Stdin).Decode(&s)
		if err == nil {
			err = paint.Draw(s, func(key string) ([]byte, error) { return os.ReadFile("/inputs/" + paint.Path(key)) }, os.Stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func hash(algo string) error {
	f, err := os.Open("/inputs/image")
	if err != nil {
		return err
	}
	defer f.Close()
	h, err := digest.Perceptual(f, algo)
	if err != nil {
		return err
	}
	_, err = fmt.Print(h)
	return err
}
