//go:build !embedgui

package webui

import "io/fs"

// guiFS reports no embedded bundle. A non-`embedgui` build serves the GUI
// from disk (apps/gui/dist) — the local dev workflow — via the HTTP
// server's disk fallback.
func guiFS() (fs.FS, bool) { return nil, false }
