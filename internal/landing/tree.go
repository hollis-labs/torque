package landing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckClean reports why a task's working directory is not safe to call done:
// uncommitted changes would be lost with the checkout. A blank dir, a missing
// dir (a merged task's per-run worktree is removed) or a non-git dir has
// nothing to check and returns nil. A git failure is reported, never ignored.
func CheckClean(ctx context.Context, dir string) []string {
	missing, _ := CheckCleanState(ctx, dir)
	return missing
}

// CheckCleanState is CheckClean that also says whether the answer is only
// "could not tell": undecided is true when git status could not be read, false
// when the tree plainly has uncommitted changes.
func CheckCleanState(ctx context.Context, dir string) (missing []string, undecided bool) {
	if dir == "" {
		return nil, false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if out, err := probe.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return []string{fmt.Sprintf("cannot read git status in %s: %v", dir, err)}, true
	}
	status := strings.TrimRight(string(out), "\n")
	if status == "" {
		return nil, false
	}
	n := strings.Count(status, "\n") + 1
	return []string{fmt.Sprintf("%d uncommitted change(s) in %s (commit or discard them)", n, dir)}, false
}
