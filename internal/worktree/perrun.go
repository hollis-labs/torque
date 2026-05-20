package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// PerRunOptions configures the per-run worktree behaviour. These map to
// TORQUE_WORKTREE_* env vars resolved by the scheduler config layer.
type PerRunOptions struct {
	// Enabled is checked by callers; SetupPerRun does not gate on it.
	Enabled bool

	// Root is the explicit parent directory where per-run worktrees are
	// created (the TORQUE_WORKTREE_ROOT operator override). When empty the
	// per-run worktree is placed as a true sibling of the repo root at the
	// SAME directory depth — see PerRunPath. When set, it is honoured as-is
	// and the worktree leaf is "${Root}/run-<id>".
	Root string

	// KeepDays is the TTL for orphaned worktrees retained because they had
	// uncommitted work or commits ahead of origin/main. 0 = keep forever.
	KeepDays int
}

// FindRepoRoot walks up from start looking for a .git directory or file
// (file form covers worktrees themselves) and returns the absolute path of
// the directory that contains it. Returns an error if no .git is found.
func FindRepoRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no .git found at or above %s", start)
		}
		cur = parent
	}
}

// PerRunPath computes the absolute path of the per-run worktree for the given
// repo root, explicit root override, and run ID.
//
//   - root == "" (default): the worktree is a TRUE SIBLING of the repo, at the
//     SAME directory depth — "${repoParent}/${repoName}-worktrees-run-<id>".
//     This is the placement that keeps relative go.mod replace directives
//     ("replace ../../x => ...") resolving identically from the worktree's
//     go.mod and from the repo's go.mod, because both go.mod files sit at the
//     same depth under a common parent.
//   - root != "" (TORQUE_WORKTREE_ROOT override): honoured as-is — the leaf is
//     "${root}/run-<id>". Operators who set this take responsibility for the
//     depth; the relative-replace guard (see CheckRelativeReplaceSafe) flags
//     the case where this override would mis-resolve relative replaces.
func PerRunPath(repoRoot, root string, runID int64) string {
	if root != "" {
		return filepath.Join(root, fmt.Sprintf("run-%d", runID))
	}
	repoParent := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)
	return filepath.Join(repoParent, fmt.Sprintf("%s-worktrees-run-%d", repoName, runID))
}

// SetupPerRun creates a per-run worktree for the given runID. When the repo
// has an `origin` remote it refreshes it and branches the worktree from the
// freshest published ref; a local-only repo (no `origin`) branches from the
// repo's local HEAD instead. The agent is expected to create their own task
// branch from this clean checkout.
//
// Placement: with an empty opts.Root the worktree is a true sibling of the
// repo root (same directory depth — see PerRunPath); with opts.Root set the
// leaf is "${opts.Root}/run-<id>".
//
// Before creating the worktree, SetupPerRun runs the relative-replace guard
// (CheckRelativeReplaceSafe): if the repo's go.mod carries relative ("../")
// replace directives and the chosen placement would NOT preserve them, it
// returns a clear blocking error instead of silently mis-resolving.
//
// Base-ref policy: the worktree is detached at the first of `origin/main`,
// `origin/HEAD`, or local `HEAD` that resolves (see perRunBaseRef). A missing
// `origin` remote, an offline `git fetch`, or a default branch that isn't
// `main` therefore degrades the *freshness* of the checkout — never whether
// the run gets an isolated worktree at all. This is deliberate: silently
// abandoning worktree isolation for a no-origin repo is exactly the kind of
// unpredictable per-run surprise the per-run-worktree contract exists to kill.
//
// Returns the worktree path on success. On any git failure the caller should
// fall back to running in workingDir directly — the run must not be blocked
// on worktree setup issues.
func SetupPerRun(opts PerRunOptions, workingDir string, runID int64) (string, error) {
	repoRoot, err := FindRepoRoot(workingDir)
	if err != nil {
		return "", err
	}
	wtPath := PerRunPath(repoRoot, opts.Root, runID)
	if err := CheckRelativeReplaceSafe(repoRoot, wtPath); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent %s: %w", filepath.Dir(wtPath), err)
	}
	// Refresh origin when the repo has one — best-effort. A fetch failure
	// (offline, auth) must not block dispatch: perRunBaseRef falls back to a
	// local ref below.
	if hasOriginRemote(repoRoot) {
		_ = runGit(repoRoot, "fetch", "origin")
	}
	base := perRunBaseRef(repoRoot)
	if err := runGit(repoRoot, "worktree", "add", "--detach", wtPath, base); err != nil {
		return "", fmt.Errorf("worktree add (base %s): %w", base, err)
	}
	return wtPath, nil
}

// hasOriginRemote reports whether the repo has a remote named "origin".
func hasOriginRemote(repoRoot string) bool {
	out, err := runGitOutput(repoRoot, "remote")
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(out), "origin")
}

// perRunBaseRef picks the ref a per-run worktree is detached at. Preference
// order: the freshly-fetched `origin/main`, then origin's published default
// branch (`origin/HEAD`), then the repo's local `HEAD`. The fallbacks ensure
// a worktree is always createable — for a local-only repo, an offline clone,
// or a repo whose default branch is not `main`.
func perRunBaseRef(repoRoot string) string {
	for _, ref := range []string{"origin/main", "origin/HEAD"} {
		if runGit(repoRoot, "rev-parse", "--verify", "--quiet", ref) == nil {
			return ref
		}
	}
	return "HEAD"
}

// goModReplaceRelRe matches a relative ("../" or "./") replace TARGET in a
// go.mod replace directive. It is intentionally permissive: any line that
// looks like "replace ... => <rel-path>" or a "=> <rel-path>" entry inside a
// replace block. The guard only needs to know "are there relative replaces".
var goModReplaceRelRe = regexp.MustCompile(`=>\s+(\.\.?/[^\s]*)`)

// hasRelativeReplace reports whether the go.mod text contains at least one
// replace directive whose target is a relative path ("../" or "./"). Relative
// targets are the only ones whose resolution depends on the depth of the
// go.mod file, so they are the only ones the worktree-depth guard cares about.
func hasRelativeReplace(goModText string) bool {
	return goModReplaceRelRe.MatchString(goModText)
}

// CheckRelativeReplaceSafe is the relative-replace guard. It parses the repo's
// go.mod and, if that go.mod contains replace directives with relative ("../")
// targets, verifies the chosen worktree placement preserves them — i.e. the
// worktree's go.mod sits at the SAME directory depth as the repo's go.mod, so
// a relative replace resolves to the same tree from both.
//
// Decision rule: a placement is safe for relative replaces iff the worktree
// directory and the repo root share the same parent directory (they are true
// siblings). The default placement (PerRunPath with empty root) always
// satisfies this. An explicit TORQUE_WORKTREE_ROOT override may not — when it
// doesn't, this returns a clear blocking error naming the problem rather than
// letting the worktree silently mis-resolve its relative replaces.
//
// When the repo has no go.mod, or its go.mod has no relative replaces, the
// guard is a no-op and returns nil — any placement is safe.
func CheckRelativeReplaceSafe(repoRoot, wtPath string) error {
	b, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		// No go.mod (or unreadable) — nothing depth-sensitive to protect.
		return nil
	}
	if !hasRelativeReplace(string(b)) {
		return nil
	}
	repoParent := filepath.Dir(filepath.Clean(repoRoot))
	wtParent := filepath.Dir(filepath.Clean(wtPath))
	if repoParent == wtParent {
		// Worktree is a true sibling of the repo: same depth, relative
		// replaces resolve identically. Safe.
		return nil
	}
	return fmt.Errorf(
		"per-run worktree placement %s would break relative go.mod replace directives: "+
			"its go.mod sits at a different directory depth than the repo go.mod at %s "+
			"(worktree parent %s != repo parent %s). "+
			"A relative \"replace ../...\" target would resolve to the wrong tree. "+
			"Unset TORQUE_WORKTREE_ROOT to use the default sibling placement, or set it "+
			"to a directory at the same depth as the repo so the worktree is a true sibling",
		wtPath, repoRoot, wtParent, repoParent,
	)
}

// CleanupPerRun removes the per-run worktree at wtPath if it has no
// uncommitted work and no commits ahead of origin/main. Returns (true, nil)
// when the worktree was removed; (false, nil) when it was preserved (and a
// human should inspect); a non-nil error only on unexpected failures.
func CleanupPerRun(workingDir, wtPath string) (bool, error) {
	repoRoot, err := FindRepoRoot(workingDir)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		// Already gone (or never created). Run prune to drop any stale
		// admin entry but don't treat as an error.
		_ = runGit(repoRoot, "worktree", "prune")
		return false, nil
	}
	dirty, err := worktreeHasWork(wtPath)
	if err != nil {
		return false, err
	}
	if dirty {
		return false, nil
	}
	if err := runGit(repoRoot, "worktree", "remove", wtPath); err != nil {
		return false, fmt.Errorf("worktree remove: %w", err)
	}
	return true, nil
}

// SweepPerRun removes orphaned per-run worktrees older than keepDays whose
// worktree is clean (no commits ahead of origin/main, no uncommitted work).
// keepDays <= 0 disables the sweep.
//
// Placement-aware: when root is empty, per-run worktrees are true siblings of
// the repo named "${repoName}-worktrees-run-*" under the repo's parent
// directory; when root is set they are "run-*" leaves directly under root.
// SweepPerRun scans the matching location/prefix accordingly. Errors on
// individual worktrees are collected in the returned slice but do not abort
// the sweep.
func SweepPerRun(repoRoot, root string, keepDays int, now time.Time) (removed []string, errs []error) {
	if keepDays <= 0 {
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
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path := filepath.Join(scanDir, e.Name())
		info, err := e.Info()
		if err != nil {
			errs = append(errs, fmt.Errorf("stat %s: %w", path, err))
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		dirty, err := worktreeHasWork(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", path, err))
			continue
		}
		if dirty {
			continue
		}
		if err := runGit(repoRoot, "worktree", "remove", path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
			continue
		}
		removed = append(removed, path)
	}
	return removed, errs
}

// perRunScanLocation returns the (scanDir, leafPrefix) pair the per-run
// sweepers walk to find per-run worktrees. Placement-aware: with an empty
// `root` the worktrees are true siblings of the repo, so the parent dir is
// scanned for `${repoName}-worktrees-run-*` leaves; with `root` set the
// scan is direct children of that root with a `run-` prefix. Centralized
// so SweepPerRun (TTL-based) and SweepMergedPerRun (PR-state-based) cannot
// drift from each other's placement assumptions.
func perRunScanLocation(repoRoot, root string) (scanDir, prefix string) {
	if root != "" {
		return root, "run-"
	}
	parent := filepath.Dir(filepath.Clean(repoRoot))
	return parent, filepath.Base(filepath.Clean(repoRoot)) + "-worktrees-run-"
}

// worktreeHasWork returns true if the worktree at path has uncommitted
// changes (status --porcelain output) OR commits ahead of origin/main.
func worktreeHasWork(wtPath string) (bool, error) {
	out, err := runGitOutput(wtPath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return true, nil
	}
	out, err = runGitOutput(wtPath, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		// origin/main may not be present in some odd configurations; treat
		// the inability to compare as "no extra commits" rather than fail
		// the cleanup. The status check above already caught uncommitted work.
		return false, nil
	}
	count := strings.TrimSpace(out)
	return count != "" && count != "0", nil
}

func runGit(dir string, args ...string) error {
	_, err := runGitOutput(dir, args...)
	return err
}

func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v in %s: %s: %w", args, dir, strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}
