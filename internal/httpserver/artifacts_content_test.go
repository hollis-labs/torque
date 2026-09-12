package httpserver_test

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"

	"github.com/hollis-labs/torque/internal/httpserver"
	"net/http/httptest"

	_ "modernc.org/sqlite"
)

// minimal-png is a 1x1 transparent PNG (67 bytes). Small but valid so that
// http.DetectContentType would also return image/png as a fallback.
var minimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// artifactServerEnv bundles a running test server plus a sandbox dir that is
// on the allowlist. All content-endpoint tests use this.
type artifactServerEnv struct {
	ts      *httptest.Server
	svc     *service.Service
	dataDir string
	taskID  string
}

func setupArtifactServer(t *testing.T) *artifactServerEnv {
	t.Helper()

	dataDir := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", dataDir)
	// Point HOME somewhere safe so the portfolio-root fallback cannot widen
	// the allowlist to the real homedir during the test run.
	t.Setenv("HOME", t.TempDir())

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// Make a task (working_dir = dataDir so we can test task-working-dir
	// rooting distinctly from TORQUE_DATA_DIR).
	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "artifact content test task",
		Executor:   "cli",
		WorkingDir: dataDir,
	})
	require.NoError(t, err)

	return &artifactServerEnv{ts: ts, svc: svc, dataDir: dataDir, taskID: task.ID}
}

func createArtifact(t *testing.T, svc *service.Service, taskID, filePath string) int64 {
	t.Helper()
	rec := &sqlstore.ArtifactRecord{
		TaskID:   taskID,
		Type:     "file",
		FilePath: filePath,
	}
	require.NoError(t, svc.Artifact.Create(rec))
	return rec.ID
}

func TestArtifactContent_NotFound_NoRow(t *testing.T) {
	env := setupArtifactServer(t)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/999999/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestArtifactContent_NotFound_EmptyFilePath(t *testing.T) {
	env := setupArtifactServer(t)

	id := createArtifact(t, env.svc, env.taskID, "")

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestArtifactContent_NotFound_MissingFile(t *testing.T) {
	env := setupArtifactServer(t)

	id := createArtifact(t, env.svc, env.taskID, filepath.Join(env.dataDir, "does-not-exist.txt"))

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestArtifactContent_Forbidden_OutsideAllowlist(t *testing.T) {
	env := setupArtifactServer(t)

	// Create a file in an unrelated temp dir that is NOT in the allowlist.
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0o600))

	id := createArtifact(t, env.svc, env.taskID, target)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestArtifactContent_Forbidden_TraversalEscape(t *testing.T) {
	env := setupArtifactServer(t)

	// Absolute-looking path with traversal pieces that, after Clean+Abs+
	// EvalSymlinks, lands outside the allowlist. /etc/passwd exists on macOS
	// and Linux and is reliably outside the test sandbox.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("traversal test relies on POSIX /etc/passwd")
	}
	// Enough .. pieces to climb above the temp-dir depth on any sensible
	// host; filepath.Clean will collapse a surplus at the root.
	parts := []string{env.dataDir}
	for i := 0; i < 20; i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, "etc", "passwd")
	id := createArtifact(t, env.svc, env.taskID, filepath.Join(parts...))

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestArtifactContent_Forbidden_SymlinkEscape(t *testing.T) {
	env := setupArtifactServer(t)

	// Target lives outside the allowlist.
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("top secret"), 0o600))

	// Symlink sits inside the allowlist but points outside.
	linkPath := filepath.Join(env.dataDir, "shortcut.txt")
	require.NoError(t, os.Symlink(target, linkPath))

	id := createArtifact(t, env.svc, env.taskID, linkPath)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestArtifactContent_OK_Image(t *testing.T) {
	env := setupArtifactServer(t)

	path := filepath.Join(env.dataDir, "tiny.png")
	require.NoError(t, os.WriteFile(path, minimalPNG, 0o600))

	id := createArtifact(t, env.svc, env.taskID, path)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "image/png"),
		"expected image/png, got %q", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, minimalPNG, body)
}

func TestArtifactContent_OK_Text(t *testing.T) {
	env := setupArtifactServer(t)

	path := filepath.Join(env.dataDir, "notes.txt")
	content := []byte("hello world\n")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	id := createArtifact(t, env.svc, env.taskID, path)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.True(t,
		strings.HasPrefix(ct, "text/plain"),
		"expected text/plain Content-Type, got %q", ct)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, content, body)
}

func TestArtifactContent_OK_UnderTaskWorkingDir(t *testing.T) {
	env := setupArtifactServer(t)

	// Point the task's working_dir at a separate allowed dir, drop the
	// TORQUE_DATA_DIR to force rooting via the task's working_dir.
	wd := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))

	path := filepath.Join(wd, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("log body"), 0o600))

	id := createArtifact(t, env.svc, env.taskID, path)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestArtifactContent_OK_RelativePathUsesTaskWorkingDirNotProcessCWD(t *testing.T) {
	env := setupArtifactServer(t)

	wd := t.TempDir()
	daemonDir := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))
	t.Chdir(daemonDir)

	relPath := filepath.Join("artifacts", "report.txt")
	require.NoError(t, os.MkdirAll(filepath.Join(wd, "artifacts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(daemonDir, "artifacts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(wd, relPath), []byte("task workdir bytes"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(daemonDir, relPath), []byte("daemon cwd bytes"), 0o600))

	id := createArtifact(t, env.svc, env.taskID, relPath)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("task workdir bytes"), body)
}

func TestArtifactContent_HEAD_RelativePathMatchesGETHeaders(t *testing.T) {
	env := setupArtifactServer(t)

	wd := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))
	path := filepath.Join(wd, "notes.txt")
	content := []byte("head body")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	id := createArtifact(t, env.svc, env.taskID, "notes.txt")

	getResp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	req, err := http.NewRequest(http.MethodHead, env.ts.URL+"/api/v1/artifacts/"+itoa(id)+"/content", nil)
	require.NoError(t, err)
	headResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer headResp.Body.Close()
	assert.Equal(t, http.StatusOK, headResp.StatusCode)
	assert.Equal(t, getResp.Header.Get("Content-Type"), headResp.Header.Get("Content-Type"))
	assert.Equal(t, getResp.Header.Get("Content-Length"), headResp.Header.Get("Content-Length"))
	headBody, err := io.ReadAll(headResp.Body)
	require.NoError(t, err)
	assert.Empty(t, headBody)
}

func TestArtifactContent_RelativePathRequiresUsableTaskWorkingDir(t *testing.T) {
	env := setupArtifactServer(t)

	fileRoot := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(fileRoot, []byte("not a dir"), 0o600))

	tests := []struct {
		name       string
		workingDir string
	}{
		{name: "empty"},
		{name: "relative", workingDir: "relative-root"},
		{name: "missing", workingDir: filepath.Join(t.TempDir(), "missing")},
		{name: "file", workingDir: fileRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
				TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(tt.workingDir)},
			}))

			id := createArtifact(t, env.svc, env.taskID, filepath.Join("out", tt.name+".txt"))

			resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		})
	}
}

func TestArtifactContent_AbsolutePathIgnoresUnusableTaskWorkingDir(t *testing.T) {
	env := setupArtifactServer(t)

	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr("relative-root")},
	}))
	path := filepath.Join(env.dataDir, "absolute.txt")
	require.NoError(t, os.WriteFile(path, []byte("absolute body"), 0o600))

	id := createArtifact(t, env.svc, env.taskID, path)

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestArtifactContent_RelativePathMissingAndDirectory(t *testing.T) {
	env := setupArtifactServer(t)

	wd := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))
	require.NoError(t, os.Mkdir(filepath.Join(wd, "dir-artifact"), 0o700))

	tests := []struct {
		name       string
		filePath   string
		wantStatus int
	}{
		{name: "missing", filePath: "missing.txt", wantStatus: http.StatusNotFound},
		{name: "directory", filePath: "dir-artifact", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := createArtifact(t, env.svc, env.taskID, tt.filePath)

			resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestArtifactContent_Forbidden_RelativeTraversalEscape(t *testing.T) {
	env := setupArtifactServer(t)

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("traversal test relies on POSIX /etc/passwd")
	}
	wd := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))

	parts := make([]string, 0, 22)
	for i := 0; i < 20; i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, "etc", "passwd")
	id := createArtifact(t, env.svc, env.taskID, filepath.Join(parts...))

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestArtifactContent_RelativeSymlinkPolicy(t *testing.T) {
	env := setupArtifactServer(t)

	wd := t.TempDir()
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(wd)},
	}))

	outsideDir := t.TempDir()
	outsideTarget := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideTarget, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(outsideTarget, filepath.Join(wd, "outside-link.txt")))

	allowedTarget := filepath.Join(env.dataDir, "shared.txt")
	require.NoError(t, os.WriteFile(allowedTarget, []byte("shared"), 0o600))
	require.NoError(t, os.Symlink(allowedTarget, filepath.Join(wd, "allowed-link.txt")))

	tests := []struct {
		name       string
		filePath   string
		wantStatus int
	}{
		{name: "outside", filePath: "outside-link.txt", wantStatus: http.StatusForbidden},
		{name: "allowed", filePath: "allowed-link.txt", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := createArtifact(t, env.svc, env.taskID, tt.filePath)

			resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestArtifactContent_RelativePathUsesSymlinkedTaskWorkingDir(t *testing.T) {
	env := setupArtifactServer(t)

	realWD := t.TempDir()
	linkWD := filepath.Join(t.TempDir(), "workdir-link")
	t.Setenv("TORQUE_DATA_DIR", "")
	require.NoError(t, os.Symlink(realWD, linkWD))
	require.NoError(t, env.svc.Task.Update(env.taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{WorkingDir: strPtr(linkWD)},
	}))
	require.NoError(t, os.WriteFile(filepath.Join(realWD, "result.txt"), []byte("linked root"), 0o600))

	id := createArtifact(t, env.svc, env.taskID, "result.txt")

	resp, err := http.Get(env.ts.URL + "/api/v1/artifacts/" + itoa(id) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func strPtr(s string) *string { return &s }

// itoa is a tiny helper to avoid importing strconv just for test URL building.
func itoa(i int64) string {
	var b [20]byte
	pos := len(b)
	n := i
	if n == 0 {
		return "0"
	}
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}
