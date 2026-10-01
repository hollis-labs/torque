package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	verdict := VerifyWorkerCompletion(context.Background(), "" /*repoRoot unused*/, worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Equal(t, 2, verdict.CommitCount)
	assert.Equal(t, 2, verdict.ToolUseHistogram["Edit"])
	assert.Equal(t, 1, verdict.ToolUseHistogram["Bash"])
	assert.Empty(t, verdict.SkipReason)
}

// TestVerifyWorkerCompletion_FailedNoCommitsWithEdits is the "did work
// but didn't ship it" path — the worker left uncommitted changes in its
// worktree and landed zero commits. Lifecycle picks this up as a failed run.
func TestVerifyWorkerCompletion_FailedNoCommitsWithEdits(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	dirtyWorktree(t, worktreePath)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Edit"},
		{Tool: "Write"},
		{Tool: "Bash"},
	})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictFailedNoCommitsWithEdits, verdict.Kind)
	assert.Equal(t, 0, verdict.CommitCount)
	assert.Contains(t, verdict.Reason, "edits but no commits")
	assert.Contains(t, verdict.Reason, "2 uncommitted path(s)")
	assert.Contains(t, verdict.Reason, "Edit=1")
	assert.Empty(t, verdict.SkipReason)
}

// TestVerifyWorkerCompletion_DiffDecidesEdits pins that the worktree diff,
// not the tool names, decides "edits": a Bash call that left a diff is an
// edit, and output posted to the task does not excuse the missing commit.
func TestVerifyWorkerCompletion_DiffDecidesEdits(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "README.md"), []byte("changed by sed\n"), 0o644))
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Bash"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 3)
	assert.Equal(t, VerdictFailedNoCommitsWithEdits, verdict.Kind)
	assert.Contains(t, verdict.Reason, "1 uncommitted path(s)")
}

// TestVerifyWorkerCompletion_CommitsWinOverDirtyTree: a worker that
// committed and also left stray changes still passes on its commits.
func TestVerifyWorkerCompletion_CommitsWinOverDirtyTree(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 1)
	dirtyWorktree(t, worktreePath)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Equal(t, 1, verdict.CommitCount)
}

// TestVerifyWorkerCompletion_ReadOnlyWithComments is the claude-code
// read-only run from the agent-os smoke test (CW-20261001-0006, run 1137):
// it read, reported through comments and self-transitioned to review. No
// commits, clean worktree, task-visible output — it passes, and says why.
func TestVerifyWorkerCompletion_ReadOnlyWithComments(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Read"},
		{Tool: "Grep"},
		{Tool: "mcp__loopback__torque_comment_add"},
		{Tool: "mcp__loopback__torque_task_review"},
	})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 2)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
	assert.Equal(t, 0, verdict.CommitCount)
	assert.Empty(t, verdict.Reason)
	assert.Contains(t, verdict.SkipReason, "clean worktree")
	assert.Contains(t, verdict.SkipReason, "during the run: 2")
	assert.Contains(t, verdict.SkipReason, "mcp__loopback__torque_comment_add=1")

	s, r := verdict.ApplyTo("review", "")
	assert.Equal(t, "review", s)
	assert.Empty(t, r)
}

// TestVerifyWorkerCompletion_BashOnlyIsNotAnEdit is CW-20261001-0007 (run
// 1138): the worker ran Bash to inspect things, changed nothing and
// committed nothing. Bash alone is not an edit — without a diff this is a
// commit-free pass, not "edits but no commits".
func TestVerifyWorkerCompletion_BashOnlyIsNotAnEdit(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Bash"}, {Tool: "Bash"}, {Tool: "Read"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
	assert.Empty(t, verdict.Reason)
	assert.Contains(t, verdict.SkipReason, "Bash=2")
}

// TestVerifyWorkerCompletion_EditsRevertedIsNoDiff: Edit calls whose
// changes were undone before exit leave nothing to commit.
func TestVerifyWorkerCompletion_EditsRevertedIsNoDiff(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}, {Tool: "Edit"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
}

// TestVerifyWorkerCompletion_TaskOutputAloneIsAction: a stream with no
// tool_use lines still passes when comments or artifacts landed on the
// task during the run.
func TestVerifyWorkerCompletion_TaskOutputAloneIsAction(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, nil)

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 1)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
	assert.Contains(t, verdict.SkipReason, "tool calls: none")
}

// TestVerifyWorkerCompletion_BlockedNoAction is the scope-mismatch path:
// no commits, a clean worktree, no tool activity and nothing posted to the
// task. Lifecycle picks this up as blocked-with-reason (the operator should
// fix the task, not retry the agent).
func TestVerifyWorkerCompletion_BlockedNoAction(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, nil)

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictBlockedNoAction, verdict.Kind)
	assert.Equal(t, 0, verdict.CommitCount)
	assert.Contains(t, verdict.Reason, "scope unclear or task malformed")
}

// TestVerifyWorkerCompletion_CodexZeroCommitsWithActivity covers the case
// the old jsonrpc-stdio special case existed for: codex's worktree-HEAD
// commit count can read 0 after a real commit+push+PR (CW-20260519-0103).
// Its activity is in the histogram and the worktree is clean, so the one
// runtime-agnostic rule passes it without knowing the runtime.
func TestVerifyWorkerCompletion_CodexZeroCommitsWithActivity(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "Bash"}, // codex commandExecution projects to "Bash"
		{Tool: "Edit"}, // codex fileChange projects to "Edit"
	})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
	assert.Empty(t, verdict.Reason)
}

// TestVerifyWorkerCompletion_SkipsWhenGitStatusFails: a 0-commit run whose
// worktree git cannot read collapses to a skip rather than a verdict.
func TestVerifyWorkerCompletion_SkipsWhenGitStatusFails(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	// A corrupt index breaks `git status` but not `git rev-list`.
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, ".git", "index"), []byte("garbage"), 0o644))
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Contains(t, verdict.SkipReason, "git status")
}

// TestVerifyWorkerCompletion_SkipsOnSharedMode confirms an empty worktree
// path (shared mode — no per-run worktree) collapses to a skipped verdict
// rather than false-flagging the worker. The histogram still populates so
// the run_completed event has tool-use data.
func TestVerifyWorkerCompletion_SkipsOnSharedMode(t *testing.T) {
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}})
	verdict := VerifyWorkerCompletion(context.Background(), "", "", logDir, "", 0)
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
	verdict := VerifyWorkerCompletion(context.Background(), "", filepath.Join(t.TempDir(), "does-not-exist"), logDir, "", 0)
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Contains(t, verdict.SkipReason, "no longer present")
}

// TestVerifyWorkerCompletion_SkipsOnMissingStreamLog covers the "forensic
// data unavailable" path: stream.jsonl is missing or unreadable, so the
// histogram can't be authoritative. Per the readToolHistogram ok-false
// contract, this short-circuits to VerdictPassed with a SkipReason —
// the verifier refuses to classify on partial data. Even a worker that
// landed commits ends up in the skip branch here, because we hit the
// histogram check before counting commits; that's deliberate — without
// histogram completeness, a 0-commit run can't be safely classified
// either, and treating the two paths uniformly avoids a footgun.
func TestVerifyWorkerCompletion_SkipsOnMissingStreamLog(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 1)

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, filepath.Join(t.TempDir(), "no-logs"), "", 0)
	assert.Equal(t, VerdictPassed, verdict.Kind)
	assert.Contains(t, verdict.SkipReason, "missing or unreadable")
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

	t.Run("passed-no-edits-expected leaves status unchanged", func(t *testing.T) {
		v := WorkerVerdict{Kind: VerdictPassedNoEditsExpected, SkipReason: "read-only run"}
		s, r := v.ApplyTo("review", "original")
		assert.Equal(t, "review", s)
		assert.Equal(t, "original", r)
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
	hist, ok := readToolHistogram(dir)
	assert.True(t, ok, "malformed JSONL lines should not fail the scanner — per-line parse errors are swallowed")
	assert.Equal(t, 2, hist["Edit"])
	assert.Equal(t, 1, hist["Bash"])
}

// TestReadToolHistogram_ReportsMissingFile pins the ok=false contract for
// the "stream sidecar degraded to no-op" case. Without this, the verifier
// would treat the empty histogram as authoritative and false-flag the
// run as VerdictBlockedNoAction.
func TestReadToolHistogram_ReportsMissingFile(t *testing.T) {
	dir := t.TempDir() // no stream.jsonl written
	hist, ok := readToolHistogram(dir)
	assert.False(t, ok, "missing stream.jsonl must surface as ok=false so the caller can skip")
	assert.Empty(t, hist)
}

// TestReadToolHistogram_ReportsEmptyDir locks the empty-log-dir branch:
// no log dir at all (test fixture or shared mode) must also surface
// ok=false — the caller can't distinguish "worker did nothing" from
// "we have no log to read" without it.
func TestReadToolHistogram_ReportsEmptyDir(t *testing.T) {
	hist, ok := readToolHistogram("")
	assert.False(t, ok)
	assert.Empty(t, hist)
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

// writeStreamJSONL materializes a fake stream.jsonl directly in a
// tempdir and returns that dir as the LOGS DIR path the verifier should
// consult. Production routes through <workspace>/logs/stream.jsonl;
// the test fixture collapses the nesting because VerifyWorkerCompletion
// joins "stream.jsonl" onto whatever path it gets — so the fixture's
// flat tempdir is byte-equivalent to a logs/ subdir for the read.
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
		idx := strconv.Itoa(i)
		fname := filepath.Join(dir, "file-"+idx+".txt")
		require.NoError(t, os.WriteFile(fname, []byte("c"+idx+"\n"), 0o644))
		mustGit(t, dir, "add", fname)
		mustGit(t, dir, "commit", "-m", "test commit "+idx)
	}
	return dir
}

// dirtyWorktree leaves two uncommitted paths in the worktree: a modified
// tracked file and an untracked one.
func dirtyWorktree(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("edited\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644))
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
}
