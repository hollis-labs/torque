package main

import (
	"bytes"
	"database/sql"
	"log"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"

	_ "modernc.org/sqlite"
)

// seedFKOrphanTask creates a task with the given sprint/project/epic ids
// (empty string = NULL) via a raw INSERT, bypassing TaskService/store
// validation so a "dangling" id can be planted directly — exactly what an
// orphaned reference from a bypassed/legacy write path looks like on disk.
func seedFKOrphanTask(t *testing.T, store *sqlstore.Store, id, sprintID, projectID, epicID string) {
	t.Helper()
	task := &sqlstore.TaskRecord{
		ID:     id,
		Title:  "orphan-fixture-" + id,
		Status: "todo",
		Kind:   "agent",
	}
	if sprintID != "" {
		task.SprintID = sql.NullString{String: sprintID, Valid: true}
	}
	if projectID != "" {
		task.ProjectID = sql.NullString{String: projectID, Valid: true}
	}
	if epicID != "" {
		task.EpicID = sql.NullString{String: epicID, Valid: true}
	}
	require.NoError(t, store.CreateTask(task))
}

func TestFindFKOrphans_NoOrphansInCleanData(t *testing.T) {
	store, cleanup, _ := newCLITestStore(t)
	defer cleanup()

	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-1", Name: "sprint one"}))
	seedFKOrphanTask(t, store, "TASK-clean-1", "SP-1", "", "")

	for _, oc := range fkOrphanColumns {
		refs, total, err := findFKOrphans(store.DB(), oc)
		require.NoError(t, err)
		require.Empty(t, refs)
		require.Zero(t, total)
	}
}

func TestFindAndNullifyFKOrphans(t *testing.T) {
	store, cleanup, _ := newCLITestStore(t)
	defer cleanup()

	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-live", Name: "live sprint"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-live", Name: "live project"}))
	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "EPIC-live", Name: "live epic"}))

	// Healthy rows: point at real sprint/project/epic.
	seedFKOrphanTask(t, store, "TASK-ok-1", "SP-live", "PRJ-live", "EPIC-live")

	// Orphaned rows planted directly (simulating a bypassed write path or a
	// deletion that skipped/lost its own cleanup) — three tasks clustered on
	// the same missing sprint id, one task on a missing project id, one task
	// on a missing epic id.
	seedFKOrphanTask(t, store, "TASK-orphan-sprint-1", "SP-deleted", "", "")
	seedFKOrphanTask(t, store, "TASK-orphan-sprint-2", "SP-deleted", "", "")
	seedFKOrphanTask(t, store, "TASK-orphan-sprint-3", "SP-deleted", "", "")
	seedFKOrphanTask(t, store, "TASK-orphan-project-1", "", "PRJ-deleted", "")
	seedFKOrphanTask(t, store, "TASK-orphan-epic-1", "", "", "EPIC-deleted")

	byColumn := map[string]fkOrphanColumn{}
	for _, oc := range fkOrphanColumns {
		byColumn[oc.Column] = oc
	}

	sprintRefs, sprintTotal, err := findFKOrphans(store.DB(), byColumn["sprint_id"])
	require.NoError(t, err)
	require.Equal(t, 3, sprintTotal)
	require.Len(t, sprintRefs, 1)
	require.Equal(t, "SP-deleted", sprintRefs[0].Value)
	require.Equal(t, 3, sprintRefs[0].Count)

	projectRefs, projectTotal, err := findFKOrphans(store.DB(), byColumn["project_id"])
	require.NoError(t, err)
	require.Equal(t, 1, projectTotal)
	require.Len(t, projectRefs, 1)
	require.Equal(t, "PRJ-deleted", projectRefs[0].Value)

	epicRefs, epicTotal, err := findFKOrphans(store.DB(), byColumn["epic_id"])
	require.NoError(t, err)
	require.Equal(t, 1, epicTotal)
	require.Len(t, epicRefs, 1)
	require.Equal(t, "EPIC-deleted", epicRefs[0].Value)

	// The cluster-warning branch should fire for sprint_id (3/3 orphans on
	// one missing id) — capture log output to confirm it's surfaced, not
	// just silently cleaned.
	var logBuf bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	logFKOrphanReport(byColumn["sprint_id"], sprintRefs, sprintTotal)
	log.SetOutput(prevOutput)
	log.SetFlags(prevFlags)
	require.Contains(t, logBuf.String(), "WARNING")
	require.Contains(t, logBuf.String(), "SP-deleted")

	// Now clean up and verify affected counts + idempotency.
	tx, err := store.DB().Begin()
	require.NoError(t, err)

	n, err := nullifyFKOrphans(tx, byColumn["sprint_id"])
	require.NoError(t, err)
	require.EqualValues(t, 3, n)

	n, err = nullifyFKOrphans(tx, byColumn["project_id"])
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	n, err = nullifyFKOrphans(tx, byColumn["epic_id"])
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	require.NoError(t, tx.Commit())

	// Re-running the find-orphans query after cleanup returns zero rows
	// (FK-001 acceptance criterion).
	for _, oc := range fkOrphanColumns {
		refs, total, err := findFKOrphans(store.DB(), oc)
		require.NoError(t, err)
		require.Empty(t, refs)
		require.Zero(t, total)
	}

	// The healthy row's references must be untouched.
	got, err := store.GetTask("TASK-ok-1")
	require.NoError(t, err)
	require.True(t, got.SprintID.Valid)
	require.Equal(t, "SP-live", got.SprintID.String)
	require.True(t, got.ProjectID.Valid)
	require.Equal(t, "PRJ-live", got.ProjectID.String)
	require.True(t, got.EpicID.Valid)
	require.Equal(t, "EPIC-live", got.EpicID.String)

	// The orphaned rows are now NULL, not deleted.
	orphan, err := store.GetTask("TASK-orphan-sprint-1")
	require.NoError(t, err)
	require.False(t, orphan.SprintID.Valid)
}
