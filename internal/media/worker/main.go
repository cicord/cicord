// Command worker is compiled to WASI so an untrusted font or image cannot exceed
// the renderer's hard memory and execution budgets.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cicord/cicord/internal/media/paint"
)

func main() {
	var s paint.Spec
	err := json.NewDecoder(os.Stdin).Decode(&s)
	if err == nil {
		err = paint.Draw(s, func(key string) ([]byte, error) { return os.ReadFile("/inputs/" + paint.Path(key)) }, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
