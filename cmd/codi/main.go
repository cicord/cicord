// Command codi is the CODI CLI and runtime (`codi serve`).
package main

import (
	"os"

	"github.com/npjonath/codi/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
