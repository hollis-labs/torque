//go:build embedgui

package webui

import (
	"embed"
	"io/fs"
)

// distFS holds the compiled GUI bundle. The Makefile `build-prod` target
// populates dist/ (copied from apps/gui/dist) immediately before
// `go build -tags embedgui`, so the directory is present at compile time.
// dist/ is gitignored — it is a build artifact, never committed.
//
//go:embed all:dist
var distFS embed.FS

func guiFS() (fs.FS, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, false
	}
	return sub, true
}
