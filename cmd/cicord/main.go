// Command cicord is the Cicord CLI and runtime (`cicord serve`).
package main

import (
	"os"

	"github.com/cicord/cicord/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
