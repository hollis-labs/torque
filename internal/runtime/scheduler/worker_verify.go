package scheduler

import (
	"bufio"
	"context"
	"encoding/json"
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
// The two failure verdicts (VerdictFailedNoCommitsWithEdits, VerdictBlocked
// NoAction) intentionally produce DIFFERENT lifecycle statuses (failed vs
// blocked) because they describe materially different operator interventions:
//
//   - edits-without-commits is an agent-correctness bug ("forgot to
//     commit") → retry-or-block per the on_fail rule, the operator will
//     usually re-run the same scope.
//   - no-edits-no-commits is a task-spec bug ("scope unclear or task
//     malformed") → blocked with reason, the operator needs to fix the
//     task before redispatch makes sense.
type WorkerVerdict struct {
	// Kind selects the verdict's downstream behavior.
	Kind WorkerVerdictKind

	// CommitCount is the number of commits the worker landed on the run
	// branch (vs the configured base). Zero is the failure signal for
	// VerdictFailedNoCommitsWithEdits and VerdictBlockedNoAction.
	CommitCount int

	// ToolUseHistogram tallies how many times each tool fired during the
	// worker's session. The keys are the canonical tool names emitted by
	// claude/codex/opencode (Edit, Write, Bash, MultiEdit, NotebookEdit,
	// plus whatever else the agent used). Surfaced on the run_completed
	// event for monitoring and the (3B) classification.
	ToolUseHistogram map[string]int

	// Reason carries the operator-facing explanation for the verdict.
	// Empty for VerdictPassed.
	Reason string

	// SkipReason is non-empty when verification was skipped (e.g. shared
	// mode without a per-run worktree, missing log file). Treated as
	// "passed" for routing but surfaced to the run_completed event so
	// monitors see the skip rather than silently believing the engine
	// verified.
	SkipReason string
}

// WorkerVerdictKind is the discrete verdict enum.
type WorkerVerdictKind int

const (
	// VerdictPassed means the worker landed at least one commit on the
	// run-branch; the normal review→reviewer-end-agent pipeline proceeds.
	VerdictPassed WorkerVerdictKind = iota

	// VerdictFailedNoCommitsWithEdits — the worker fired Edit/Write/Bash
	// tools but committed nothing on the run-branch. "Did work but
	// didn't ship it." Lifecycle picks failed → on_fail rule.
	VerdictFailedNoCommitsWithEdits

	// VerdictBlockedNoAction — the worker fired zero file-mutating tools
	// AND committed nothing. Scope-mismatch or task-malformed. Lifecycle
	// picks blocked-with-reason.
	VerdictBlockedNoAction

	// VerdictPassedNoEditsExpected — the worker landed zero commits AND
	// zero file-mutating tools, but the task's contract did not require
	// either (e.g. a research / triage task where the deliverable is a
	// comment). Reserved for future use; currently the verifier does not
	// emit this verdict because the substrate has no signal for "task
	// requires no edits". The kind exists so the lifecycle path can
	// route on it without a new code path when we add the signal.
	VerdictPassedNoEditsExpected
)

// EditingToolNames are the tool names whose presence-or-absence in the
// stream classifies a verdict. Mirrors the claude-code-side names; codex
// uses identical tool labels for these primitives (Edit, Write, Bash,
// MultiEdit, NotebookEdit). A worker that hasn't called ANY of these did
// not touch the filesystem in a write-bearing way.
//
// Read-only tools (Read, Grep, Glob) are intentionally NOT in this list —
// a worker that only read files and then signaled done deserves the
// VerdictBlockedNoAction route because reading is not delivery.
var EditingToolNames = []string{"Edit", "Write", "Bash", "MultiEdit", "NotebookEdit"}

// VerifyWorkerCompletion is the engine-side completion check that fires
// when a ModeLongLived worker self-transitions to "review". It does two
// things:
//
//  1. Counts commits on the worker's run-branch via `git rev-list --count
//     <base>..HEAD` in the worktree.
//  2. Tallies file-mutating tool calls from the worker's stream.jsonl.
//
// The combination produces a WorkerVerdict the executor surfaces back to
// the lifecycle manager.
//
// Skips (returns VerdictPassed with SkipReason set) when:
//
//   - worktreePath is empty (shared mode — no per-run worktree to compare
//     against; the substrate has no clean signal in that mode).
//   - the worktree path no longer exists (the per-run cleanup ran before
//     us — that itself implies the worktree had no work, but classifying
//     here is unsafe because we cannot read the stream.jsonl).
//   - stream.jsonl is missing or unreadable (forensic data unavailable;
//     downgrade to no-verdict so we don't false-flag a working agent).
//
// Soft-error policy throughout. Failure to read git/stream is logged
// upstream by the caller; the verifier never returns an error — bad
// state collapses to SkipReason. The lifecycle then proceeds as if the
// verification passed, preserving the pre-Phase-3 behavior on
// environments where verification can't run.
func VerifyWorkerCompletion(workdirRepoRoot, worktreePath, workspaceLogDir, base string) WorkerVerdict {
	hist := readToolHistogram(workspaceLogDir)
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
	count, countErr := countCommitsOnRunBranch(worktreePath, base)
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
	edits := sumEditingTools(hist)
	if edits > 0 {
		v.Kind = VerdictFailedNoCommitsWithEdits
		v.Reason = fmt.Sprintf("worker exited with edits but no commits on run-branch (tool calls: %s)", formatHistogram(hist))
		return v
	}
	v.Kind = VerdictBlockedNoAction
	v.Reason = "worker exited without taking any action — scope unclear or task malformed"
	return v
}

// ApplyTo overlays the verdict onto an existing ExecutionResult-shaped
// (status, reason) pair. Returns (status, reason). Used by the long-lived
// executor in the agent package to override its tentative review result
// when verification fails.
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
	case VerdictFailedNoCommitsWithEdits:
		return "failed", v.Reason
	case VerdictBlockedNoAction:
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
func countCommitsOnRunBranch(worktreePath, base string) (int, error) {
	candidates := preferredBases(base)
	for _, ref := range candidates {
		count, err := revListCount(worktreePath, ref+"..HEAD")
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

func revListCount(worktreePath, rangeArg string) (int, error) {
	cmd := exec.CommandContext(context.Background(), "git", "rev-list", "--count", rangeArg)
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

// readToolHistogram parses the worker's stream.jsonl file and returns a
// map of tool name → invocation count. Best-effort: missing file, partial
// reads, and malformed JSONL lines collapse to a zero/empty histogram —
// the verification path treats an empty histogram as "no editing tools
// fired", which is the conservative classification (routes to blocked
// not failed).
func readToolHistogram(workspaceLogDir string) map[string]int {
	hist := map[string]int{}
	if workspaceLogDir == "" {
		return hist
	}
	p := filepath.Join(workspaceLogDir, "stream.jsonl")
	f, err := os.Open(p)
	if err != nil {
		return hist
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
			continue
		}
		if line.Type != "tool_use" || line.ToolUse == nil || line.ToolUse.Name == "" {
			continue
		}
		hist[line.ToolUse.Name]++
	}
	return hist
}

// sumEditingTools returns the total invocations of file-mutating tools.
func sumEditingTools(hist map[string]int) int {
	total := 0
	for _, name := range EditingToolNames {
		total += hist[name]
	}
	return total
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
	for _, k := range EditingToolNames {
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
