package sqlstore_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func setupTaskForCheckpoint(t *testing.T, s *sqlstore.Store, taskID string) {
	t.Helper()
	require.NoError(t, s.CreateTask(&sqlstore.TaskRecord{
		ID:                   taskID,
		Title:                "t",
		Status:               "doing",
		Kind:                 "decision",
		SourceType:           "user",
		Trust:                "normal",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "resume",
		OnDone:               "review",
		OnFail:               "retry",
		OnReview:             "pause",
		OnDoneMerge:          "none",
		Manual:               true,
	}))
}

func setupTaskAndCheckpoint(t *testing.T, s *sqlstore.Store, taskID, corrID string) {
	t.Helper()
	setupTaskForCheckpoint(t, s, taskID)
	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{
		TaskID:            taskID,
		CorrelationID:     corrID,
		Type:              "collect_data",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
	}))
}

func TestCheckpoint_InsertAndGet(t *testing.T) {
	store := setupTestStore(t)
	setupTaskForCheckpoint(t, store, "CW-CP-T1")

	cp := &sqlstore.CheckpointRecord{
		TaskID:            "CW-CP-T1",
		CorrelationID:     "01H-ULID",
		Type:              "collect_data",
		PayloadJSON:       `{"question":"pick one"}`,
		EmitterSourceType: "agent",
		EmitterSourceRef:  sql.NullString{String: "claude-code", Valid: true},
	}
	require.NoError(t, store.CreateCheckpoint(cp))
	assert.NotZero(t, cp.ID)

	got, err := store.GetCheckpointByCorrelation("01H-ULID")
	require.NoError(t, err)
	assert.Equal(t, "CW-CP-T1", got.TaskID)
	assert.Equal(t, "collect_data", got.Type)
	assert.Equal(t, `{"question":"pick one"}`, got.PayloadJSON)
	assert.Equal(t, "agent", got.EmitterSourceType)
	assert.Equal(t, "claude-code", got.EmitterSourceRef.String)
	assert.Equal(t, "pending", got.Status)
	assert.False(t, got.RespondedAt.Valid)
}

func TestCheckpoint_GetByCorrelation_NotFound(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.GetCheckpointByCorrelation("missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrCheckpointNotFound))
}

func TestCheckpoint_Respond(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T2", "CORR-2")

	now := time.Now().UTC()
	require.NoError(t, store.RespondCheckpoint("CORR-2", `{"answer":"a"}`, "user", "chrispian", now))

	got, err := store.GetCheckpointByCorrelation("CORR-2")
	require.NoError(t, err)
	assert.Equal(t, "responded", got.Status)
	assert.Equal(t, `{"answer":"a"}`, got.ResponseJSON.String)
	assert.Equal(t, "user", got.ResponderSourceType.String)
	assert.Equal(t, "chrispian", got.ResponderSourceRef.String)
	assert.True(t, got.RespondedAt.Valid)
}

func TestCheckpoint_Respond_AlreadyTerminal(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T2b", "CORR-2b")

	now := time.Now().UTC()
	require.NoError(t, store.RespondCheckpoint("CORR-2b", `{"answer":"a"}`, "user", "", now))

	// Second respond should fail — checkpoint no longer pending.
	err := store.RespondCheckpoint("CORR-2b", `{"answer":"b"}`, "user", "", now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrCheckpointNotFound))
}

func TestCheckpoint_Cancel(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T3", "CORR-3")

	now := time.Now().UTC()
	require.NoError(t, store.CancelCheckpoint("CORR-3", "no longer relevant", "user", "chrispian", now))

	got, err := store.GetCheckpointByCorrelation("CORR-3")
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.Status)
	assert.Contains(t, got.ResponseJSON.String, "canceled")
	assert.Contains(t, got.ResponseJSON.String, "no longer relevant")
	assert.Equal(t, "user", got.ResponderSourceType.String)
}

func TestCheckpoint_Cancel_AlreadyTerminal(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T3b", "CORR-3b")

	now := time.Now().UTC()
	require.NoError(t, store.CancelCheckpoint("CORR-3b", "oops", "user", "", now))
	err := store.CancelCheckpoint("CORR-3b", "again", "user", "", now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrCheckpointNotFound))
}

func TestCheckpoint_SweepTimedOut(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T4", "CORR-4")
	setupTaskAndCheckpoint(t, store, "CW-CP-T4b", "CORR-4b")

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(1 * time.Hour)
	require.NoError(t, store.SetCheckpointTimeout("CORR-4", past))
	require.NoError(t, store.SetCheckpointTimeout("CORR-4b", future))

	n, err := store.SweepTimedOutCheckpoints(time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := store.GetCheckpointByCorrelation("CORR-4")
	require.NoError(t, err)
	assert.Equal(t, "timed_out", got.Status)

	// Checkpoint with future timeout stays pending.
	got2, err := store.GetCheckpointByCorrelation("CORR-4b")
	require.NoError(t, err)
	assert.Equal(t, "pending", got2.Status)
}

func TestCheckpoint_ListForTask(t *testing.T) {
	store := setupTestStore(t)
	setupTaskForCheckpoint(t, store, "CW-CP-T5")

	for _, corr := range []string{"C5-1", "C5-2", "C5-3"} {
		require.NoError(t, store.CreateCheckpoint(&sqlstore.CheckpointRecord{
			TaskID:            "CW-CP-T5",
			CorrelationID:     corr,
			Type:              "test",
			PayloadJSON:       `{}`,
			EmitterSourceType: "system",
		}))
	}

	list, err := store.ListCheckpointsForTask("CW-CP-T5")
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestCheckpoint_ListPending(t *testing.T) {
	store := setupTestStore(t)
	setupTaskAndCheckpoint(t, store, "CW-CP-T6a", "CORR-6a")
	setupTaskAndCheckpoint(t, store, "CW-CP-T6b", "CORR-6b")
	now := time.Now().UTC()
	require.NoError(t, store.RespondCheckpoint("CORR-6b", `{}`, "user", "", now))

	pending, err := store.ListPendingCheckpoints()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "CORR-6a", pending[0].CorrelationID)
}

func TestCheckpoint_CorrelationIDUnique(t *testing.T) {
	store := setupTestStore(t)
	setupTaskForCheckpoint(t, store, "CW-CP-T7")

	cp := &sqlstore.CheckpointRecord{
		TaskID:            "CW-CP-T7",
		CorrelationID:     "DUP",
		Type:              "test",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
	}
	require.NoError(t, store.CreateCheckpoint(cp))

	dup := &sqlstore.CheckpointRecord{
		TaskID:            "CW-CP-T7",
		CorrelationID:     "DUP",
		Type:              "test",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
	}
	err := store.CreateCheckpoint(dup)
	require.Error(t, err, "duplicate correlation_id should fail")
}
