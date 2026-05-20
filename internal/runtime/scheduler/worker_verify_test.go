package scheduler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyWorkerCompletion_Passed exercises the happy path: the worker
// committed code on the run-branch. Verification returns VerdictPassed
// with the commit count + histogram surfaced for the run_completed event.
func TestVerifyWorkerCompletion_Passed(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 2)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Read"},
		{Tool: "Edit"},
		{Tool: "Edit"},
		{Tool: "Bash"},
	})

	verdict := VerifyWorkerCompletion("" /*repoRoot unused*/, worktreePath, logDir, "")
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Equal(t, 2, verdict.CommitCount)
	assert.Equal(t, 2, verdict.ToolUseHistogram["Edit"])
	assert.Equal(t, 1, verdict.ToolUseHistogram["Bash"])
	assert.Empty(t, verdict.SkipReason)
}

// TestVerifyWorkerCompletion_FailedNoCommitsWithEdits is the "did work
// but didn't ship it" path — the worker fired editing tools but landed
// zero commits. Lifecycle picks this up as a failed run.
func TestVerifyWorkerCompletion_FailedNoCommitsWithEdits(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Edit"},
		{Tool: "Write"},
		{Tool: "Bash"},
	})

	verdict := VerifyWorkerCompletion("", worktreePath, logDir, "")
	assert.Equal(t, VerdictFailedNoCommitsWithEdits, verdict.Kind)
	assert.Equal(t, 0, verdict.CommitCount)
	assert.Contains(t, verdict.Reason, "edits but no commits")
	assert.Contains(t, verdict.Reason, "Edit=1")
}

// TestVerifyWorkerCompletion_BlockedNoAction is the scope-mismatch path:
// no commits AND no editing tools fired. Lifecycle picks this up as
// blocked-with-reason (the operator should fix the task, not retry the
// agent).
func TestVerifyWorkerCompletion_BlockedNoAction(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Read"},
		{Tool: "Grep"},
	})

	verdict := VerifyWorkerCompletion("", worktreePath, logDir, "")
	assert.Equal(t, VerdictBlockedNoAction, verdict.Kind)
	assert.Equal(t, 0, verdict.CommitCount)
	assert.Contains(t, verdict.Reason, "scope unclear or task malformed")
}

// TestVerifyWorkerCompletion_SkipsOnSharedMode confirms an empty worktree
// path (shared mode — no per-run worktree) collapses to a skipped verdict
// rather than false-flagging the worker. The histogram still populates so
// the run_completed event has tool-use data.
func TestVerifyWorkerCompletion_SkipsOnSharedMode(t *testing.T) {
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}})
	verdict := VerifyWorkerCompletion("", "", logDir, "")
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.NotEmpty(t, verdict.SkipReason)
	assert.Equal(t, 1, verdict.ToolUseHistogram["Edit"])
}

// TestVerifyWorkerCompletion_SkipsOnMissingWorktree covers the
// "scheduler cleaned the worktree before us" timing: per-run cleanup ran
// because the worker landed no commits, removing the worktree. Without
// the worktree the verifier can't run git rev-list, so we skip rather
// than mark the worker failed for an absent path.
func TestVerifyWorkerCompletion_SkipsOnMissingWorktree(t *testing.T) {
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}})
	verdict := VerifyWorkerCompletion("", filepath.Join(t.TempDir(), "does-not-exist"), logDir, "")
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Contains(t, verdict.SkipReason, "no longer present")
}

// TestVerifyWorkerCompletion_SkipsOnMissingStreamLog covers the "forensic
// data unavailable" path: the worktree exists but stream.jsonl is
// missing. Histogram comes back empty, but verification still runs and
// classifies based on commits alone — which means a worker that
// committed gets VerdictPassed even without histogram evidence.
func TestVerifyWorkerCompletion_PassedEvenWithMissingStreamLog(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 1)

	verdict := VerifyWorkerCompletion("", worktreePath, filepath.Join(t.TempDir(), "no-logs"), "")
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Equal(t, 1, verdict.CommitCount)
	assert.Empty(t, verdict.ToolUseHistogram)
}

// TestApplyTo_OverridesOnlyForFailures covers the (status, reason)
// projection. ApplyTo must NOT clobber a successful Status when verdict
// is Passed — the long-lived executor calls it unconditionally on a
// review/done outcome and depends on this no-op behavior.
func TestApplyTo_OverridesOnlyForFailures(t *testing.T) {
	t.Run("passed leaves status unchanged", func(t *testing.T) {
		v := WorkerVerdict{Kind: VerdictPassed}
		s, r := v.ApplyTo("review", "original")
		assert.Equal(t, "review", s)
		assert.Equal(t, "original", r)
	})

	t.Run("passed-with-skip leaves status unchanged", func(t *testing.T) {
		v := WorkerVerdict{Kind: VerdictPassed, SkipReason: "shared mode"}
		s, r := v.ApplyTo("review", "")
		assert.Equal(t, "review", s)
		assert.Equal(t, "", r)
	})

	t.Run("failed-no-commits overrides to failed", func(t *testing.T) {
		v := WorkerVerdict{Kind: VerdictFailedNoCommitsWithEdits, Reason: "edits but no commits"}
		s, r := v.ApplyTo("review", "")
		assert.Equal(t, "failed", s)
		assert.Equal(t, "edits but no commits", r)
	})

	t.Run("blocked-no-action overrides to blocked", func(t *testing.T) {
		v := WorkerVerdict{Kind: VerdictBlockedNoAction, Reason: "scope unclear"}
		s, r := v.ApplyTo("review", "")
		assert.Equal(t, "blocked", s)
		assert.Equal(t, "scope unclear", r)
	})
}

// TestReadToolHistogram_HandlesMalformedLines confirms the parser is
// resilient to partial / corrupt stream.jsonl — common with crashes or
// truncated writes. Malformed lines drop silently; valid lines after them
// still count.
func TestReadToolHistogram_HandlesMalformedLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "stream.jsonl")
	content := `{"type":"tool_use","tool_use":{"name":"Edit"}}
not-json-at-all
{"type":"delta","content":"thinking..."}
{"type":"tool_use","tool_use":{"name":"Bash"}}
{"malformed":
{"type":"tool_use","tool_use":{"name":"Edit"}}
`
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	hist := readToolHistogram(dir)
	assert.Equal(t, 2, hist["Edit"])
	assert.Equal(t, 1, hist["Bash"])
}

// TestFormatHistogram_StableOrdering checks the histogram renders
// deterministically — editing tools first in canonical order, then any
// extras. Test snapshots and operator-side grep workflows rely on this.
func TestFormatHistogram_StableOrdering(t *testing.T) {
	hist := map[string]int{
		"Read":  5,
		"Edit":  2,
		"Bash":  1,
		"Glob":  3,
		"Write": 4,
	}
	got := formatHistogram(hist)
	assert.Equal(t, "Edit=2, Write=4, Bash=1, Glob=3, Read=5", got)
}

// ---- helpers ----------------------------------------------------------

type toolUseEntry struct {
	Tool string
}

// writeStreamJSONL materializes a fake stream.jsonl in a tempdir's
// logs/ subdir. Returns the LOGS DIR path (matching the shape Verify
// WorkerCompletion expects — it joins "stream.jsonl" itself).
func writeStreamJSONL(t *testing.T, entries []toolUseEntry) string {
	t.Helper()
	dir := t.TempDir()
	var lines []byte
	for _, e := range entries {
		ln, _ := json.Marshal(map[string]any{
			"type":     "tool_use",
			"tool_use": map[string]any{"name": e.Tool},
		})
		lines = append(lines, ln...)
		lines = append(lines, '\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stream.jsonl"), lines, 0o644))
	return dir
}

// makeGitRepoWithCommits initializes a real git repo in a tempdir,
// configures it so the verifier's preferredBases resolution finds a
// usable base, and lands the requested number of commits AHEAD of base.
// Returns the worktree path the verifier will run `git rev-list` in.
//
// Strategy: init repo, create initial commit on a "main" branch (this is
// the BASE), check out a feature branch off main, land `commits` more
// commits on the feature branch. preferredBases will fall through to
// HEAD when origin/main / origin/HEAD don't resolve in the in-test
// setup; we don't need a real remote because we explicitly thread an
// empty base + the verifier falls back through.
func makeGitRepoWithCommits(t *testing.T, commits int) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test Runner")
	// Base commit on main — the verifier compares against origin/main
	// → origin/HEAD → HEAD; with no remote we synthesize the same shape
	// by making `main` the local origin via a tracked ref.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644))
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "base commit")
	// Fake an `origin/main` ref pointing at the base commit so the
	// verifier's preferredBases resolution lands here. `git update-ref`
	// is the safest way without a real remote — it just writes the ref.
	mustGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, dir, "update-ref", "refs/remotes/origin/HEAD", "HEAD")
	// Now create a feature branch and pile `commits` more commits onto it.
	if commits > 0 {
		mustGit(t, dir, "checkout", "-b", "fix/test-branch")
	}
	for i := 0; i < commits; i++ {
		fname := filepath.Join(dir, "file-"+intToStr(i)+".txt")
		require.NoError(t, os.WriteFile(fname, []byte("c"+intToStr(i)+"\n"), 0o644))
		mustGit(t, dir, "add", fname)
		mustGit(t, dir, "commit", "-m", "test commit "+intToStr(i))
	}
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
}

func intToStr(i int) string {
	// Tiny utility — avoid pulling strconv in for one call site (clarity
	// > optimization).
	return string(rune('0' + i))
}
