package worktree

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// MergeRequest holds the parameters for executing a merge.
type MergeRequest struct {
	Policy       MergePolicy
	RepoPath     string
	SourceBranch string
	TargetBranch string
	WorktreePath string
}

// Merger executes merge policies against git branches.
type Merger struct{}

// NewMerger creates a merge executor.
func NewMerger() *Merger {
	return &Merger{}
}

// Execute runs the merge according to the specified policy.
func (m *Merger) Execute(ctx context.Context, req MergeRequest) (*MergeResult, error) {
	switch req.Policy {
	case MergePolicyNone:
		return m.execNone(ctx, req)
	case MergePolicyAuto:
		return m.execAuto(ctx, req)
	case MergePolicyPR:
		return m.execPR(ctx, req)
	case MergePolicyAutoResolve:
		return m.execAutoResolve(ctx, req)
	default:
		return nil, fmt.Errorf("unknown merge policy: %s", req.Policy)
	}
}

// execNone does nothing — the branch is left for human decision.
func (m *Merger) execNone(_ context.Context, _ MergeRequest) (*MergeResult, error) {
	return &MergeResult{
		Success:         true,
		NeedsResolution: false,
	}, nil
}

// execAuto attempts a fast-forward or merge. On conflict, it aborts
// and returns the conflict information.
func (m *Merger) execAuto(ctx context.Context, req MergeRequest) (*MergeResult, error) {
	// Checkout target branch in the main repo.
	if err := gitCmdCtx(ctx, req.RepoPath, "checkout", req.TargetBranch); err != nil {
		return nil, fmt.Errorf("checkout target: %w", err)
	}

	// Attempt merge.
	mergeOut, mergeErr := gitCmdOutput(ctx, req.RepoPath, "merge", "--no-ff", req.SourceBranch)
	if mergeErr == nil {
		// Merge succeeded — get the commit hash.
		commit, _ := gitCmdOutput(ctx, req.RepoPath, "rev-parse", "HEAD")
		return &MergeResult{
			Success:      true,
			MergedCommit: strings.TrimSpace(commit),
		}, nil
	}

	// Merge failed — check if it's a conflict.
	conflictFiles := m.parseConflicts(ctx, req.RepoPath)

	// Abort the merge to leave the repo clean.
	_ = gitCmdCtx(ctx, req.RepoPath, "merge", "--abort")

	if len(conflictFiles) > 0 {
		return &MergeResult{
			Success:         false,
			ConflictFiles:   conflictFiles,
			NeedsResolution: true,
			ErrorMessage:    "merge conflict",
		}, nil
	}

	return &MergeResult{
		Success:      false,
		ErrorMessage: fmt.Sprintf("merge failed: %s", mergeOut),
	}, nil
}

// execPR is a placeholder — PR creation requires a GitHub/GitLab plugin.
// It returns success with a note that the branch is ready for PR.
func (m *Merger) execPR(_ context.Context, req MergeRequest) (*MergeResult, error) {
	return &MergeResult{
		Success:      true,
		ErrorMessage: fmt.Sprintf("branch %s ready for PR to %s (requires plugin-github)", req.SourceBranch, req.TargetBranch),
	}, nil
}

// execAutoResolve attempts merge, and on conflict returns resolution info
// instead of simply blocking. The caller (scheduler) uses this to create
// a resolution task.
func (m *Merger) execAutoResolve(ctx context.Context, req MergeRequest) (*MergeResult, error) {
	// First try auto merge.
	result, err := m.execAuto(ctx, req)
	if err != nil {
		return nil, err
	}

	// If auto merge succeeded, we're done.
	if result.Success {
		return result, nil
	}

	// If there are conflicts, return them flagged for resolution.
	// The caller will create a resolution task.
	return &MergeResult{
		Success:         false,
		ConflictFiles:   result.ConflictFiles,
		NeedsResolution: true,
		ErrorMessage:    "merge conflict — resolution agent required",
	}, nil
}

// parseConflicts extracts conflicting file paths from the repo state.
func (m *Merger) parseConflicts(ctx context.Context, repoPath string) []string {
	out, err := gitCmdOutput(ctx, repoPath, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// gitCmdCtx runs a git command with context support.
func gitCmdCtx(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s: %w", args, string(out), err)
	}
	return nil
}

// gitCmdOutput runs a git command and returns stdout.
func gitCmdOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stderr.String(), fmt.Errorf("git %v: %s: %w", args, stderr.String(), err)
	}
	return stdout.String(), nil
}
