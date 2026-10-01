package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepoWithOrigin is a repository with one commit on main and an origin
// remote, the shape Torque dispatches a worker into.
func gitRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := makeGitRepoWithCommits(t, 0)
	mustGit(t, dir, "remote", "add", "origin", originURL)
	return dir
}

func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-m", "add "+name)
}

func TestCheckRunGitSafety_OrdinaryRunPasses(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	before := SnapshotRunGit(ctx, dir)
	require.NotEmpty(t, before.Head)
	assert.Equal(t, "https://git.example.com/acme/app.git", before.Remotes["origin (fetch)"])

	mustGit(t, dir, "checkout", "-b", "fix/CW-1-thing")
	commitFile(t, dir, "a.txt", "a\n")
	commitFile(t, dir, "b.txt", "b\n")

	assert.Empty(t, CheckRunGitSafety(ctx, dir, before))
}

// The CW-20260918-0001 incident: the run directory was not a repository
// yet, and the worker created one and added a remote it inferred from the
// project name.
func TestCheckRunGitSafety_RemoteAddedToNewRepository(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	before := SnapshotRunGit(ctx, dir)
	require.Empty(t, before.TopLevel)

	mustGit(t, dir, "init", "--initial-branch=main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test Runner")
	commitFile(t, dir, "README.md", "scaffold\n")
	mustGit(t, dir, "remote", "add", "origin", "https://git.example.com/acme/tachyon.git")

	reason := CheckRunGitSafety(ctx, dir, before)
	assert.Contains(t, reason, "added origin (fetch) https://git.example.com/acme/tachyon.git")
	assert.Contains(t, reason, "added origin (push) https://git.example.com/acme/tachyon.git")
	assert.Contains(t, reason, "CW-20260918-0009")
}

func TestCheckRunGitSafety_RemoteRepointed(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	before := SnapshotRunGit(ctx, dir)

	mustGit(t, dir, "remote", "set-url", "origin", "https://git.example.com/acme/other.git")

	reason := CheckRunGitSafety(ctx, dir, before)
	assert.Contains(t, reason, "origin (fetch) https://git.example.com/acme/app.git -> https://git.example.com/acme/other.git")
}

func TestCheckRunGitSafety_ExtraRemoteAdded(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	before := SnapshotRunGit(ctx, dir)

	mustGit(t, dir, "remote", "add", "upstream", "https://git.example.com/someone/app.git")

	assert.Contains(t, CheckRunGitSafety(ctx, dir, before), "added upstream (fetch) https://git.example.com/someone/app.git")
}

// The other half of the incident: the worker reset its branch onto a
// populated repository that shares no history with the project's.
func TestCheckRunGitSafety_ResetOntoUnrelatedHistory(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	before := SnapshotRunGit(ctx, dir)

	unrelated := t.TempDir()
	mustGit(t, unrelated, "init", "--initial-branch=main")
	mustGit(t, unrelated, "config", "user.email", "someone@example.com")
	mustGit(t, unrelated, "config", "user.name", "Someone Else")
	commitFile(t, unrelated, "app.go", "package app\n")
	mustGit(t, dir, "fetch", unrelated, "main")
	mustGit(t, dir, "reset", "--hard", "FETCH_HEAD")
	commitFile(t, dir, "wipe.txt", "replacement\n")

	reason := CheckRunGitSafety(ctx, dir, before)
	assert.Contains(t, reason, "shares no history with "+shortSHA(before.Head))
	assert.NotContains(t, reason, "remotes changed")
}

// Moving to other work that shares history (an existing PR branch, a
// rebase onto a newer main) is not unrelated history.
func TestCheckRunGitSafety_RelatedHistoryPasses(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	mustGit(t, dir, "checkout", "-b", "older-pr")
	commitFile(t, dir, "pr.txt", "pr\n")
	mustGit(t, dir, "checkout", "main")
	commitFile(t, dir, "newer.txt", "main moved\n")
	before := SnapshotRunGit(ctx, dir)

	mustGit(t, dir, "checkout", "older-pr")

	assert.Empty(t, CheckRunGitSafety(ctx, dir, before))
}

func TestCheckRunGitSafety_UnreadableStatePasses(t *testing.T) {
	ctx := context.Background()
	dir := gitRepoWithOrigin(t, "https://git.example.com/acme/app.git")
	before := SnapshotRunGit(ctx, dir)
	require.NoError(t, os.RemoveAll(dir))

	// The directory is gone: the after-state reads as no repository and no
	// remotes. That is a degraded environment, not a worker adding or
	// removing remotes, so it must not block.
	assert.Empty(t, CheckRunGitSafety(ctx, dir, before))
	assert.Empty(t, CheckRunGitSafety(ctx, "", before))
}
