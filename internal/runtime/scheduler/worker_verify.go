package scheduler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// WorkerVerdict captures the engine-side completion verification result for
// a ModeLongLived worker that self-transitioned to "review". The verdict
// drives the run's ExecutionResult.Status downstream — see ApplyTo for the
// concrete mapping. CW-20260519-0095 Phase 3.
//
// Both failure verdicts (VerdictFailedNoCommitsWithEdits, VerdictBlocked
// NoAction) park the task in `blocked`; their reasons name the different
// operator interventions they need:
//
//   - edits-without-commits is an agent-correctness bug ("forgot to
//     commit"). The diff is preserved in the run's worktree, and the reason
//     names that worktree, how many paths are uncommitted, and the remedy:
//     commit or discard them there, then re-queue. It is not retried under
//     the on_fail rule: a retry re-dispatched at once into a fresh worktree
//     off origin/main, stranding the diff, and occupied the project's slot
//     while a worker that forgot to commit usually forgot again
//     (CW-20261001-0028).
//   - no-action is a task-spec bug ("scope unclear or task malformed")
//     → blocked with reason, the operator needs to fix the task before
//     redispatch makes sense.
type WorkerVerdict struct {
	// Kind selects the verdict's downstream behavior.
	Kind WorkerVerdictKind

	// CommitCount is the number of commits the worker landed on the run
	// branch (vs the configured base). Zero sends classification on to
	// the worktree's uncommitted changes and the worker's activity.
	CommitCount int

	// ToolUseHistogram tallies how many times each tool fired during the
	// worker's session. The keys are the canonical tool names emitted by
	// claude/codex/opencode (Edit, Write, Bash, MultiEdit, NotebookEdit,
	// plus whatever else the agent used). Surfaced on the run_completed
	// event for monitoring, and counted as worker activity when the run
	// landed no commits.
	ToolUseHistogram map[string]int

	// Reason carries the operator-facing explanation for the verdict.
	// Empty for VerdictPassed and VerdictPassedNoEditsExpected.
	Reason string

	// SkipReason is non-empty when verification was skipped (e.g. shared
	// mode without a per-run worktree, missing log file), and on
	// VerdictPassedNoEditsExpected, where it says why a run with nothing
	// to commit still passed. Treated as "passed" for routing but
	// surfaced to the run_completed event so monitors see the skip or the
	// commit-free pass rather than silently believing the engine counted
	// commits.
	SkipReason string
}

// WorkerVerdictKind is the discrete verdict enum.
type WorkerVerdictKind int

const (
	// VerdictPassed means the worker landed at least one commit on the
	// run-branch; the normal review→reviewer-end-agent pipeline proceeds.
	VerdictPassed WorkerVerdictKind = iota

	// VerdictFailedNoCommitsWithEdits — the worker left uncommitted
	// changes in its worktree (`git status --porcelain` is non-empty) but
	// committed nothing on the run-branch. "Did work but didn't ship
	// it." Lifecycle parks the task in blocked, with a reason that points
	// at the preserved worktree; on_fail does not apply. Tool names do not
	// decide this: a Bash call is not an edit unless it left a diff.
	VerdictFailedNoCommitsWithEdits

	// VerdictBlockedNoAction — no commits, a clean worktree, no tool
	// activity in the stream and nothing posted to the task during the
	// run. Scope-mismatch or task-malformed. Lifecycle picks
	// blocked-with-reason.
	VerdictBlockedNoAction

	// VerdictPassedNoEditsExpected — no commits and a clean worktree, but
	// the worker acted: tools fired, or it left comments/artifacts on the
	// task during the run. Read-only work (research, triage, review) whose
	// deliverable is a comment lands here. Routed like VerdictPassed; the
	// SkipReason says what the worker did. CW-20261001-0013.
	VerdictPassedNoEditsExpected
)

// editingToolNames are the file-mutating tool names, in claude-code's
// spelling. Codex tool calls reach stream.jsonl already in this spelling
// (codex_events.go maps commandExecution→Bash, fileChange→Edit). Other
// runtimes spell them differently, so readToolHistogram canonicalizes names
// first; see canonicalToolName. They no longer classify a verdict — the
// worktree diff does (CW-20261001-0013) — but folding them keeps the
// histogram on run_completed comparable across runtimes, and
// formatHistogram lists them first.
//
// Unexported var (was EditingToolNames pre-review): external callers
// could otherwise mutate the slice at runtime. Use EditingTools() if a
// caller outside this package legitimately needs to read the list.
var editingToolNames = []string{"Edit", "Write", "Bash", "MultiEdit", "NotebookEdit"}

// editingToolAliases maps runtime-specific names for an editing primitive
// that differ by more than case onto the canonical name. Every entry is
// observed in real event streams, not guessed: OpenCode's patch-based file
// edit is `apply_patch` (seen in OpenCode's own session store alongside
// bash/edit/write), and it is Edit-class the way codex fileChange is.
var editingToolAliases = map[string]string{
	"apply_patch": "Edit",
}

// canonicalToolName folds a runtime's tool name onto editingToolNames'
// spelling: an editing tool matched case-insensitively (OpenCode reports
// bash/edit/write) or through editingToolAliases. Every other name is
// returned unchanged, and matching is exact after folding, so a name that
// merely contains an editing word (OpenCode's todowrite, an MCP
// memory_write) never counts as an edit.
func canonicalToolName(name string) string {
	lower := strings.ToLower(name)
	if alias, ok := editingToolAliases[lower]; ok {
		return alias
	}
	for _, canonical := range editingToolNames {
		if strings.EqualFold(name, canonical) {
			return canonical
		}
	}
	return name
}

// EditingTools returns a fresh copy of the canonical editing-tool name
// list. Reserved for documentation / tooling consumers.
func EditingTools() []string {
	out := make([]string, len(editingToolNames))
	copy(out, editingToolNames)
	return out
}

// VerifyWorkerCompletion is the engine-side completion check that fires
// when a ModeLongLived worker self-transitions to "review". It classifies
// the run by one rule for every runtime:
//
//  1. Commits on the worker's run-branch (`git rev-list --count
//     <base>..HEAD` in the worktree) → VerdictPassed.
//  2. No commits, but the worktree has uncommitted changes (`git status
//     --porcelain`) → VerdictFailedNoCommitsWithEdits.
//  3. No commits, a clean worktree, and the worker acted — any tool call
//     in its stream.jsonl, or taskOutput > 0 → VerdictPassedNoEditsExpected.
//  4. None of the above → VerdictBlockedNoAction.
//
// taskOutput is how many comments and artifacts landed on the task while
// the run was live, measured by the caller (the verifier does not read the
// store). Pass 0 when it is unknown; the histogram alone then decides
// step 3.
//
// Before CW-20261001-0013, step 2 counted Edit/Write/Bash tool calls rather
// than a diff, and only codex (jsonrpc-stdio) got step 3. A read-only
// claude-code run that reported through comments was graded blocked, or
// failed if it had run Bash. Codex still lands in step 3 when its
// worktree-HEAD commit count reads 0 after a real commit+push elsewhere
// (CW-20260519-0103), because its tool activity is in the histogram.
//
// Skips (returns VerdictPassed with SkipReason set) when:
//
//   - worktreePath is empty (shared mode — no per-run worktree to compare
//     against; the substrate has no clean signal in that mode).
//   - the worktree path no longer exists (the per-run cleanup ran before
//     us — that itself implies the worktree had no work, but classifying
//     here is unsafe because we cannot read the stream.jsonl).
//   - stream.jsonl is missing or unreadable (forensic data unavailable;
//     downgrade to no-verdict so we don't false-flag a working agent
//     whose stream sidecar degraded to no-op).
//   - git rev-list or git status fails in the worktree.
//
// Soft-error policy throughout. Failure to read git/stream is reported
// via SkipReason; the verifier never returns an error — bad state
// collapses to skipped-with-reason. The lifecycle then proceeds as if
// the verification passed, preserving the pre-Phase-3 behavior on
// environments where verification can't run.
//
// ctx bounds the git invocations so scheduler shutdown / per-task cancel
// can tear the verifier down promptly. A nil ctx falls back to
// context.Background; pass context.Background explicitly when the
// caller has no ctx of its own.
func VerifyWorkerCompletion(ctx context.Context, workdirRepoRoot, worktreePath, workspaceLogDir, base string, taskOutput int) WorkerVerdict {
	if ctx == nil {
		ctx = context.Background()
	}
	hist, histOK := readToolHistogram(workspaceLogDir)
	// histOK=false means we couldn't read stream.jsonl reliably (open
	// failure, mid-read scan error). Treat as "forensic data
	// unavailable" — the doc above promises we skip rather than
	// classify on a degraded signal. Without this guard the empty-
	// histogram path would force VerdictBlockedNoAction on every
	// no-commit run, false-flagging working agents whose sidecar
	// degraded to no-op.
	if !histOK {
		return WorkerVerdict{
			Kind:             VerdictPassed,
			ToolUseHistogram: hist,
			SkipReason:       fmt.Sprintf("stream.jsonl at %s missing or unreadable; engine-side commit verification skipped", workspaceLogDir),
		}
	}
	if worktreePath == "" {
		return WorkerVerdict{
			Kind:             VerdictPassed,
			ToolUseHistogram: hist,
			SkipReason:       "no per-run worktree configured (shared mode); engine-side commit verification skipped",
		}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		return WorkerVerdict{
			Kind:             VerdictPassed,
			ToolUseHistogram: hist,
			SkipReason:       fmt.Sprintf("per-run worktree %s no longer present; engine-side commit verification skipped", worktreePath),
		}
	}
	count, countErr := countCommitsOnRunBranch(ctx, worktreePath, base)
	if countErr != nil {
		return WorkerVerdict{
			Kind:             VerdictPassed,
			ToolUseHistogram: hist,
			SkipReason:       fmt.Sprintf("git rev-list at %s failed: %v; engine-side commit verification skipped", worktreePath, countErr),
		}
	}
	v := WorkerVerdict{
		CommitCount:      count,
		ToolUseHistogram: hist,
	}
	if count > 0 {
		v.Kind = VerdictPassed
		return v
	}
	changed, statusErr := countUncommittedChanges(ctx, worktreePath)
	if statusErr != nil {
		v.Kind = VerdictPassed
		v.SkipReason = fmt.Sprintf("git status at %s failed: %v; engine-side commit verification skipped", worktreePath, statusErr)
		return v
	}
	if changed > 0 {
		v.Kind = VerdictFailedNoCommitsWithEdits
		// The dirty worktree survives per-run cleanup (worktreeHasWork), so
		// the reason can send the operator to it.
		v.Reason = fmt.Sprintf("worker exited with edits but no commits on run-branch: %d uncommitted path(s) preserved in worktree %s (tool calls: %s); commit or discard them there, then re-queue the task", changed, worktreePath, formatHistogram(hist))
		return v
	}
	if len(hist) > 0 || taskOutput > 0 {
		v.Kind = VerdictPassedNoEditsExpected
		v.SkipReason = fmt.Sprintf("no commits and a clean worktree, but the worker acted (tool calls: %s; comments/artifacts posted to the task during the run: %d); nothing to commit, passing (CW-20261001-0013)", formatHistogram(hist), taskOutput)
		return v
	}
	v.Kind = VerdictBlockedNoAction
	v.Reason = "worker exited without taking any action — scope unclear or task malformed"
	return v
}

// ApplyTo overlays the verdict onto an existing ExecutionResult-shaped
// (status, reason) pair. Returns (status, reason). Used by the long-lived
// executor in the agent package to override its tentative review result
// when verification fails. Both failure verdicts return "blocked", which the
// lifecycle applies as-is: no retry count, no re-dispatch.
//
// Why returning a tuple instead of mutating: the verifier lives in the
// scheduler package and the result shape lives in the executor package;
// passing a (status, reason) pair across the boundary keeps the
// scheduler→executor type dependency one-way (scheduler does not import
// executor).
func (v WorkerVerdict) ApplyTo(currentStatus, currentReason string) (status, reason string) {
	switch v.Kind {
	case VerdictPassed, VerdictPassedNoEditsExpected:
		// Verification passed (or was skipped). Keep whatever the wait
		// loop set — typically "review" for the worker's self-transition
		// path.
		return currentStatus, currentReason
	case VerdictFailedNoCommitsWithEdits, VerdictBlockedNoAction:
		return "blocked", v.Reason
	default:
		return currentStatus, currentReason
	}
}

// countCommitsOnRunBranch counts commits on the worktree HEAD ahead of the
// given base ref. Mirrors worktree.worktreeHasWork's comparison but
// returns the count instead of a boolean. Falls back through the same
// preference order perRunBaseRef uses: origin/main → origin/HEAD → HEAD
// when the supplied base doesn't resolve.
//
// ctx bounds every git invocation in the fallback chain; callers
// typically pass a short context.WithTimeout so a stuck-`git` (lock
// contention, NFS hang) cannot wedge completion verification forever.
func countCommitsOnRunBranch(ctx context.Context, worktreePath, base string) (int, error) {
	candidates := preferredBases(base)
	for _, ref := range candidates {
		count, err := revListCount(ctx, worktreePath, ref+"..HEAD")
		if err == nil {
			return count, nil
		}
	}
	return 0, fmt.Errorf("no base ref resolved among %v", candidates)
}

// preferredBases returns the resolution order for the base ref the engine
// compares the worktree HEAD against. A non-empty explicit `base` is
// tried first (operator override), then origin/main and origin/HEAD,
// finally HEAD (degenerate but keeps the verifier from hard-failing
// when no remote is configured).
func preferredBases(base string) []string {
	bases := make([]string, 0, 4)
	if strings.TrimSpace(base) != "" {
		bases = append(bases, base)
	}
	for _, ref := range []string{"origin/main", "origin/HEAD", "HEAD"} {
		if base != ref {
			bases = append(bases, ref)
		}
	}
	return bases
}

func revListCount(ctx context.Context, worktreePath, rangeArg string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-list", "--count", rangeArg)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s in %s: %s: %w", rangeArg, worktreePath, strings.TrimSpace(string(out)), err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse git rev-list output %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// countUncommittedChanges returns how many paths `git status --porcelain`
// reports in the worktree: modified, staged, deleted and untracked files
// alike. It matches the check per-run worktree cleanup uses to decide a
// worktree holds work (worktree.worktreeHasWork), so a run graded "edits
// but no commits" is also one whose worktree is preserved for inspection.
func countUncommittedChanges(ctx context.Context, worktreePath string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		var detail string
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		return 0, fmt.Errorf("git status --porcelain in %s: %s: %w", worktreePath, detail, err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// readToolHistogram parses the worker's stream.jsonl file and returns a
// map of tool name (canonicalized; see canonicalToolName) → invocation
// count plus an `ok` flag reporting whether
// the read completed cleanly. ok=true means the histogram is
// authoritative — every tool_use line was either counted or
// deliberately skipped (malformed JSON). ok=false means the data is
// degraded:
//
//   - workspaceLogDir is empty (no log dir at all).
//   - stream.jsonl could not be opened (missing, permissions, etc).
//   - scanner.Err() returned a non-nil read error (I/O failure,
//     ErrTooLong on a line exceeding maxStreamLine) — partial counts
//     are useless for the classifier, which depends on completeness to
//     distinguish "worker did nothing" from "we lost the data".
//
// Callers consult ok to skip verification rather than classifying on
// a possibly-truncated histogram. The empty-histogram-as-no-action
// heuristic from pre-review code false-flagged workers whose sidecar
// degraded; the explicit ok signal closes that gap.
func readToolHistogram(workspaceLogDir string) (map[string]int, bool) {
	hist := map[string]int{}
	if workspaceLogDir == "" {
		return hist, false
	}
	p := filepath.Join(workspaceLogDir, "stream.jsonl")
	f, err := os.Open(p)
	if err != nil {
		return hist, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	// stream.jsonl lines can carry sizable tool inputs (Edit's old_string /
	// new_string blocks). Bump the per-line buffer ceiling from bufio's
	// 64KiB default — a worker doing a multi-hundred-line edit would
	// otherwise hit ErrTooLong and the line would be silently dropped,
	// undercounting the histogram.
	const maxStreamLine = 4 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for scanner.Scan() {
		var line struct {
			Type    string `json:"type"`
			ToolUse *struct {
				Name string `json:"name"`
			} `json:"tool_use,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			// Per-line JSON parse failures are tolerated — JSONL streams
			// can have a partial trailing line on crash, and individual
			// malformed lines don't invalidate the rest of the stream.
			continue
		}
		if line.Type != "tool_use" || line.ToolUse == nil || line.ToolUse.Name == "" {
			continue
		}
		hist[canonicalToolName(line.ToolUse.Name)]++
	}
	if err := scanner.Err(); err != nil {
		// Any scanner error (I/O, ErrTooLong) means we have partial
		// data. The classifier needs completeness to be honest — fall
		// through to skip-with-reason rather than report incomplete
		// counts as authoritative.
		return hist, false
	}
	return hist, true
}

// formatHistogram renders the histogram in a stable, log-friendly shape.
// Empty histograms render as "none".
func formatHistogram(hist map[string]int) string {
	if len(hist) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	// Stable order for the log/operator-readable form. Editing tools
	// listed first in their canonical order, then any extras alphabet-
	// ically. Determinism matters for test snapshots and for operators
	// grepping across runs — Go map iteration is randomized, so without
	// the explicit sort the trailing extras would shuffle per call.
	ordered := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, k := range editingToolNames {
		if _, ok := hist[k]; ok {
			ordered = append(ordered, k)
			seen[k] = struct{}{}
		}
	}
	extras := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}
		extras = append(extras, k)
	}
	sort.Strings(extras)
	ordered = append(ordered, extras...)
	parts := make([]string, 0, len(ordered))
	for _, k := range ordered {
		parts = append(parts, fmt.Sprintf("%s=%d", k, hist[k]))
	}
	return strings.Join(parts, ", ")
}
