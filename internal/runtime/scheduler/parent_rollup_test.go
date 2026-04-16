package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
)

func setupRollupStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func createChildTask(t *testing.T, store *sqlstore.Store, id, status string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: id, Title: "child " + id, Status: status,
	}))
}

func createParentTask(t *testing.T, store *sqlstore.Store, id string, childIDs []string, onDone string) {
	t.Helper()
	md := `{"children":[`
	for i, c := range childIDs {
		if i > 0 {
			md += ","
		}
		md += `"` + c + `"`
	}
	md += `]}`
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       id,
		Title:    "parent " + id,
		Status:   "todo",
		Kind:     "parent",
		OnDone:   onDone,
		Metadata: sql.NullString{String: md, Valid: true},
	}))
}

func TestParentRollup_AllChildrenDone_OnDoneClose_TransitionsToDone(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-1", "done")
	createChildTask(t, store, "CW-C-2", "done")
	createParentTask(t, store, "CW-P-1", []string{"CW-C-1", "CW-C-2"}, "close")

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-1")
	require.NoError(t, err)
	assert.Equal(t, "done", parent.Status)
}

func TestParentRollup_AllChildrenDone_OnDoneReview_TransitionsToReview(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-3", "done")
	createChildTask(t, store, "CW-C-4", "done")
	createParentTask(t, store, "CW-P-2", []string{"CW-C-3", "CW-C-4"}, "review")

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-2")
	require.NoError(t, err)
	assert.Equal(t, "review", parent.Status)
}

func TestParentRollup_AnyChildBlocked_ParentBlocks(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-5", "done")
	createChildTask(t, store, "CW-C-6", "blocked")
	createParentTask(t, store, "CW-P-3", []string{"CW-C-5", "CW-C-6"}, "close")

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-3")
	require.NoError(t, err)
	assert.Equal(t, "blocked", parent.Status)
	assert.Contains(t, parent.BlockedReason, "child")
}

func TestParentRollup_WorkInFlight_ParentUnchanged(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-7", "done")
	createChildTask(t, store, "CW-C-8", "doing")
	createParentTask(t, store, "CW-P-4", []string{"CW-C-7", "CW-C-8"}, "close")

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-4")
	require.NoError(t, err)
	assert.Equal(t, "todo", parent.Status, "parent stays in todo while work is in flight")
}

func TestParentRollup_SkipsTerminalParents(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-9", "blocked")
	// Parent already archived — rollup should leave it alone.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-P-5",
		Title:    "archived parent",
		Status:   "archived",
		Kind:     "parent",
		OnDone:   "close",
		Metadata: sql.NullString{String: `{"children":["CW-C-9"]}`, Valid: true},
	}))

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-5")
	require.NoError(t, err)
	assert.Equal(t, "archived", parent.Status, "archived parent should not be touched")
}

func TestParentRollup_MissingChildren_ParentUnchanged(t *testing.T) {
	store := setupRollupStore(t)
	// Parent has no metadata.children at all.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:     "CW-P-6",
		Title:  "no kids",
		Status: "todo",
		Kind:   "parent",
		OnDone: "close",
	}))

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-6")
	require.NoError(t, err)
	assert.Equal(t, "todo", parent.Status, "parent with no children is a noop for rollup")
}

func TestParentRollup_MissingChildTask_TreatedAsNotDone(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-10", "done")
	// CW-MISSING never existed; rollup should leave parent in todo (all-done is false).
	createParentTask(t, store, "CW-P-7", []string{"CW-C-10", "CW-MISSING"}, "close")

	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-7")
	require.NoError(t, err)
	assert.Equal(t, "todo", parent.Status)
}

// When the rollup transitions the parent, subsequent ticks should be no-ops.
// (Idempotency safeguard — the terminal-state guard at the top of the loop.)
func TestParentRollup_Idempotent(t *testing.T) {
	store := setupRollupStore(t)
	createChildTask(t, store, "CW-C-11", "done")
	createChildTask(t, store, "CW-C-12", "done")
	createParentTask(t, store, "CW-P-8", []string{"CW-C-11", "CW-C-12"}, "close")

	require.NoError(t, scheduler.ParentRollupTick(store))
	require.NoError(t, scheduler.ParentRollupTick(store))

	parent, err := store.GetTask("CW-P-8")
	require.NoError(t, err)
	assert.Equal(t, "done", parent.Status)
}
