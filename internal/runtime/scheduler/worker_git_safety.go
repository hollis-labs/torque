package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// RunGitState is the part of a run's git state the engine compares before
// and after a ModeLongLived worker runs: the repository the run directory
// belongs to, the commit it was at, and its remotes (CW-20260918-0009).
//
// A worker once found no remote, inferred one from the project name, hit
// an unrelated populated repository of that name, reset onto it with `git
// reset --hard origin/main`, and opened a PR that would have deleted ~29.6k
// lines. The contract tells workers never to add or repoint a remote and to
// stop on unrelated history; this state lets the engine notice when one did
// anyway. It is a snapshot taken in memory before Boot, so it works in
// shared mode as well as in a per-run worktree, and a remote the worker
// changes inside a worktree (worktrees share the repository's config) is
// caught all the same.
type RunGitState struct {
	// TopLevel is the repository root `git rev-parse --show-toplevel`
	// reports for the run directory; "" when it is in no repository.
	TopLevel string
	// Head is the commit HEAD resolved to; "" for an unborn branch or no
	// repository.
	Head string
	// Remotes maps "<name> (fetch)" / "<name> (push)" to the URL, as
	// `git remote -v` lists them. Empty outside a repository; nil when
	// the listing failed, which CheckRunGitSafety treats as unknown.
	Remotes map[string]string
}

// SnapshotRunGit records workdir's RunGitState. It never fails: a directory
// outside any repository, or a git error, yields the corresponding empty
// fields, and CheckRunGitSafety compares like with like.
func SnapshotRunGit(ctx context.Context, workdir string) RunGitState {
	none := RunGitState{Remotes: map[string]string{}}
	if workdir == "" {
		return none
	}
	top, err := gitOutput(ctx, workdir, "rev-parse", "--show-toplevel")
	if err != nil {
		return none
	}
	state := RunGitState{TopLevel: top}
	if head, err := gitOutput(ctx, workdir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		state.Head = head
	}
	if out, err := gitOutput(ctx, workdir, "remote", "-v"); err == nil {
		state.Remotes = map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			// "<name>\t<url> (fetch)"
			name, rest, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			i := strings.LastIndex(rest, " (")
			if i < 0 {
				continue
			}
			state.Remotes[name+rest[i:]] = rest[:i]
		}
	}
	return state
}

// CheckRunGitSafety compares workdir's git state after the worker ran with
// before, the snapshot taken ahead of Boot, and returns the reason to park
// the task in blocked when the run did something the worker contract
// forbids:
//
//   - a remote was added, removed or pointed at a different URL. Torque
//     dispatches a worker into the project's repository with the remote it
//     already has; a new or changed remote is how a push reaches a
//     repository nobody tied to the project.
//   - HEAD shares no history with the commit the run started from: the
//     branch was reset or rebuilt onto unrelated history.
//
// It returns "" when neither applies, and also when the state after the
// run cannot be read (a vanished directory, a git error), so a degraded
// environment does not block a working run.
func CheckRunGitSafety(ctx context.Context, workdir string, before RunGitState) string {
	if workdir == "" {
		return ""
	}
	after := SnapshotRunGit(ctx, workdir)
	if after.TopLevel == "" {
		// Not a repository now: nothing this run could push from.
		return ""
	}
	var problems []string
	if before.Remotes != nil && after.Remotes != nil {
		if diff := diffRemotes(before.Remotes, after.Remotes); diff != "" {
			problems = append(problems, "remotes changed during the run ("+diff+")")
		}
	}
	if before.Head != "" && after.Head != "" && before.TopLevel == after.TopLevel && before.Head != after.Head {
		if unrelated, err := noCommonHistory(ctx, workdir, before.Head, after.Head); err == nil && unrelated {
			problems = append(problems, fmt.Sprintf("HEAD %s shares no history with %s, the commit the run started from", shortSHA(after.Head), shortSHA(before.Head)))
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return "worker git safety: " + strings.Join(problems, "; ") +
		". A worker pushes only to the remote the project's repository already has and stops on unrelated history; " +
		"check what this run pushed, and where, before re-queueing (CW-20260918-0009)"
}

// diffRemotes lists added, removed and repointed remote URLs, in a stable
// order; "" when the two sets match.
func diffRemotes(before, after map[string]string) string {
	keys := map[string]struct{}{}
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var changes []string
	for _, k := range sorted {
		was, hadBefore := before[k]
		now, hasAfter := after[k]
		switch {
		case !hadBefore:
			changes = append(changes, fmt.Sprintf("added %s %s", k, now))
		case !hasAfter:
			changes = append(changes, fmt.Sprintf("removed %s %s", k, was))
		case was != now:
			changes = append(changes, fmt.Sprintf("%s %s -> %s", k, was, now))
		}
	}
	return strings.Join(changes, ", ")
}

// noCommonHistory reports whether commits a and b have no merge base.
// `git merge-base` exits 1 with no output in exactly that case; any other
// failure is returned as an error.
func noCommonHistory(ctx context.Context, dir, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", a, b)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(string(out)) == "" {
		return true, nil
	}
	return false, err
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
