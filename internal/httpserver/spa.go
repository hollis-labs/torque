package httpserver

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/hollis-labs/torque/internal/httpserver/webui"
)

// spaHandler serves the compiled React SPA. It prefers the GUI bundle
// embedded in the binary (a `make build-prod` / `-tags embedgui` build);
// otherwise it falls back to serving apps/gui/dist from disk (the dev
// workflow), and finally to a placeholder when no built GUI is present.
func spaHandler() http.HandlerFunc {
	if gui, ok := webui.FS(); ok {
		return spaFromFS(gui)
	}
	if distDir := locateDistDir(); distDir != "" {
		return spaFromFS(os.DirFS(distDir))
	}
	return spaPlaceholder()
}

// locateDistDir resolves the on-disk apps/gui/dist directory for a
// non-embedded (dev) build. Empty when no built frontend is on disk.
func locateDistDir() string {
	dirs := []string{
		"apps/gui/dist",
		"../apps/gui/dist",
	}
	if guiDir := os.Getenv("TORQUE_GUI_DIR"); guiDir != "" {
		dirs = append([]string{filepath.Join(guiDir, "dist")}, dirs...)
	}
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d
		}
	}
	return ""
}

// spaFromFS serves a built SPA out of fsys. A request whose path does not
// resolve to a real file falls back to index.html, so client-side routes
// resolve. fsys is either the embedded bundle or os.DirFS(distDir).
func spaFromFS(fsys fs.FS) http.HandlerFunc {
	fileServer := http.FileServerFS(fsys)
	return func(w http.ResponseWriter, r *http.Request) {
		// fs.FS paths are slash-separated and unrooted.
		name := path.Clean("/" + r.URL.Path)[1:]
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

// spaPlaceholder is the last-resort handler when neither an embedded bundle
// nor an on-disk dist/ is available — a non-embedded binary whose GUI has
// not been built.
func spaPlaceholder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><body style="background:#09090b;color:#fafafa;font-family:monospace;display:flex;align-items:center;justify-content:center;height:100vh"><div><h1>Torque</h1><p>GUI not built. Run: cd apps/gui &amp;&amp; npm run build</p></div></body></html>`)) //nolint:errcheck
	}
}
