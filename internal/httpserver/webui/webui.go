// Package webui bridges the Torque HTTP server to the compiled React GUI
// bundle (apps/gui/dist).
//
// The bundle is compiled INTO the binary only when it is built with the
// `embedgui` build tag — see the Makefile `build-prod` target, which runs
// the npm build and copies dist/ into this package before
// `go build -tags embedgui`. A plain `go build` (no tag) produces a binary
// with no embedded GUI; the HTTP server then falls back to serving
// apps/gui/dist from disk, which is the local dev workflow.
//
// One binary, one port: a `build-prod` artifact serves the API and the GUI
// from the same process with no apps/gui/dist dependency on disk.
package webui

import "io/fs"

// FS returns the embedded GUI filesystem. ok is true only when the binary
// was built with the `embedgui` tag; callers fall back to disk when false.
func FS() (gui fs.FS, ok bool) {
	return guiFS()
}

// Embedded reports whether this binary carries the GUI bundle.
func Embedded() bool {
	_, ok := guiFS()
	return ok
}
