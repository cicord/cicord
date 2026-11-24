// Command cicord is the Cicord CLI and runtime (`cicord serve`).
package main

import (
	"os"

	"github.com/npjonath/cicord/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
