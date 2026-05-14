package httpserver

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// portfolioRootSubdir is the named portfolio root under $HOME. Placeholder we
// can tighten later (e.g. move to config, require an explicit opt-in).
const portfolioRootSubdir = "Projects-apps"

// serveArtifactContent streams the file referenced by artifacts.file_path.
// Strict security gate: the symlink-resolved absolute path must live inside
// one of: TORQUE_DATA_DIR, the owning task's working_dir, or
// $HOME/Projects-apps.
func (s *Server) serveArtifactContent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}

	art, err := s.svc.Artifact.Get(id)
	if err != nil {
		if errors.Is(err, sqlstore.ErrArtifactNotFound) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if art.FilePath == "" {
		writeError(w, http.StatusNotFound, "artifact has no file_path")
		return
	}

	// Resolve the candidate to an absolute, symlink-free path. EvalSymlinks
	// also returns an error if any component does not exist, which we map to
	// 404 so callers cannot distinguish "missing" from "outside allowlist".
	resolved, err := resolvePath(art.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "artifact file not found on disk")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Collect the allowed roots. A root is skipped if it is empty or the
	// symlink-resolve fails — never let a bad root upgrade to "allow all".
	var taskWorkingDir string
	if task, err := s.svc.Task.Get(art.TaskID); err == nil {
		taskWorkingDir = task.WorkingDir
	}
	roots := collectAllowedRoots(taskWorkingDir)

	if !anyRootContains(roots, resolved) {
		writeError(w, http.StatusForbidden, "artifact file is outside the allowed roots")
		return
	}

	f, err := os.Open(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "artifact file not found on disk")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusForbidden, "artifact path is a directory")
		return
	}

	contentType, err := detectContentType(f, resolved)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, f) //nolint:errcheck
}

// resolvePath returns the absolute, symlink-resolved form of p.
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// collectAllowedRoots returns the resolved allowlist roots. Empty or
// unresolvable roots are silently dropped.
func collectAllowedRoots(taskWorkingDir string) []string {
	candidates := []string{
		os.Getenv("TORQUE_DATA_DIR"),
		taskWorkingDir,
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, portfolioRootSubdir))
	}

	var roots []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		resolved, err := resolvePath(c)
		if err != nil {
			continue
		}
		roots = append(roots, resolved)
	}
	return roots
}

// anyRootContains reports whether candidate lives inside at least one root.
// Both root and candidate are expected to be Clean, absolute, symlink-free.
func anyRootContains(roots []string, candidate string) bool {
	for _, root := range roots {
		if pathWithin(root, candidate) {
			return true
		}
	}
	return false
}

// pathWithin reports whether candidate is root itself or a descendant.
func pathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}

// detectContentType returns the best Content-Type for the file. Tries
// mime.TypeByExtension first (fast path), then net/http.DetectContentType on
// the first 512 bytes. The file is rewound so the caller can stream from the
// start.
func detectContentType(f *os.File, path string) (string, error) {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct, nil
	}
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}
