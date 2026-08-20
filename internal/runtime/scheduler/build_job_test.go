package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
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

func TestBuildJob_PopulatesPlantedBootIdentityFields(t *testing.T) {
	sched, _ := setupBuildJobScheduler(t)
	task := sqlstore.TaskRecord{
		ID:          "CW-T-BOOT",
		Title:       "Fix worker boot context",
		Description: "Plant task context for workers.",
		Status:      "doing",
		Priority:    3,
		Kind:        "agent",
		ProjectID:   sql.NullString{String: "PRJ-BOOT", Valid: true},
		ParentID:    sql.NullString{String: "CW-PARENT", Valid: true},
		SprintID:    sql.NullString{String: "SPR-1", Valid: true},
		EpicID:      sql.NullString{String: "EPIC-1", Valid: true},
	}
	job := sched.buildJob(task, 42)
	assert.Equal(t, "CW-T-BOOT", job.TaskID)
	assert.Equal(t, "Fix worker boot context", job.TaskTitle)
	assert.Equal(t, "agent", job.Kind)
	assert.Equal(t, "doing", job.TaskStatus)
	assert.Equal(t, 3, job.TaskPriority)
	assert.Equal(t, "PRJ-BOOT", job.ProjectID)
	assert.Equal(t, "CW-PARENT", job.ParentID)
	assert.Equal(t, "SPR-1", job.SprintID)
	assert.Equal(t, "EPIC-1", job.EpicID)
}

// TestBuildJob_PopulatesDependsOnFromJoinTable covers depends_on since
// migration 027 / FK-003: it now lives in the task_dependencies join table,
// not a TaskRecord column, so buildJob queries the store by task.ID rather
// than decoding a JSON string off the passed-in struct. Unlike the other
// buildJob field tests, this one needs real rows persisted (including the
// dependency targets, since depends_on_task_id is now a real FK).
func TestBuildJob_PopulatesDependsOnFromJoinTable(t *testing.T) {
	sched, store := setupBuildJobScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-DEP-1", Title: "dep 1"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-DEP-2", Title: "dep 2"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-T-DEPS", Title: "depender"}))
	require.NoError(t, store.SetTaskDependencies("CW-T-DEPS", []string{"CW-DEP-1", "CW-DEP-2"}))

	task := sqlstore.TaskRecord{ID: "CW-T-DEPS", Title: "depender"}
	job := sched.buildJob(task, 42)
	assert.Equal(t, []string{"CW-DEP-1", "CW-DEP-2"}, job.DependsOn)
}

// TestBuildJob_EmptyDependsOnWhenNoneSet covers the no-dependencies case:
// ListTaskDependencyIDs returns an empty result, and buildJob leaves
// job.DependsOn nil (its zero value) rather than an empty non-nil slice.
func TestBuildJob_EmptyDependsOnWhenNoneSet(t *testing.T) {
	sched, store := setupBuildJobScheduler(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-T-NODEPS", Title: "no deps"}))

	task := sqlstore.TaskRecord{ID: "CW-T-NODEPS", Title: "no deps"}
	job := sched.buildJob(task, 42)
	assert.Nil(t, job.DependsOn)
}

func TestBuildJob_InheritsProjectContext(t *testing.T) {
	sched, store := setupBuildJobScheduler(t)
	project := &sqlstore.ProjectRecord{
		ID:           "PRJ-1",
		Name:         "Torque",
		RepoPath:     "/repo/project",
		AgentPath:    ".agents/torque.md",
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
	assert.Equal(t, ".agents/torque.md", job.AgentFile)
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
