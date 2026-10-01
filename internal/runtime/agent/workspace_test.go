package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceCreateMaterializesTree verifies the durable per-session dir
// tree is created and the four roots are populated.
func TestWorkspaceCreateMaterializesTree(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()

	ws, err := WorkspaceCreate(root, "proj-A", "sess-123", repo, repo)
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}

	wantWorkspace := filepath.Join(root, "proj-A", "sess-123")
	if ws.WorkspaceDir != wantWorkspace {
		t.Errorf("WorkspaceDir = %q, want %q", ws.WorkspaceDir, wantWorkspace)
	}
	for _, d := range []string{ws.WorkspaceDir, ws.PromptDir, ws.StateDir, ws.LogDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected dir %q to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
	if ws.LogPath != filepath.Join(ws.LogDir, "session.log") {
		t.Errorf("LogPath = %q, want %q", ws.LogPath, filepath.Join(ws.LogDir, "session.log"))
	}
}

// TestWorkspaceCreateFourRootsSharedMode: when workRoot == repoRoot the layout
// reflects shared mode and BuildDirRoot is the named $TMPDIR/torque-boot.
func TestWorkspaceCreateFourRootsSharedMode(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()

	ws, err := WorkspaceCreate(root, "proj", "sess", repo, repo)
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}
	if ws.RepoRoot != repo {
		t.Errorf("RepoRoot = %q, want %q", ws.RepoRoot, repo)
	}
	if ws.WorkRoot != repo {
		t.Errorf("WorkRoot = %q, want %q (shared mode: work_root == repo_root)", ws.WorkRoot, repo)
	}
	if ws.BuildDirRoot != DefaultBuildDirRoot() {
		t.Errorf("BuildDirRoot = %q, want %q", ws.BuildDirRoot, DefaultBuildDirRoot())
	}
	if !strings.Contains(ws.BuildDirRoot, BuildDirRootName) {
		t.Errorf("BuildDirRoot %q must contain %q for forensic tooling", ws.BuildDirRoot, BuildDirRootName)
	}
}

// TestWorkspaceCreateWorktreeMode: when workRoot differs from repoRoot (the
// per-run worktree case) the layout keeps RepoRoot and WorkRoot distinct.
func TestWorkspaceCreateWorktreeMode(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "run-7")

	ws, err := WorkspaceCreate(root, "proj", "sess", repo, worktreePath)
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}
	if ws.RepoRoot != repo {
		t.Errorf("RepoRoot = %q, want %q", ws.RepoRoot, repo)
	}
	if ws.WorkRoot != worktreePath {
		t.Errorf("WorkRoot = %q, want %q (worktree mode)", ws.WorkRoot, worktreePath)
	}
	// The durable WorkspaceDir must live OUTSIDE the worktree so worktree
	// cleanup never destroys session logs/state.
	if strings.HasPrefix(ws.WorkspaceDir, worktreePath) {
		t.Errorf("WorkspaceDir %q must not live inside the worktree %q", ws.WorkspaceDir, worktreePath)
	}
}

// TestWorkspaceCreateEmptyWorkRootDefaultsToRepoRoot: callers that don't
// resolve a worktree pass an empty workRoot and get shared-mode behaviour.
func TestWorkspaceCreateEmptyWorkRootDefaultsToRepoRoot(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()

	ws, err := WorkspaceCreate(root, "proj", "sess", repo, "")
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}
	if ws.WorkRoot != repo {
		t.Errorf("empty workRoot should default to repoRoot: WorkRoot = %q, want %q", ws.WorkRoot, repo)
	}
}

// TestWorkspaceCreateUnscopedProjectKey: an empty projectID maps to "unscoped".
func TestWorkspaceCreateUnscopedProjectKey(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()

	ws, err := WorkspaceCreate(root, "", "sess", repo, repo)
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}
	if ws.WorkspaceDir != filepath.Join(root, "unscoped", "sess") {
		t.Errorf("WorkspaceDir = %q, want unscoped key", ws.WorkspaceDir)
	}
}

// In a test, a workspaces root inside the real ~/.torque is refused, so a
// test whose Dependencies leave WorkspacesRoot empty fails loudly instead of
// writing into the operator's ~/.torque/workspaces (CW-20261001-0175), and
// nothing is created there. Elsewhere under $HOME is allowed: a $TMPDIR
// under $HOME must keep working for t.TempDir(). The cases that create
// dirs run against a stand-in home, so the test never writes to the real
// one.
func TestWorkspaceCreateRefusesTheRealTorqueDirInTests(t *testing.T) {
	if startHome != "" {
		err := refuseRealHomeInTests(filepath.Join(startHome, ".torque", "workspaces"))
		if err == nil || !strings.Contains(err.Error(), "testenv.WorkspacesRoot(t)") {
			t.Fatalf("the real default root: err = %v, want a refusal naming testenv.WorkspacesRoot(t)", err)
		}
	}

	home := t.TempDir()
	prev := startHome
	startHome = home
	t.Cleanup(func() { startHome = prev })
	t.Setenv("HOME", home)

	if _, err := WorkspaceCreate("", "unscoped", "SES-GUARD", t.TempDir(), ""); err == nil {
		t.Fatal("the default root (~/.torque/workspaces) must be refused")
	}
	if _, err := os.Stat(filepath.Join(home, ".torque")); !os.IsNotExist(err) {
		t.Fatalf("the refused workspace was created anyway (stat err %v)", err)
	}
	if _, err := WorkspaceCreate(filepath.Join(home, ".torque", "elsewhere"), "p", "SES-GUARD", t.TempDir(), ""); err == nil {
		t.Fatal("an explicit root inside ~/.torque must be refused too")
	}
	for _, root := range []string{
		filepath.Join(home, ".cache", "task-tmp", "workspaces"), // under $HOME, outside ~/.torque
		filepath.Join(home, ".torque-other"),                    // a sibling that only shares the prefix
		t.TempDir(),
	} {
		if _, err := WorkspaceCreate(root, "p", "SES-GUARD", t.TempDir(), ""); err != nil {
			t.Fatalf("root %s must be accepted: %v", root, err)
		}
	}
}
