package worktree

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Manager handles git worktree lifecycle — creation, tracking, and cleanup.
type Manager struct {
	store *sqlstore.Store
	cfg   WorktreeConfig
}

// CreateRequest holds the parameters for creating a new worktree.
type CreateRequest struct {
	TaskID    string
	RunID     int64
	ProjectID string
	RepoPath  string
}

// NewManager creates a worktree manager.
func NewManager(store *sqlstore.Store, cfg WorktreeConfig) *Manager {
	return &Manager{
		store: store,
		cfg:   cfg,
	}
}

// Create creates a new git worktree for a task execution.
// It creates a branch named torque/<taskID>, adds a git worktree at
// <repoPath>/.torque/worktrees/<taskID>/, and records it in the database.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*WorktreeRecord, error) {
	// Check max concurrent worktrees for this project.
	active, err := m.countActiveForProject(ctx, req.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("count active worktrees: %w", err)
	}
	if active >= m.cfg.MaxPerProject {
		return nil, fmt.Errorf("max concurrent worktrees (%d) reached for project %s", m.cfg.MaxPerProject, req.ProjectID)
	}

	branch := "torque/" + req.TaskID
	wtPath := filepath.Join(req.RepoPath, m.cfg.WorktreeBaseDir, req.TaskID)

	// Ensure the parent directory exists.
	if err := os.MkdirAll(filepath.Dir(wtPath), 0755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}

	// Create the branch from HEAD.
	if err := gitCmd(req.RepoPath, "branch", branch); err != nil {
		return nil, fmt.Errorf("create branch %s: %w", branch, err)
	}

	// Add the worktree.
	if err := gitCmd(req.RepoPath, "worktree", "add", wtPath, branch); err != nil {
		// Clean up branch on failure.
		_ = gitCmd(req.RepoPath, "branch", "-D", branch)
		return nil, fmt.Errorf("add worktree: %w", err)
	}

	// Record in database.
	// run_id is nullable (FK to runs); stored as NULL here since the worktree
	// manager does not create runs directly — callers can update the association.
	record := &WorktreeRecord{
		TaskID:    req.TaskID,
		ProjectID: req.ProjectID,
		Branch:    branch,
		Path:      wtPath,
		Status:    "active",
		CreatedAt: time.Now(),
	}

	if err := m.insertWorktree(ctx, record); err != nil {
		// Best-effort cleanup on DB failure.
		_ = gitCmd(req.RepoPath, "worktree", "remove", "--force", wtPath)
		_ = gitCmd(req.RepoPath, "branch", "-D", branch)
		return nil, fmt.Errorf("record worktree: %w", err)
	}

	return record, nil
}

// Get returns the worktree record for a task.
func (m *Manager) Get(ctx context.Context, taskID string) (*WorktreeRecord, error) {
	record := &WorktreeRecord{}
	var cleanedAt sql.NullTime
	var projectID sql.NullString

	err := m.store.DB().QueryRow(
		`SELECT id, task_id, run_id, project_id, branch, path, status, created_at, cleaned_at
		 FROM worktrees WHERE task_id = ? ORDER BY id DESC LIMIT 1`,
		taskID,
	).Scan(
		&record.ID, &record.TaskID, &record.RunID, &projectID,
		&record.Branch, &record.Path, &record.Status, &record.CreatedAt, &cleanedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("worktree for task %s not found", taskID)
	}
	if err != nil {
		return nil, err
	}

	if projectID.Valid {
		record.ProjectID = projectID.String
	}
	if cleanedAt.Valid {
		record.CleanedAt = &cleanedAt.Time
	}

	return record, nil
}

// ListActive returns all active worktrees for a project.
func (m *Manager) ListActive(ctx context.Context, projectID string) ([]WorktreeRecord, error) {
	rows, err := m.store.DB().Query(
		`SELECT id, task_id, run_id, project_id, branch, path, status, created_at, cleaned_at
		 FROM worktrees WHERE project_id = ? AND status = 'active' ORDER BY created_at ASC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var worktrees []WorktreeRecord
	for rows.Next() {
		var wt WorktreeRecord
		var cleanedAt sql.NullTime
		var projectID sql.NullString
		if err := rows.Scan(
			&wt.ID, &wt.TaskID, &wt.RunID, &projectID,
			&wt.Branch, &wt.Path, &wt.Status, &wt.CreatedAt, &cleanedAt,
		); err != nil {
			return nil, err
		}
		if projectID.Valid {
			wt.ProjectID = projectID.String
		}
		if cleanedAt.Valid {
			wt.CleanedAt = &cleanedAt.Time
		}
		worktrees = append(worktrees, wt)
	}
	return worktrees, rows.Err()
}

// Cleanup removes a worktree from disk and updates the database record.
// It runs `git worktree remove` and deletes the branch.
func (m *Manager) Cleanup(ctx context.Context, taskID string, repoPath string) error {
	record, err := m.Get(ctx, taskID)
	if err != nil {
		return err
	}

	// Remove the worktree from git.
	if err := gitCmd(repoPath, "worktree", "remove", "--force", record.Path); err != nil {
		// If the directory doesn't exist, that's fine — still update DB.
		if _, statErr := os.Stat(record.Path); !os.IsNotExist(statErr) {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}

	// Prune stale worktree references.
	_ = gitCmd(repoPath, "worktree", "prune")

	// Delete the branch.
	_ = gitCmd(repoPath, "branch", "-D", record.Branch)

	// Update database record.
	_, err = m.store.DB().Exec(
		`UPDATE worktrees SET status = 'cleaned', cleaned_at = CURRENT_TIMESTAMP WHERE task_id = ? AND status = 'active'`,
		taskID,
	)
	return err
}

// countActiveForProject returns the number of active worktrees for a project.
func (m *Manager) countActiveForProject(ctx context.Context, projectID string) (int, error) {
	var count int
	err := m.store.DB().QueryRow(
		"SELECT COUNT(*) FROM worktrees WHERE project_id = ? AND status = 'active'",
		projectID,
	).Scan(&count)
	return count, err
}

// insertWorktree records a new worktree in the database.
func (m *Manager) insertWorktree(ctx context.Context, record *WorktreeRecord) error {
	var projectID sql.NullString
	if record.ProjectID != "" {
		projectID = sql.NullString{String: record.ProjectID, Valid: true}
	}
	result, err := m.store.DB().Exec(
		`INSERT INTO worktrees (task_id, run_id, project_id, branch, path, status) VALUES (?, ?, ?, ?, ?, ?)`,
		record.TaskID, record.RunID, // run_id is NullInt64 (nullable FK to runs)
		projectID, record.Branch, record.Path, record.Status,
	)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	record.ID = id
	return nil
}

// gitCmd runs a git command in the given directory.
func gitCmd(dir string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s: %w", args, string(out), err)
	}
	return nil
}
