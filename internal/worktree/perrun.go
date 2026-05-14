package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PerRunOptions configures the per-run worktree behaviour. These map to
// TORQUE_WORKTREE_* env vars resolved by the scheduler config layer.
type PerRunOptions struct {
	// Enabled is checked by callers; SetupPerRun does not gate on it.
	Enabled bool

	// Root is the parent directory where per-run worktrees are created.
	// If empty, defaults to "${repoRoot}-worktrees".
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

// SetupPerRun creates a per-run worktree under opts.Root for the given runID.
// It fetches origin and creates the worktree at origin/main. The agent is
// expected to create their own task branch from this clean checkout.
//
// Returns the worktree path on success. On any git failure the caller should
// fall back to running in workingDir directly — the run must not be blocked
// on worktree setup issues.
func SetupPerRun(opts PerRunOptions, workingDir string, runID int64) (string, error) {
	repoRoot, err := FindRepoRoot(workingDir)
	if err != nil {
		return "", err
	}
	root := opts.Root
	if root == "" {
		root = repoRoot + "-worktrees"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create worktree root %s: %w", root, err)
	}
	if err := runGit(repoRoot, "fetch", "origin"); err != nil {
		return "", fmt.Errorf("fetch origin: %w", err)
	}
	wtPath := filepath.Join(root, fmt.Sprintf("run-%d", runID))
	if err := runGit(repoRoot, "worktree", "add", "--detach", wtPath, "origin/main"); err != nil {
		return "", fmt.Errorf("worktree add: %w", err)
	}
	return wtPath, nil
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

// SweepPerRun removes worktrees under root whose mtime is older than
// keepDays AND whose worktree is clean (no commits ahead of origin/main,
// no uncommitted work). keepDays <= 0 disables the sweep. Errors on
// individual worktrees are logged via the returned error slice but do not
// abort the sweep.
func SweepPerRun(repoRoot, root string, keepDays int, now time.Time) (removed []string, errs []error) {
	if keepDays <= 0 {
		return nil, nil
	}
	if root == "" {
		root = repoRoot + "-worktrees"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read worktree root %s: %w", root, err)}
	}
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		path := filepath.Join(root, e.Name())
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
