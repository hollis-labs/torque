package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeE2EMockTaskCompletes spins up the full serve stack (HTTP server
// plus scheduler plus SSE bridge) against an isolated temp DB, creates a task
// bound to the mock executor, and polls the HTTP API until the task reaches a
// terminal lifecycle state. It then cancels the server context and verifies
// runServe exits cleanly.
func TestServeE2EMockTaskCompletes(t *testing.T) {
	dir := t.TempDir()

	// Isolate all persistent state inside the temp dir so the test does not
	// touch the dev/prod clockwork.db or data dir.
	t.Setenv("CLOCKWORK_DB_PATH", filepath.Join(dir, "test.db"))
	t.Setenv("CLOCKWORK_DATA_DIR", dir)
	t.Setenv("CLOCKWORK_POSTGRES_DSN", "") // force sqlite even if the dev env sets it
	t.Setenv("CLOCKWORK_PROFILES_PATH", filepath.Join(dir, "no-such-profiles.yaml"))
	t.Setenv("CLOCKWORK_SCHED_INTERVAL", "1")
	t.Setenv("CLOCKWORK_SCHED_ENABLED", "true")
	t.Setenv("CLOCKWORK_SCHED_WORKERS", "1")

	// Pick a free port by binding 127.0.0.1:0 and handing the listener to
	// runServe. The listener is closed by srv.Serve inside runServe on shutdown.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, ln)
	}()

	// Wait for the HTTP server to start answering requests. The scheduler
	// status endpoint is a good probe because it is always registered and
	// requires no task state.
	waitForListen(t, base+"/api/v1/scheduler/status", 5*time.Second)

	// Create a task that targets the mock executor. `on_done: "close"` makes
	// the lifecycle transition "doing" directly to "done" on executor success,
	// so we can observe a definitive terminal state via the HTTP API.
	createBody := `{
		"title":       "e2e smoke",
		"description": "mock execution end-to-end",
		"executor":    "mock",
		"on_done":     "close"
	}`
	resp, err := http.Post(base+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create task should return 201")

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	taskID, _ := created["id"].(string)
	require.NotEmpty(t, taskID, "created task must have an id")

	// Poll until the task reaches a terminal state. With a 1s scheduler tick
	// and the zero-delay mock executor, this typically completes in ~1-2s.
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

	// We expect "done" because the task has on_done: "close". The assertion
	// is permissive about the exact terminal state so that lifecycle rule
	// tweaks do not break this smoke test — what matters is that the
	// scheduler picked the task up and the executor ran to completion.
	assert.Equal(t, "done", finalStatus,
		"task should reach terminal 'done' via scheduler → mock executor → lifecycle; got %q", finalStatus)

	// Sanity check: the scheduler status endpoint is now backed by a real
	// Scheduler (not the 503 fallback). This confirms the httpserver.New
	// wiring in runServe actually passed the scheduler reference through.
	sr, err := http.Get(base + "/api/v1/scheduler/status")
	require.NoError(t, err)
	defer sr.Body.Close()
	assert.Equal(t, http.StatusOK, sr.StatusCode, "scheduler/status should succeed, not 503")
	var status map[string]interface{}
	require.NoError(t, json.NewDecoder(sr.Body).Decode(&status))
	assert.Contains(t, status, "enabled")
	assert.Contains(t, status, "max_workers")

	// Trigger graceful shutdown and verify runServe returns within a few seconds.
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

// waitForListen polls url until the HTTP server is answering or the timeout
// expires. It is intentionally silent about the response status: we just want
// to know the socket is live and the handler is wired up.
func waitForListen(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := http.Get(url)
		if err == nil {
			r.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s did not come up within %s", url, timeout)
}
