package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/httpserver/webui"
	"github.com/hollis-labs/torque/internal/worktree"
)

// Admin endpoints are GUI-triggered maintenance actions. Pre-launch, the
// frontend has no auth layer, so these are gated to localhost-only by
// default. An optional TORQUE_ADMIN_TOKEN env var, if set, adds an
// X-Admin-Token header requirement on top of the localhost check.

const (
	restartFrontendCooldown = 30 * time.Second
	restartFrontendTimeout  = 60 * time.Second
)

var (
	restartFrontendMu   sync.Mutex
	restartFrontendLast time.Time
)

func adminGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			writeError(w, http.StatusForbidden, "admin endpoints are localhost-only")
			return
		}
		if token := strings.TrimSpace(os.Getenv("TORQUE_ADMIN_TOKEN")); token != "" {
			if r.Header.Get("X-Admin-Token") != token {
				writeError(w, http.StatusForbidden, "invalid admin token")
				return
			}
		}
		next(w, r)
	}
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// restartFrontend shells out to `npm run build` in apps/gui, hashes the
// resulting dist/index.html so callers can tell a new build landed, and
// returns the hash + duration. Rate-limited in-process to one run per
// cooldown window. The 60s timeout covers clean and cold-cache builds on
// the dev machine; slower hardware will need to revisit this.
//
// Only meaningful for a non-embedded (dev) binary that serves the GUI from
// disk. A `build-prod` / -tags embedgui binary carries the GUI compiled in;
// this endpoint then returns 409 — updating the GUI means rebuilding and
// redeploying the binary.
func (s *Server) restartFrontend(w http.ResponseWriter, r *http.Request) {
	if webui.Embedded() {
		writeError(w, http.StatusConflict,
			"GUI is embedded in this binary; rebuild with `make build-prod` and redeploy to update it")
		return
	}

	restartFrontendMu.Lock()
	if time.Since(restartFrontendLast) < restartFrontendCooldown {
		remaining := restartFrontendCooldown - time.Since(restartFrontendLast)
		restartFrontendMu.Unlock()
		w.Header().Set("Retry-After", formatSeconds(remaining))
		writeError(w, http.StatusTooManyRequests, "restart cooldown active, retry shortly")
		return
	}
	restartFrontendLast = time.Now()
	restartFrontendMu.Unlock()

	guiDir, err := locateGuiDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), restartFrontendTimeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "npm", "run", "build")
	cmd.Dir = guiDir
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{
			"error":       "frontend build timed out",
			"timeout_ms":  restartFrontendTimeout.Milliseconds(),
			"duration_ms": duration.Milliseconds(),
			"output":      tailBytes(out, 4000),
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":       "frontend build failed: " + err.Error(),
			"duration_ms": duration.Milliseconds(),
			"output":      tailBytes(out, 4000),
		})
		return
	}

	hash, hashErr := hashDistIndex(guiDir)
	if hashErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":       "build succeeded but dist hash failed: " + hashErr.Error(),
			"duration_ms": duration.Milliseconds(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hash":        hash,
		"duration_ms": duration.Milliseconds(),
	})
}

// locateGuiDir resolves the GUI source directory in priority order:
// explicit TORQUE_GUI_DIR (matches the SPA handler convention),
// then TORQUE_REPO + /apps/gui, then walking up from cwd looking for a
// .git. Returns an error if none of the lookups yield a directory.
func locateGuiDir() (string, error) {
	if gui := strings.TrimSpace(os.Getenv("TORQUE_GUI_DIR")); gui != "" {
		return verifyDir(gui)
	}
	if root := strings.TrimSpace(os.Getenv("TORQUE_REPO")); root != "" {
		return verifyDir(filepath.Join(root, "apps", "gui"))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	found, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return "", err
	}
	return verifyDir(filepath.Join(found, "apps", "gui"))
}

func verifyDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New(path + " is not a directory")
	}
	return path, nil
}

func hashDistIndex(guiDir string) (string, error) {
	path := filepath.Join(guiDir, "dist", "index.html")
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func tailBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}

func formatSeconds(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}
