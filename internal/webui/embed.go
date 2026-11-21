// Package webui embeds the prebuilt admin UI and default community portal served by
// the runtime (/admin/ and /). Real builds are copied here by scripts/sync-ui.sh; the
// committed index.html files are placeholders.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:admin
var admin embed.FS

//go:embed all:portal
var portal embed.FS

// Admin returns the admin UI files (index.html at the root).
func Admin() fs.FS { return sub(admin, "admin") }

// Portal returns the default portal files (index.html at the root).
func Portal() fs.FS { return sub(portal, "portal") }

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
