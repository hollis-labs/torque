package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuildDirRootName is the basename of the parent directory under $TMPDIR that
// holds planted provider boot dirs. Kept as a named constant (not an inline
// literal) so the four-root model has one source of truth for build_dir's
// root; the substring "torque-boot" is load-bearing for cross-app forensic
// tooling (`find /var/folders -path '*torque-boot*'`).
const BuildDirRootName = "torque-boot"

// DefaultBuildDirRoot returns $TMPDIR/torque-boot — the parent directory under
// which go-agent-launch materializes per-run provider boot dirs (launcher.Prepare
// allocates the dir, providerplant.Plant renders the BootDirSpec into it). This
// is the root of build_dir in the four-root model; the per-run leaf basename is
// agentlaunch-bootdir-<planhash>-* (named by go-agent-launch). Torque owns the
// leaf's lifecycle — cleanup is consumer-driven, not lib-driven.
func DefaultBuildDirRoot() string {
	return filepath.Join(os.TempDir(), BuildDirRootName)
}

// WorkspaceLayout is Torque's named representation of the shared four-root
// directory model for one agent session/run. It is the substrate Stage 2
// (go-agent-launch integration) builds on — callers thread a single
// WorkspaceLayout instead of passing loose path strings around.
//
// The four roots:
//
//	RepoRoot     — the canonical project checkout, read-mostly. In Torque this
//	               is Options.Workdir (the project root passed to Boot).
//	WorkRoot     — the per-launch writable dir the agent actually executes in.
//	               When worktree mode is OFF, WorkRoot == RepoRoot. When ON it
//	               is the per-run git worktree from worktree.SetupPerRun. The
//	               scheduler owns per-run worktree creation today; Boot itself
//	               only records the value it is handed.
//	WorkspaceDir — the per-session state/logs/metadata root. This is the dir
//	               tree materialized by WorkspaceCreate
//	               (~/.torque/workspaces/<projectKey>/<sessID>/). It lives
//	               OUTSIDE any worktree so durable logs/state survive worktree
//	               cleanup.
//	BuildDir     — the planted provider boot directory. BuildDirRoot is
//	               $TMPDIR/torque-boot; the per-run leaf (agentlaunch-bootdir-*)
//	               is planted by go-agent-launch (launcher.Prepare +
//	               providerplant.Plant) before the session starts and captured
//	               by Boot. Torque owns the leaf's cleanup. May be empty for
//	               adapters with no BootDirSpec.
//
// The sub-paths (PromptDir / StateDir / LogDir / LogPath) are children of
// WorkspaceDir and carry the existing per-session dir tree.
type WorkspaceLayout struct {
	// RepoRoot is the canonical project checkout (read-mostly).
	RepoRoot string

	// WorkRoot is the writable dir the agent executes in. Equals RepoRoot in
	// shared mode; a per-run git worktree in worktree mode.
	WorkRoot string

	// WorkspaceDir is the per-session state/logs root
	// (~/.torque/workspaces/<projectKey>/<sessID>).
	WorkspaceDir string

	// PromptDir is <WorkspaceDir>/prompts (reserved — see WorkspaceCreate doc).
	PromptDir string

	// StateDir is <WorkspaceDir>/state (reserved — see WorkspaceCreate doc).
	StateDir string

	// LogDir is <WorkspaceDir>/logs.
	LogDir string

	// LogPath is <WorkspaceDir>/logs/session.log.
	LogPath string

	// BuildDirRoot is $TMPDIR/torque-boot — the parent dir for planted boot
	// dirs. Always populated.
	BuildDirRoot string

	// BuildDir is the concrete planted provider boot dir for this run
	// (agentlaunch-bootdir-*), planted by go-agent-launch's providerplant.Plant
	// before the session starts. Empty for adapters with no BootDirSpec.
	BuildDir string
}

// WorkspaceCreate materializes the per-session WorkspaceDir tree and returns a
// WorkspaceLayout with the four roots populated.
//
// Inputs:
//
//	workspacesRoot — parent for <projectKey>/<sessID>; defaults to
//	                 $HOME/.torque/workspaces when empty.
//	projectID      — projectKey component; "unscoped" when empty.
//	sessID         — the generated session ID.
//	repoRoot       — RepoRoot for the layout (Options.Workdir at the call site).
//	workRoot       — the writable WorkRoot; pass repoRoot in shared mode or the
//	                 per-run worktree path in worktree mode. Empty defaults to
//	                 repoRoot so callers that don't (yet) resolve a worktree
//	                 keep the shared-mode behaviour.
//
// The persistent dir tree under WorkspaceDir is:
//
//	<WorkspaceDir>/
//	├── prompts/         (reserved — torque does not yet mirror boot.md here)
//	├── state/           (reserved — resume hint + plan snapshot mirroring
//	│                     deferred to a follow-up)
//	└── logs/
//	    └── session.log  (PTY runtime / stderr tee write here)
//
// Two-dir model rationale (see cross-app design §5): build_dir is ephemeral
// (cleaned on session done); WorkspaceDir is durable so logs survive
// post-mortem. WorkspaceDir lives outside any per-run worktree, so worktree
// cleanup never destroys session logs/state.
//
// 0o700 (user-private) for the workspace tree — these dirs hold logs and
// reserved state with potentially sensitive content (planted prompts, tool
// payloads, resume hints).
func WorkspaceCreate(workspacesRoot, projectID, sessID, repoRoot, workRoot string) (*WorkspaceLayout, error) {
	if workspacesRoot == "" {
		// Default to $HOME/.torque/workspaces; matches the convention in
		// the mux daemon path and nanite's chat sessions.
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve $HOME for workspaces root: %w", err)
		}
		workspacesRoot = filepath.Join(home, ".torque", "workspaces")
	}
	projectKey := projectID
	if projectKey == "" {
		projectKey = "unscoped"
	}
	if workRoot == "" {
		workRoot = repoRoot
	}
	root := filepath.Join(workspacesRoot, projectKey, sessID)

	dirs := []string{
		root,
		filepath.Join(root, "prompts"),
		filepath.Join(root, "state"),
		filepath.Join(root, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("mkdir workspace %s: %w", d, err)
		}
	}

	return &WorkspaceLayout{
		RepoRoot:     repoRoot,
		WorkRoot:     workRoot,
		WorkspaceDir: root,
		PromptDir:    filepath.Join(root, "prompts"),
		StateDir:     filepath.Join(root, "state"),
		LogDir:       filepath.Join(root, "logs"),
		LogPath:      filepath.Join(root, "logs", "session.log"),
		BuildDirRoot: DefaultBuildDirRoot(),
	}, nil
}
