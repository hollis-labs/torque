package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MergedSet is a set of branch names whose pull request is in the MERGED state
// on the origin remote. It is consumed by the PR-aware cleanup paths to break
// the false-preserve case where a squash-merged PR leaves the local branch
// with commits that look "ahead of origin/main" but are actually shipped.
type MergedSet map[string]bool

// Has reports whether the given local branch ref is recorded as merged. A nil
// or empty set returns false — the caller falls back to the existing
// dirty-preservation rule (commits/uncommitted work).
func (m MergedSet) Has(branch string) bool {
	if m == nil || branch == "" {
		return false
	}
	return m[branch]
}

// MergedHeadRefs queries the origin remote (via the `gh` CLI) for the head
// branch names of recently-merged pull requests and returns them as a set.
// Best-effort: a missing `gh`, an unauthenticated `gh`, a network failure,
// or an unparseable response all return an empty set and a nil error so the
// caller falls through to the existing TTL/dirty rules. A genuine timeout
// past the context deadline returns a non-nil error for the caller to log.
//
// limit caps the number of PRs fetched in a single call; values <= 0 use a
// default of 200 which comfortably covers a project with ~hundreds of merged
// PRs without paging. Operators with much larger histories can raise this.
//
// The `gh` query is bound to the repo at repoRoot (gh auto-detects the remote
// from that working directory), so the returned set reflects the origin that
// the per-run worktrees are branched from.
func MergedHeadRefs(ctx context.Context, repoRoot string, limit int) (MergedSet, error) {
	if limit <= 0 {
		limit = 200
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return MergedSet{}, nil
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--state", "merged",
		"--json", "headRefName",
		"--limit", fmt.Sprintf("%d", limit),
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gh pr list timed out: %w", ctx.Err())
		}
		// Auth missing, no remote, rate-limited, etc. — degrade silently.
		return MergedSet{}, nil
	}
	var rows []struct {
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return MergedSet{}, nil
	}
	set := MergedSet{}
	for _, r := range rows {
		if r.HeadRefName != "" {
			set[r.HeadRefName] = true
		}
	}
	return set, nil
}

// WorktreeBranchName returns the local branch HEAD points to in the worktree
// at wtPath, or "" when the worktree is detached (no branch) or the lookup
// fails. The returned name has no `refs/heads/` prefix — it matches what `gh`
// reports as headRefName, so it can be used as a MergedSet key directly.
func WorktreeBranchName(wtPath string) string {
	out, err := runGitOutput(wtPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// CleanupMergedPerRun extends CleanupPerRun with a PR-merged force-remove
// path. It runs CleanupPerRun first; if that preserves the worktree (commits
// ahead or uncommitted work), it consults mergedSet for the worktree's
// checked-out branch and, when the branch's PR is merged on origin, removes
// the worktree with --force and deletes the local branch.
//
// Squash-merged PRs leave the local branch with commits that look ahead of
// origin/main even though the work has shipped — the existing
// worktreeHasWork check sees those commits and preserves the worktree
// indefinitely. The MergedSet lookup is the authoritative answer to "has this
// shipped?" that the local commit graph cannot give us.
//
// mergedSet may be nil (no `gh` available, sweep skipped) — in that case the
// behavior collapses to CleanupPerRun. Returns (true, nil) when the worktree
// was removed by either path.
func CleanupMergedPerRun(workingDir, wtPath string, mergedSet MergedSet) (bool, error) {
	removed, err := CleanupPerRun(workingDir, wtPath)
	if err != nil {
		return false, err
	}
	if removed {
		return true, nil
	}
	if mergedSet == nil {
		return false, nil
	}
	if _, statErr := os.Stat(wtPath); os.IsNotExist(statErr) {
		return false, nil
	}
	branch := WorktreeBranchName(wtPath)
	if branch == "" || !mergedSet.Has(branch) {
		return false, nil
	}
	repoRoot, err := FindRepoRoot(workingDir)
	if err != nil {
		return false, err
	}
	if err := forceRemoveAndDeleteBranch(repoRoot, wtPath, branch); err != nil {
		return false, err
	}
	return true, nil
}

// SweepMergedPerRun scans the per-run worktree directory for worktrees whose
// checked-out branch has a merged PR and force-removes each one (deleting
// the local branch as well). Placement-aware mirroring SweepPerRun: empty
// root scans the repo's parent dir for `${repoName}-worktrees-run-*`; an
// explicit root scans for `run-*` leaves directly under it.
//
// mergedSet is the merged-branch-name set from MergedHeadRefs; a nil/empty
// set short-circuits to a no-op. Errors on individual worktrees are
// collected and do not abort the sweep.
func SweepMergedPerRun(repoRoot, root string, mergedSet MergedSet) (removed []string, errs []error) {
	if len(mergedSet) == 0 {
		return nil, nil
	}
	scanDir, prefix := perRunScanLocation(repoRoot, root)
	entries, err := os.ReadDir(scanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read worktree scan dir %s: %w", scanDir, err)}
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path := filepath.Join(scanDir, e.Name())
		branch := WorktreeBranchName(path)
		if branch == "" || !mergedSet.Has(branch) {
			continue
		}
		if err := forceRemoveAndDeleteBranch(repoRoot, path, branch); err != nil {
			errs = append(errs, fmt.Errorf("merged-sweep %s: %w", path, err))
			continue
		}
		removed = append(removed, path)
	}
	return removed, errs
}

// DefaultMergedBranchPrefixes is the prefix allowlist SweepMergedBranches
// scans by default. It covers the conventional commit-style names the worker
// template prescribes (`fix/CW-...`, `feat/CW-...`, `task/CW-...`,
// `chore/CW-...`) plus the legacy short-form `cw/...` we still see in the
// wild. Branches outside this list are left alone — the PR-merged check is
// authoritative, but the prefix filter keeps the sweep from touching local
// scratch branches an operator may have created by hand.
var DefaultMergedBranchPrefixes = []string{
	"cw/",
	"cw-",
	"fix/CW-",
	"fix/cw-",
	"feat/CW-",
	"feat/cw-",
	"task/CW-",
	"task/cw-",
	"chore/CW-",
	"chore/cw-",
}

// SweepMergedBranches deletes local branches matching one of `prefixes` whose
// PR is in mergedSet AND which are not currently checked out by any worktree.
// Empty prefixes use DefaultMergedBranchPrefixes.
//
// Skips: branches with a worktree (we don't yank the rug out from a running
// run — SweepMergedPerRun handles the worktree case first), `main`/`master`
// (defensive — these should never match a prefix anyway), and branches not
// in mergedSet. Errors per-branch are collected; the sweep does not abort.
func SweepMergedBranches(repoRoot string, prefixes []string, mergedSet MergedSet) (deleted []string, errs []error) {
	if len(mergedSet) == 0 {
		return nil, nil
	}
	if len(prefixes) == 0 {
		prefixes = DefaultMergedBranchPrefixes
	}
	branches, err := localBranches(repoRoot)
	if err != nil {
		return nil, []error{fmt.Errorf("list local branches: %w", err)}
	}
	inUse, err := branchesInUseByWorktree(repoRoot)
	if err != nil {
		// Non-fatal: if we cannot resolve worktree branches we err on the
		// side of skipping the sweep entirely rather than risk pulling a
		// branch out from under an active run.
		return nil, []error{fmt.Errorf("list worktree branches: %w", err)}
	}
	for _, br := range branches {
		if br == "" || br == "main" || br == "master" || br == "HEAD" {
			continue
		}
		if !matchesAnyPrefix(br, prefixes) {
			continue
		}
		if !mergedSet.Has(br) {
			continue
		}
		if inUse[br] {
			continue
		}
		if err := runGit(repoRoot, "branch", "-D", br); err != nil {
			errs = append(errs, fmt.Errorf("delete branch %s: %w", br, err))
			continue
		}
		deleted = append(deleted, br)
	}
	return deleted, errs
}

// FetchMergedHeadRefs is a convenience wrapper that applies a default
// 30-second timeout to MergedHeadRefs. The scheduler's startup sweep and
// per-run cleanup call this so a wedged `gh` (e.g. a hung credential
// prompt) cannot block dispatch.
func FetchMergedHeadRefs(repoRoot string, limit int) (MergedSet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return MergedHeadRefs(ctx, repoRoot, limit)
}

// forceRemoveAndDeleteBranch removes the worktree at wtPath with --force
// (necessary because the worktree's branch has commits ahead of origin/main
// even though the PR has been squash-merged) and deletes the local branch
// with -D (same reason: -d refuses because the local SHA is not in origin/main).
func forceRemoveAndDeleteBranch(repoRoot, wtPath, branch string) error {
	if err := runGit(repoRoot, "worktree", "remove", "--force", wtPath); err != nil {
		return fmt.Errorf("worktree remove --force: %w", err)
	}
	if branch == "" {
		return nil
	}
	if err := runGit(repoRoot, "branch", "-D", branch); err != nil {
		// Branch may have already been removed (e.g. by `git worktree
		// remove --force` on a branch-checkout worktree). Treat as
		// success rather than fail the cleanup over an idempotency edge.
		return nil
	}
	return nil
}

// localBranches returns the names of all local branches (no `refs/heads/`
// prefix), suitable for direct comparison with MergedSet keys.
func localBranches(repoRoot string) ([]string, error) {
	out, err := runGitOutput(repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// branchesInUseByWorktree returns the set of local branch names currently
// checked out by some worktree (any worktree, not just per-run ones — a
// human-curated branch checkout must also be protected from the sweep).
func branchesInUseByWorktree(repoRoot string) (map[string]bool, error) {
	out, err := runGitOutput(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	inUse := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "branch ") {
			continue
		}
		ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		ref = strings.TrimPrefix(ref, "refs/heads/")
		if ref != "" {
			inUse[ref] = true
		}
	}
	return inUse, nil
}

func matchesAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
