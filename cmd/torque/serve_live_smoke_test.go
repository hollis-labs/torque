//go:build smoke

// LIVE end-to-end smoke for the full Torque run/execution path.
//
// Unlike internal/e2e/agent_boot/live_smoke_test.go — which drives agent.Boot
// directly — this test exercises the COMPLETE orchestration path that a real
// Torque run takes:
//
//	HTTP task create → scheduler picker → worker dispatch → agent.Executor →
//	agent.Boot → real provider subprocess (claude / codex) → run record +
//	lifecycle transition.
//
// It is the smoke that answers "can I actually use Torque to run work", not
// just "does Boot plant a boot dir". Gated behind `//go:build smoke` so it
// never runs in plain CI.
//
// The task asks the agent to create a marker file. The test then asserts the
// file landed in the run's actual work_root — which proves both that the
// agent did real work AND that it ran in the directory Torque intended:
//
//   - shared variant   → marker in the task working_dir.
//   - worktree variant → marker in the per-run sibling worktree
//     (TORQUE_WORKTREE_PER_RUN — CW-20260517-0038 P2). A marker that shows up
//     in working_dir instead means the worktree path silently fell back.
//
// Run:
//
//	go test -tags smoke -run TestServeLiveSmoke ./cmd/torque/ -v -timeout 600s
//
// Each provider/variant is an isolated subtest. A provider auth/network
// failure is reported as BLOCKED(environment) and does not fail the test; a
// scheduler / dispatch / executor / worktree wiring break IS a hard failure.
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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// markerFile is the file the smoke task asks the agent to create. Asserting
// on its location is how the test distinguishes a real run in the intended
// work_root from a no-op or a wrong-directory dispatch.
const markerFile = "SMOKE_OK.txt"

// smokePrompt is deliberately explicit and minimal so both providers behave
// deterministically: one file, one word, nothing else. It targets the
// directory named by $TORQUE_WORK_ROOT — exercising the env-var work-root
// contract — rather than the process cwd (which is the ephemeral boot dir).
const smokePrompt = "Create a file named " + markerFile + " containing exactly the word OK, " +
	"inside the directory named by the TORQUE_WORK_ROOT environment variable. " +
	"Do not create any other files, do not edit any other files, and do not run any other commands."

// liveProviders maps a profiles.yaml agent-profile name to the binary it
// requires and a human label. These profiles must exist in the repo's
// profiles.yaml — `default` (claude-code, streaming-stdio) and `codex-long`
// (codex, jsonrpc-stdio).
var liveProviders = []struct {
	profile string
	binary  string
	label   string
}{
	{profile: "default", binary: "claude", label: "claude-code"},
	{profile: "codex-long", binary: "codex", label: "codex"},
}

// TestServeLiveSmoke spins up the full serve stack against an isolated temp DB
// and runs a marker-file task end-to-end through the scheduler for each live
// provider, in both the shared and per-run-worktree dispatch shapes.
func TestServeLiveSmoke(t *testing.T) {
	repoRoot := repoRootFromTest(t)
	profilesPath := filepath.Join(repoRoot, "profiles.yaml")
	if _, err := os.Stat(profilesPath); err != nil {
		t.Fatalf("repo profiles.yaml not found at %s: %v", profilesPath, err)
	}

	for _, p := range liveProviders {
		p := p
		for _, worktree := range []bool{false, true} {
			worktree := worktree
			variant := "shared"
			if worktree {
				variant = "worktree"
			}
			t.Run(p.label+"/"+variant, func(t *testing.T) {
				if _, err := exec.LookPath(p.binary); err != nil {
					t.Skipf("BLOCKED(environment): %s binary not on PATH — cannot run a live smoke", p.binary)
				}
				runLiveServeSmoke(t, profilesPath, p.profile, p.label, worktree)
			})
		}
	}
}

// runLiveServeSmoke boots runServe, creates one agent task bound to profile,
// promotes it for the scheduler, polls until the task reaches a terminal
// lifecycle state, then asserts the marker file landed in the run's intended
// work_root.
//
// When worktree is true the per-run worktree path is exercised: the working
// dir is a real git repo with an `origin` remote (SetupPerRun fetches origin
// and branches the worktree from origin/main), TORQUE_WORKTREE_PER_RUN is on,
// and the marker is expected in the sibling worktree dir — not working_dir.
func runLiveServeSmoke(t *testing.T, profilesPath, profile, label string, worktree bool) {
	t.Helper()

	dir := t.TempDir()
	// The agent runs here on the shared path. On the worktree path this is
	// the repo_root; the run's work_root is a sibling worktree.
	workdir := filepath.Join(dir, "agent-workdir")
	require.NoError(t, os.MkdirAll(workdir, 0o755))
	if worktree {
		// SetupPerRun does `git fetch origin` + `git worktree add origin/main`,
		// so the working dir must be a git repo with an origin remote whose
		// main branch is published.
		initGitRepoWithOrigin(t, dir, workdir)
		t.Setenv("TORQUE_WORKTREE_PER_RUN", "true")
	}

	// Isolate every persistent root inside the temp dir — never touch the
	// dev/prod torque.db or .torque data dir.
	t.Setenv("TORQUE_DB_PATH", filepath.Join(dir, "live.db"))
	t.Setenv("TORQUE_DATA_DIR", dir)
	t.Setenv("TORQUE_POSTGRES_DSN", "") // force sqlite even if the dev env sets it
	t.Setenv("TORQUE_PROFILES_PATH", profilesPath)
	t.Setenv("TORQUE_SCHED_INTERVAL", "1")
	t.Setenv("TORQUE_SCHED_ENABLED", "true")
	t.Setenv("TORQUE_SCHED_WORKERS", "1")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runServe(ctx, ln) }()

	waitForListen(t, base+"/api/v1/scheduler/status", 10*time.Second)

	// Create a one-shot agent task. `description` carries through as the
	// user-prompt body (agent.Executor: job.Description → OneShot turn).
	// `on_done: close` transitions doing → done on executor success so the
	// poll observes a definitive terminal state. executor defaults to "cli"
	// for kind=agent (migration 024); agent_profile resolves against the
	// loaded profiles.yaml.
	// The prompt targets $TORQUE_WORK_ROOT, which Torque resolves to the
	// shared working_dir or the per-run worktree as appropriate — so the
	// same prompt verifies both dispatch shapes, and the marker-placement
	// check below confirms the agent honored the env-var contract.
	createBody := map[string]any{
		"title":         "live serve smoke — " + label,
		"description":   smokePrompt,
		"agent_profile": profile,
		"working_dir":   workdir,
		"on_done":       "close",
	}
	bodyJSON, err := json.Marshal(createBody)
	require.NoError(t, err)

	resp, err := http.Post(base+"/api/v1/tasks", "application/json", bytes.NewReader(bodyJSON))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create task should return 201")

	var created map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	taskID, _ := created["id"].(string)
	require.NotEmpty(t, taskID, "created task must have an id")
	t.Logf("provider=%s profile=%s worktree=%v task=%s workdir=%s", label, profile, worktree, taskID, workdir)

	// CW-20260417-0133 forces manual=true on HTTP create. Flip it back so the
	// scheduler picker will dispatch it.
	promote, err := http.NewRequest(http.MethodPut, base+"/api/v1/tasks/"+taskID,
		bytes.NewBufferString(`{"manual":false}`))
	require.NoError(t, err)
	promote.Header.Set("Content-Type", "application/json")
	pr, err := http.DefaultClient.Do(promote)
	require.NoError(t, err)
	pr.Body.Close()
	require.Equal(t, http.StatusOK, pr.StatusCode, "promote to manual=false must succeed")

	// Poll until terminal. A real provider turn for a one-file prompt finishes
	// well within a couple of minutes; the deadline guards a hung subprocess.
	terminal := map[string]bool{"done": true, "review": true, "blocked": true, "failed": true}
	deadline := time.Now().Add(240 * time.Second)
	var finalStatus, blockedReason string
	for time.Now().Before(deadline) {
		task := getTaskJSON(t, base, taskID)
		if s, ok := task["status"].(string); ok {
			finalStatus = s
			if br, ok := task["blocked_reason"].(string); ok {
				blockedReason = br
			}
			if terminal[s] {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	runs := getRunsJSON(t, base, taskID)
	t.Logf("provider=%s: final task status=%q runs=%d", label, finalStatus, len(runs))
	var runID int64
	for i, r := range runs {
		t.Logf("provider=%s run[%d]: ID=%v Status=%v ExitCode=%v Cost=%v ErrorMessage=%q",
			label, i, r["ID"], r["Status"], r["ExitCode"], r["Cost"], r["ErrorMessage"])
		if id, ok := r["ID"].(float64); ok {
			runID = int64(id)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("runServe returned: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runServe did not shut down within 15s of ctx cancel")
	}

	classifyLiveOutcome(t, label, finalStatus, blockedReason, runs)

	// On a clean completion, assert the agent's marker file landed in the
	// run's intended work_root.
	if finalStatus == "done" || finalStatus == "review" {
		assertMarkerPlacement(t, label, worktree, dir, workdir, runID)
	}
}

// assertMarkerPlacement verifies the marker file the agent was asked to write
// exists in the directory the run was supposed to use as its work_root.
func assertMarkerPlacement(t *testing.T, label string, worktree bool, dir, workdir string, runID int64) {
	t.Helper()

	sharedMarker := filepath.Join(workdir, markerFile)
	if !worktree {
		if fileExists(sharedMarker) {
			t.Logf("PASS provider=%s: marker file present in working_dir (%s) — agent did real work in the shared work_root",
				label, sharedMarker)
			return
		}
		t.Errorf("FAIL provider=%s: task completed but marker file %s is absent — the agent produced no output in the expected work_root",
			label, sharedMarker)
		return
	}

	// Worktree variant: the marker must be in the per-run sibling worktree,
	// PerRunPath = "${repoParent}/${repoName}-worktrees-run-<runID>". The
	// agent's untracked marker keeps the worktree "dirty", so CleanupPerRun
	// preserves it after the run.
	require.NotZero(t, runID, "could not resolve run ID from runs API — cannot locate the per-run worktree")
	wtDir := filepath.Join(dir, filepath.Base(workdir)+fmt.Sprintf("-worktrees-run-%d", runID))
	wtMarker := filepath.Join(wtDir, markerFile)

	switch {
	case fileExists(wtMarker):
		t.Logf("PASS provider=%s: marker file present in the per-run worktree (%s) — TORQUE_WORKTREE_PER_RUN routed the run correctly",
			label, wtMarker)
	case fileExists(sharedMarker):
		t.Errorf("FAIL provider=%s: marker file landed in working_dir (%s), NOT the per-run worktree (%s) — per-run worktree setup silently fell back to shared mode despite TORQUE_WORKTREE_PER_RUN=true",
			label, sharedMarker, wtDir)
	default:
		t.Errorf("FAIL provider=%s: task completed but the marker file is in neither the worktree (%s) nor working_dir (%s)",
			label, wtMarker, sharedMarker)
	}
}

// classifyLiveOutcome maps the end-to-end lifecycle result into PASS / FAIL /
// BLOCKED. (Marker-file placement is asserted separately by the caller.)
//
//   - status=done / review                 → run completed; placement check
//     follows.
//   - no run record at all                 → FAIL: the scheduler never
//     dispatched — a picker / worker / executor-registration wiring break.
//   - run record exists, status failed      → inspect ErrorMessage: an
//     auth/network/quota signature is BLOCKED(environment); anything else is
//     a FAIL of the dispatch/executor path.
func classifyLiveOutcome(t *testing.T, label, status, blockedReason string, runs []map[string]any) {
	t.Helper()

	if status == "done" || status == "review" {
		t.Logf("provider=%s: end-to-end run completed via scheduler → agent executor (task status=%s)", label, status)
		return
	}

	if len(runs) == 0 {
		t.Errorf("FAIL provider=%s: task status=%q with NO run record — scheduler never dispatched the task (picker / worker / executor-registration break)",
			label, status)
		return
	}

	// A run record exists, so dispatch + executor wiring worked. Distinguish a
	// provider environment failure from a genuine pipeline break.
	var errMsg string
	runStillActive := false
	for _, r := range runs {
		if m, ok := r["ErrorMessage"].(string); ok && m != "" {
			errMsg = m
		}
		if s, ok := r["Status"].(string); ok && (s == "running" || s == "pending") {
			runStillActive = true
		}
	}

	// Non-terminal task with a still-active run after the poll deadline = a
	// HANG: the agent dispatched but its turn never completed. For codex this
	// is the known approval-elicitation deadlock — codex emits a server-side
	// JSON-RPC approval request for a tool call, the jsonrpc-stdio runtime
	// does not answer it, and the turn blocks forever (CW-20260517-0038
	// deferred the non-claude permission/approval contract).
	if runStillActive || status == "doing" || status == "todo" {
		t.Errorf("FAIL(hang) provider=%s: task status=%q with an unfinished run after the poll deadline — the agent dispatched but its turn never completed. For codex this is the unanswered tool-approval elicitation; codex cannot run tool-using tasks until that contract is threaded.",
			label, status)
		return
	}
	combined := strings.ToLower(errMsg + " " + blockedReason)
	envSignatures := []string{
		"auth", "login", "not logged in", "credential", "api key", "apikey",
		"unauthorized", "401", "403", "network", "timeout", "rate limit",
		"quota", "429", "overloaded", "connection refused",
	}
	for _, sig := range envSignatures {
		if strings.Contains(combined, sig) {
			t.Logf("BLOCKED(environment) provider=%s: task status=%q, run recorded but the provider failed for an environment reason (%q). Dispatch + executor path was exercised cleanly.",
				label, status, errMsg)
			return
		}
	}

	t.Errorf("FAIL provider=%s: task status=%q, run recorded but failed for a non-environment reason — ErrorMessage=%q blocked_reason=%q. This is a dispatch/executor pipeline break.",
		label, status, errMsg, blockedReason)
}

// getTaskJSON GETs a task and decodes it into a generic map.
func getTaskJSON(t *testing.T, base, taskID string) map[string]any {
	t.Helper()
	r, err := http.Get(base + "/api/v1/tasks/" + taskID)
	require.NoError(t, err)
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var task map[string]any
	require.NoError(t, json.Unmarshal(body, &task), "decode task body: %s", string(body))
	return task
}

// getRunsJSON GETs the run records for a task.
func getRunsJSON(t *testing.T, base, taskID string) []map[string]any {
	t.Helper()
	r, err := http.Get(base + "/api/v1/runs?task_id=" + taskID)
	require.NoError(t, err)
	defer r.Body.Close()
	var payload struct {
		Runs []map[string]any `json:"runs"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return payload.Runs
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// initGitRepoWithOrigin makes workdir a git repo with one commit on `main`
// and a bare `origin` remote (created under parent) with `main` published, so
// the scheduler's per-run worktree setup (git fetch origin + git worktree add
// origin/main) succeeds.
func initGitRepoWithOrigin(t *testing.T, parent, workdir string) {
	t.Helper()
	bare := filepath.Join(parent, "origin.git")

	gitIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		// Deterministic identity so `git commit` does not depend on global config.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=torque-smoke", "GIT_AUTHOR_EMAIL=smoke@torque.test",
			"GIT_COMMITTER_NAME=torque-smoke", "GIT_COMMITTER_EMAIL=smoke@torque.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), d, err, string(out))
		}
	}

	gitIn(parent, "init", "--bare", "-b", "main", bare)
	gitIn(workdir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "README.md"),
		[]byte("torque smoke sandbox\n"), 0o644))
	gitIn(workdir, "add", "-A")
	gitIn(workdir, "commit", "-q", "-m", "smoke: initial commit")
	gitIn(workdir, "remote", "add", "origin", bare)
	gitIn(workdir, "push", "-q", "origin", "main")
}

// repoRootFromTest walks up from the test working directory to the repo root
// (the dir holding go.mod).
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRootFromTest: go.mod not found walking up from %s", dir)
		}
		dir = parent
	}
}
