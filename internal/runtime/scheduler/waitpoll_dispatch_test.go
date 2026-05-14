package scheduler_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/waitpoll"
)

func setupWaitStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

// createWaitTask inserts a kind=wait/manual=false task with the given
// metadata.wait config, in "todo" state so the picker would dispatch it.
func createWaitTask(t *testing.T, store *sqlstore.Store, id, onDone, waitJSON string) *sqlstore.TaskRecord {
	t.Helper()
	rec := &sqlstore.TaskRecord{
		ID:                   id,
		Title:                "wait " + id,
		Status:               "todo",
		Kind:                 "wait",
		SourceType:           "user",
		Trust:                "normal",
		CheckpointMode:       "none",
		OnCheckpointResponse: "resume",
		OnDone:               onDone,
		OnFail:               "retry",
		OnReview:             "pause",
		OnDoneMerge:          "none",
		Metadata:             sql.NullString{String: waitJSON, Valid: true},
	}
	require.NoError(t, store.CreateTask(rec))
	return rec
}

func TestDispatchWait_PredicateTrue_TransitionsToDone(t *testing.T) {
	store := setupWaitStore(t)

	// target task (task_done predicate looks for it)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-TGT-1", Title: "target", Status: "done",
	}))

	task := createWaitTask(t, store, "CW-W-1", "close",
		`{"wait":{"predicate_type":"task_done","params":{"task_id":"CW-TGT-1"}}}`)

	reg := waitpoll.NewRegistry()
	require.NoError(t, reg.Register(waitpoll.NewTaskDone(store)))
	bus := scheduler.NewEventBus()
	defer bus.Close()

	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, bus, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", got.Status)
}

func TestDispatchWait_PredicateFalse_StaysInTodo(t *testing.T) {
	store := setupWaitStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-TGT-2", Title: "target", Status: "todo",
	}))

	task := createWaitTask(t, store, "CW-W-2", "close",
		`{"wait":{"predicate_type":"task_done","params":{"task_id":"CW-TGT-2"}}}`)

	reg := waitpoll.NewRegistry()
	require.NoError(t, reg.Register(waitpoll.NewTaskDone(store)))

	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, nil, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status, "wait task stays in todo when predicate hasn't fired")
}

func TestDispatchWait_PredicateTrue_OnDoneReview_Parks(t *testing.T) {
	store := setupWaitStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-TGT-3", Title: "target", Status: "done",
	}))

	task := createWaitTask(t, store, "CW-W-3", "review",
		`{"wait":{"predicate_type":"task_done","params":{"task_id":"CW-TGT-3"}}}`)

	reg := waitpoll.NewRegistry()
	require.NoError(t, reg.Register(waitpoll.NewTaskDone(store)))

	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, nil, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status)
}

func TestDispatchWait_UnknownPredicate_Blocks(t *testing.T) {
	store := setupWaitStore(t)
	task := createWaitTask(t, store, "CW-W-BAD", "close",
		`{"wait":{"predicate_type":"unknown_predicate","params":{}}}`)

	reg := waitpoll.NewRegistry()

	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, nil, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", got.Status)
	assert.Contains(t, got.BlockedReason, "unknown_predicate")
}

func TestDispatchWait_PredicateError_Blocks(t *testing.T) {
	store := setupWaitStore(t)
	// task_done predicate but target task doesn't exist → predicate returns
	// ErrTaskNotFound; dispatcher should mark the wait task blocked rather
	// than loop forever.
	task := createWaitTask(t, store, "CW-W-ERR", "close",
		`{"wait":{"predicate_type":"task_done","params":{"task_id":"CW-NONE"}}}`)

	reg := waitpoll.NewRegistry()
	require.NoError(t, reg.Register(waitpoll.NewTaskDone(store)))

	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, nil, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", got.Status)
	assert.Contains(t, got.BlockedReason, "task_done")
}

func TestDispatchWait_MalformedMetadata_Blocks(t *testing.T) {
	store := setupWaitStore(t)
	// metadata present but no `wait` key
	task := createWaitTask(t, store, "CW-W-MAL", "close", `{"not_wait":{}}`)

	reg := waitpoll.NewRegistry()
	require.NoError(t, scheduler.DispatchWait(context.Background(), store, reg, nil, task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", got.Status)
	assert.Contains(t, got.BlockedReason, "metadata.wait")
}

func TestDispatchWait_WrongKind_Rejected(t *testing.T) {
	store := setupWaitStore(t)
	agentTask := &sqlstore.TaskRecord{
		ID: "CW-AGENT-1", Title: "agent", Status: "todo", Kind: "agent",
		Executor: "cli",
	}
	require.NoError(t, store.CreateTask(agentTask))

	reg := waitpoll.NewRegistry()
	err := scheduler.DispatchWait(context.Background(), store, reg, nil, agentTask)
	require.Error(t, err, "DispatchWait should reject non-wait tasks — caller bug")
	assert.Contains(t, err.Error(), "non-wait")
}
