package setup

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/portalroutes"
	"github.com/cicord/cicord/internal/writeback"
)

// portalRoutes keeps the route types of a custom portal (<portal>/src/cicord-routes.d.ts,
// checked by `cicord portal typegen --check` in the portal's CI) in line with a plugin
// change: they name the version of every plugin with routes, and their routes.
type portalRoutes portalroutes.Result

// regenerateRoutes generates the route types of the repository in dir (the active revision
// with the change applied, loaded and built as repo), with the same code as
// `cicord portal typegen` (and `cicord plugin add` / `update`).
func regenerateRoutes(dir string, repo *config.Repo) portalRoutes {
	return portalRoutes(portalroutes.Regenerate(dir, repo))
}

// apply adds the regenerated file to the edits of the change (same commit or pull
// request) and returns the commit message completed with what happened to it.
func (p portalRoutes) apply(edits map[string]writeback.EditFunc, message string) string {
	if p.Path == "" {
		return message
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(message, "\n"))
	if p.Content != nil {
		content := p.Content
		edits[p.Path] = func([]byte) ([]byte, error) { return content, nil }
		fmt.Fprintf(&b, "\n\nRegenerated %s (cicord portal typegen).", p.Path)
		if len(p.Removed) > 0 {
			fmt.Fprintf(&b, "\nRoutes no longer available to the portal: %s. Portal code still calling them no longer type-checks.", strings.Join(p.Removed, ", "))
		}
	}
	for _, w := range p.Warnings {
		b.WriteString("\n\nWarning: " + w)
	}
	return b.String()
}

// editPaths lists the paths a change touches, sorted.
func editPaths(edits map[string]writeback.EditFunc) []string {
	return slices.Sorted(maps.Keys(edits))
}
