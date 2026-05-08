package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// workspace describes the persistent per-session dir tree:
//
//	<WorkspacesRoot>/<projectKey>/<sessID>/
//	├── prompts/         (boot.md mirrored here for post-mortem)
//	├── state/           (resume hint, plan snapshot)
//	└── logs/
//	    ├── session.log  (PTY runtime writes here when LogPath empty)
//	    └── events.jsonl
//
// projectKey is opts.ProjectID when non-empty, else "unscoped". sessID is the
// generated session ID. Caller passes these in via workspaceCreate.
//
// Two-dir model rationale (see cross-app design §5): the boot dir is
// ephemeral (cleaned on session done); the workspace dir is durable so logs
// survive post-mortem and resume state lives on disk after the agent exits.
type workspace struct {
	Root      string // ~/.clockwork/workspaces/<projectKey>/<sessID>
	PromptDir string // <root>/prompts
	StateDir  string // <root>/state
	LogDir    string // <root>/logs
	LogPath   string // <root>/logs/session.log
}

// workspaceCreate materializes the dir tree for a session. Returns the
// workspace handle on success; caller plumbs Root via StartOptions.WorkspaceDir
// and LogPath via StartOptions.LogPath (the lib falls back to <WorkspaceDir>/
// logs/session.log when LogPath is empty, but explicit is clearer).
func workspaceCreate(workspacesRoot, projectID, sessID string) (*workspace, error) {
	if workspacesRoot == "" {
		// Default to $HOME/.clockwork/workspaces; matches the convention in
		// the mux daemon path and nanite's chat sessions.
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve $HOME for workspaces root: %w", err)
		}
		workspacesRoot = filepath.Join(home, ".clockwork", "workspaces")
	}
	projectKey := projectID
	if projectKey == "" {
		projectKey = "unscoped"
	}
	root := filepath.Join(workspacesRoot, projectKey, sessID)

	dirs := []string{
		root,
		filepath.Join(root, "prompts"),
		filepath.Join(root, "state"),
		filepath.Join(root, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir workspace %s: %w", d, err)
		}
	}

	return &workspace{
		Root:      root,
		PromptDir: filepath.Join(root, "prompts"),
		StateDir:  filepath.Join(root, "state"),
		LogDir:    filepath.Join(root, "logs"),
		LogPath:   filepath.Join(root, "logs", "session.log"),
	}, nil
}
