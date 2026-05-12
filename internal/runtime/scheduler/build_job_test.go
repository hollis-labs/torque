package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestBuildJob_PopulatesMetadata(t *testing.T) {
	sched, _ := setupBuildJobScheduler(t)
	task := sqlstore.TaskRecord{
		ID:       "CW-T-1",
		Title:    "t",
		Metadata: sql.NullString{String: `{"timeout_seconds_override":1800,"origin":"auto"}`, Valid: true},
	}
	job := sched.buildJob(task, 7)
	require.NotNil(t, job.Metadata)
	assert.Equal(t, float64(1800), job.Metadata["timeout_seconds_override"])
	assert.Equal(t, "auto", job.Metadata["origin"])
}

func TestBuildJob_NilMetadataWhenAbsent(t *testing.T) {
	sched, _ := setupBuildJobScheduler(t)
	task := sqlstore.TaskRecord{ID: "CW-T-2", Title: "t"}
	job := sched.buildJob(task, 7)
	assert.Nil(t, job.Metadata)
}

func TestBuildJob_NilMetadataWhenInvalidJSON(t *testing.T) {
	sched, _ := setupBuildJobScheduler(t)
	task := sqlstore.TaskRecord{
		ID:       "CW-T-3",
		Title:    "t",
		Metadata: sql.NullString{String: `not-json`, Valid: true},
	}
	job := sched.buildJob(task, 7)
	// Invalid JSON must not panic; Metadata stays nil so downstream code treats it as absent.
	assert.Nil(t, job.Metadata)
}

func TestBuildJob_InheritsProjectContext(t *testing.T) {
	sched, store := setupBuildJobScheduler(t)
	project := &sqlstore.ProjectRecord{
		ID:           "PRJ-1",
		Name:         "Clockwork",
		RepoPath:     "/repo/project",
		AgentPath:    ".agents/clockwork.md",
		ReadPaths:    sql.NullString{String: `["docs","specs"]`, Valid: true},
		WritePaths:   sql.NullString{String: `["apps/gui"]`, Valid: true},
		ContextPaths: sql.NullString{String: `["docs/architecture","design"]`, Valid: true},
		Permissions:  sql.NullString{String: `{"sandbox":"workspace-write"}`, Valid: true},
		Rules:        sql.NullString{String: `["Prefer project docs first"]`, Valid: true},
		Status:       "active",
	}
	require.NoError(t, store.CreateProject(project))
	require.NoError(t, store.CreateProjectArtifact(&sqlstore.ProjectArtifactRecord{
		ProjectID: project.ID,
		EntryType: "document",
		Title:     "PRD",
		FilePath:  "docs/prd.md",
	}))

	task := sqlstore.TaskRecord{
		ID:        "CW-T-4",
		Title:     "t",
		ProjectID: sql.NullString{String: project.ID, Valid: true},
	}
	job := sched.buildJob(task, 7)
	require.NotNil(t, job.Metadata)
	assert.Equal(t, "/repo/project", job.WorkingDir)
	assert.Equal(t, ".agents/clockwork.md", job.AgentFile)
	assert.Contains(t, job.Files, "docs/architecture")
	assert.Contains(t, job.Files, "docs/prd.md")
	assert.Equal(t, "workspace-write", job.Permissions["sandbox"])
	ctx, ok := job.Metadata["project_context"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, project.ID, ctx["project_id"])
}

func setupBuildJobScheduler(t *testing.T) (*Scheduler, *sqlstore.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	reg := executor.NewRegistry()
	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	sched := New(store, q, reg, nil, &config.SchedulerConfig{Workers: 1, Enabled: true, StaleSeconds: 300})
	return sched, store
}
