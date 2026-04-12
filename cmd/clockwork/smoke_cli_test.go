package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSmokeCLIExecutorCompletes spins up the full serve stack and runs a task
// through the real CLI executor against a shell-script profile. Verifies that:
//
//	(a) bootstrap.Executors registers the CLI executor,
//	(b) loadProfilesOrEmpty parses the YAML pointed at by CLOCKWORK_PROFILES_PATH
//	    and the scheduler can route tasks to the cli executor by name,
//	(c) Step 2's handleArtifactSignal parses the JSON artifact payload and
//	    populates result.Artifacts on a real subprocess path (not just mock),
//	(d) the task transitions to "done" via the lifecycle on executor success,
//	(e) the parsed artifact is persisted and queryable via the HTTP API.
//
// This is the Step 8 closer for Path A of the E2E execution plan.
func TestSmokeCLIExecutorCompletes(t *testing.T) {
	dir := t.TempDir()

	// Write the shell-script fixture with an absolute path. The CLI executor
	// spawns the command with exec.Cmd which resolves relative paths against
	// the daemon's cwd — we can't rely on that being the repo root in tests,
	// so the profile must point at an absolute path.
	scriptPath := filepath.Join(dir, "smoke-echo.sh")
	scriptContent := `#!/usr/bin/env bash
# smoke-echo.sh — minimal executor-cli smoke test profile.
# Ignores its args; emits an artifact signal and a DONE signal.
set -eu
echo "starting smoke run"
echo '{"signal":"CLOCKWORK_ARTIFACT","type":"log","content":"smoke run output"}'
echo "CLOCKWORK_DONE"
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0o755))

	// Write the profile file with the absolute script path.
	profilePath := filepath.Join(dir, "profiles.yaml")
	profileContent := fmt.Sprintf(`agent_profiles:
  smoke-echo:
    executor: cli
    command: %s
    output_format: print
    timeout_seconds: 10
`, scriptPath)
	require.NoError(t, os.WriteFile(profilePath, []byte(profileContent), 0o644))

	// Env isolation — same pattern as TestServeE2EMockTaskCompletes.
	t.Setenv("CLOCKWORK_DB_PATH", filepath.Join(dir, "test.db"))
	t.Setenv("CLOCKWORK_DATA_DIR", dir)
	t.Setenv("CLOCKWORK_POSTGRES_DSN", "")
	t.Setenv("CLOCKWORK_PROFILES_PATH", profilePath)
	t.Setenv("CLOCKWORK_SCHED_INTERVAL", "1")
	t.Setenv("CLOCKWORK_SCHED_ENABLED", "true")
	t.Setenv("CLOCKWORK_SCHED_WORKERS", "1")

	// Pick a free port by binding 127.0.0.1:0 and handing the listener to
	// runServe. runServe owns the listener from that point forward.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runServe(ctx, ln) }()

	// Wait for the HTTP server to come up.
	waitForListen(t, base+"/api/v1/scheduler/status", 5*time.Second)

	// Create a task targeting executor=cli with the smoke-echo profile. The
	// description is ignored by the shell script, but the CLI executor passes
	// it as the sole arg via buildGenericSpec.
	createBody := `{
		"title":         "cli smoke",
		"description":   "run the smoke-echo script",
		"executor":      "cli",
		"agent_profile": "smoke-echo",
		"on_done":       "close"
	}`
	resp, err := http.Post(base+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create task: status=%d body=%s", resp.StatusCode, b)
	}
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	taskID, _ := created["id"].(string)
	require.NotEmpty(t, taskID, "created task must have an id")

	// Poll until the task reaches a terminal state.
	deadline := time.Now().Add(15 * time.Second)
	var finalStatus string
	terminal := map[string]bool{"done": true, "review": true, "blocked": true, "failed": true}
	for time.Now().Before(deadline) {
		r, err := http.Get(base + "/api/v1/tasks/" + taskID)
		require.NoError(t, err)
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()

		var task map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &task))
		if s, ok := task["status"].(string); ok {
			finalStatus = s
			if terminal[s] {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Equal(t, "done", finalStatus,
		"task should reach terminal 'done' via the CLI executor + lifecycle; got %q", finalStatus)

	// Fetch artifacts via the HTTP API. This proves the scheduler's event
	// callback persisted the parsed Artifact from the JSON signal.
	//
	// Note: sqlstore.ArtifactRecord has no JSON tags, so fields serialize with
	// Go's default (PascalCase) names: "Type", "Content", "ID", etc. If that
	// changes, update the keys below accordingly.
	ar, err := http.Get(base + "/api/v1/artifacts?task_id=" + taskID)
	require.NoError(t, err)
	defer ar.Body.Close()
	require.Equal(t, http.StatusOK, ar.StatusCode, "GET /api/v1/artifacts must succeed")

	bodyBytes, err := io.ReadAll(ar.Body)
	require.NoError(t, err)
	var wrapped struct {
		Artifacts []map[string]interface{} `json:"artifacts"`
	}
	require.NoError(t, json.Unmarshal(bodyBytes, &wrapped),
		"parse artifacts response (body: %s)", bodyBytes)

	require.GreaterOrEqual(t, len(wrapped.Artifacts), 1,
		"expected at least one artifact persisted via the scheduler event callback, got: %s", bodyBytes)

	found := false
	for _, a := range wrapped.Artifacts {
		if a["Type"] == "log" && a["Content"] == "smoke run output" {
			found = true
			break
		}
	}
	assert.True(t, found,
		"expected artifact with Type=log Content='smoke run output' (Step 2 regression guard), got: %s", bodyBytes)

	// Clean shutdown — cancel ctx and wait for runServe to return.
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("runServe returned: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServe did not shut down within 10s of ctx cancel")
	}
}
