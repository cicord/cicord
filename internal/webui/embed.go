// Package webui embeds the prebuilt admin UI and default community portal served by
// the runtime (/admin/ and /), and the portal kit used by `codi portal init`. Real builds
// are copied here by scripts/sync-ui.sh; the committed files are placeholders.
package webui

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:admin
var admin embed.FS

//go:embed all:portal
var portal embed.FS

//go:embed all:kit
var kit embed.FS

// Admin returns the admin UI files (index.html at the root).
func Admin() fs.FS { return sub(admin, "admin") }

// Portal returns the default portal files (index.html at the root).
func Portal() fs.FS { return sub(portal, "portal") }

// ErrNoKit means the binary was built without the portal kit (make ui).
var ErrNoKit = errors.New("this codi binary was built without the portal kit (@codi/* packages and the portal template): " +
	"build it with `make ui build` (or run scripts/sync-ui.sh before `go build`)")

// PortalKit returns the portal kit: packages/*.tgz (the @codi/* tarballs) and template/
// (the reference portal). It fails with ErrNoKit when the kit was not generated.
func PortalKit() (fs.FS, error) {
	k := sub(kit, "kit")
	tgz, _ := fs.Glob(k, "packages/*.tgz")
	if _, err := fs.Stat(k, "template/package.json"); err != nil || len(tgz) == 0 {
		return nil, ErrNoKit
	}
	return k, nil
}

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
