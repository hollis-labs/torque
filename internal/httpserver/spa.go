package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
)

// spaHandler serves the compiled React SPA from the dist directory.
// Falls back to a placeholder page when no built frontend is present.
func spaHandler() http.HandlerFunc {
	dirs := []string{
		"apps/gui/dist",
		"../apps/gui/dist",
	}
	if guiDir := os.Getenv("CLOCKWORK_GUI_DIR"); guiDir != "" {
		dirs = append([]string{filepath.Join(guiDir, "dist")}, dirs...)
	}

	var distDir string
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			distDir = d
			break
		}
	}

	if distDir == "" {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<!DOCTYPE html><html><body style="background:#09090b;color:#fafafa;font-family:monospace;display:flex;align-items:center;justify-content:center;height:100vh"><div><h1>Clockwork Manifold</h1><p>GUI not built. Run: cd apps/gui &amp;&amp; npm run build</p></div></body></html>`)) //nolint:errcheck
		}
	}

	fs := http.FileServer(http.Dir(distDir))

	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(distDir, filepath.Clean(r.URL.Path))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	}
}
