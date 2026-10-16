package sqlstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

func eventsFor(t *testing.T, s *sqlstore.Store, entityType string) []outboxRow {
	t.Helper()
	var out []outboxRow
	for _, r := range readOutbox(t, s) {
		if r.Payload.Entity.Type == entityType {
			out = append(out, r)
		}
	}
	return out
}

func TestRunStateEvents_CreateCompleteAndOperatorCancel(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.CreateTask(sampleTask("CW-RUN-1")))

	_, err := s.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUN-1", Executor: "cli", Trigger: sqlstore.Trigger{Kind: sqlstore.TriggerScheduler, Ref: "scheduler"}})
	require.NoError(t, err)
	runs := eventsFor(t, s, "run")
	require.Len(t, runs, 1)
	assert.Equal(t, "run.state_changed", runs[0].Kind)
	assert.Equal(t, "", runs[0].Payload.From, "a new run has no previous state")
	assert.Equal(t, "running", runs[0].Payload.To)
	assert.Equal(t, "scheduler", runs[0].Trigger)
	assert.Equal(t, "CW-RUN-1", runs[0].Task, "scope carries the task")
	assert.Equal(t, "msg://run/torque/"+runs[0].Payload.Entity.ID, runs[0].Env.Subject)
	assert.Equal(t, "run", runs[0].Payload.Entity.Type)
	assert.Equal(t, runs[0].Env.Scope.RunID, runs[0].Payload.Entity.ID)
	assert.Equal(t, runs[0].Payload.Entity.ID, runs[0].Env.Corr.CorrelationID, "a run event correlates on the run, not the task already in scope")
}

func TestRunStateEvents_CompleteWritesOnceAndOperatorLockedWritesNone(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.CreateTask(sampleTask("CW-RUN-2")))
	id, err := s.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUN-2", Executor: "cli"})
	require.NoError(t, err)

	require.NoError(t, s.CompleteRun(id, sqlstore.RunCompletion{Status: "done", Trigger: sqlstore.Trigger{Kind: sqlstore.TriggerScheduler, Ref: "scheduler"}}))
	runs := eventsFor(t, s, "run")
	require.Len(t, runs, 2)
	assert.Equal(t, "running", runs[1].Payload.From)
	assert.Equal(t, "done", runs[1].Payload.To)

	// An operator-cancelled run is locked: a late executor result changes
	// nothing and writes no event.
	id2, err := s.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUN-2", Executor: "cli"})
	require.NoError(t, err)
	require.NoError(t, s.SetRunOperatorStatus(id2, "cancelled", "operator said stop"))
	before := len(eventsFor(t, s, "run"))
	require.NoError(t, s.CompleteRun(id2, sqlstore.RunCompletion{Status: "done"}))
	assert.Len(t, eventsFor(t, s, "run"), before, "a locked run's late completion writes no event")

	var cancelled outboxRow
	for _, r := range eventsFor(t, s, "run") {
		if r.Payload.To == "cancelled" {
			cancelled = r
		}
	}
	assert.Equal(t, "running", cancelled.Payload.From)
	assert.Equal(t, "operator said stop", cancelled.Payload.Reason)
}

func TestRunStateEvents_CompleteMissingRunWritesNothing(t *testing.T) {
	s := setupTestStore(t)
	require.Error(t, s.CompleteRun(9999, sqlstore.RunCompletion{Status: "done"}))
	assert.Empty(t, eventsFor(t, s, "run"))
}

func TestCheckpointStateEvents_LifecycleCarriesTriggerAndCorrelation(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	setupTaskForCheckpoint(t, s, "CW-CP-1")

	agentCtx := sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_checkpoint_emit"})
	require.NoError(t, s.CreateCheckpointCtx(agentCtx, &sqlstore.CheckpointRecord{
		TaskID: "CW-CP-1", CorrelationID: "CORR1", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system", Status: "pending",
	}))
	humanCtx := sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: sqlstore.TriggerHuman, Ref: "http"})
	require.NoError(t, s.RespondCheckpointCtx(humanCtx, "CORR1", `{"a":1}`, "user", "me", time.Now()))

	cps := eventsFor(t, s, "checkpoint")
	require.Len(t, cps, 2)
	assert.Equal(t, "checkpoint.state_changed", cps[0].Kind)
	assert.Equal(t, "CORR1", cps[0].Payload.Entity.ID)
	assert.Equal(t, "", cps[0].Payload.From)
	assert.Equal(t, "pending", cps[0].Payload.To)
	assert.Equal(t, "agent", cps[0].Trigger)
	assert.Equal(t, "checkpoint:CORR1", cps[0].Payload.Trig.Ref)
	assert.Equal(t, "CW-CP-1", cps[0].Task)

	assert.Equal(t, "pending", cps[1].Payload.From)
	assert.Equal(t, "responded", cps[1].Payload.To)
	assert.Equal(t, "human", cps[1].Trigger)

	// A second response finds nothing pending: error, and no event.
	err := s.RespondCheckpoint("CORR1", `{}`, "user", "me", time.Now())
	require.ErrorIs(t, err, sqlstore.ErrCheckpointNotFound)
	assert.Len(t, eventsFor(t, s, "checkpoint"), 2)
}

func TestCheckpointStateEvents_CancelAndSweep(t *testing.T) {
	s := setupTestStore(t)
	setupTaskForCheckpoint(t, s, "CW-CP-2")
	setupTaskForCheckpoint(t, s, "CW-CP-3")

	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-2", CorrelationID: "C2", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system"}))
	require.NoError(t, s.CancelCheckpoint("C2", "stale", "user", "me", time.Now()))

	past := sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-3", CorrelationID: "C3", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system", TimeoutAt: past}))
	n, err := s.SweepTimedOutCheckpoints(time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var tos []string
	for _, r := range eventsFor(t, s, "checkpoint") {
		tos = append(tos, r.Payload.Entity.ID+":"+r.Payload.From+">"+r.Payload.To)
	}
	assert.Equal(t, []string{"C2:>pending", "C2:pending>canceled", "C3:>pending", "C3:pending>timed_out"}, tos)

	last := eventsFor(t, s, "checkpoint")[3]
	assert.Equal(t, "system", last.Trigger)
	assert.Equal(t, "checkpoint:C3", last.Payload.Trig.Ref)

	// Sweeping again finds nothing due and writes nothing.
	n, err = s.SweepTimedOutCheckpoints(time.Now())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Len(t, eventsFor(t, s, "checkpoint"), 4)
}

// The checkpoint event's subject is its correlation id, which is the table's
// unique key: two checkpoints never share a subject, and a second checkpoint
// cannot be created under an existing correlation id.
func TestCheckpointStateEvents_SubjectIsTheUniqueCorrelationID(t *testing.T) {
	s := setupTestStore(t)
	setupTaskForCheckpoint(t, s, "CW-CP-4")

	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-4", CorrelationID: "UNIQ1", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system"}))
	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-4", CorrelationID: "UNIQ2", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system"}))
	err := s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-4", CorrelationID: "UNIQ1", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system"})
	require.Error(t, err, "the correlation id is unique, so a subject cannot be reused")

	subjects := map[string]int{}
	for _, r := range eventsFor(t, s, "checkpoint") {
		subjects[r.Env.Subject]++
	}
	assert.Equal(t, map[string]int{"msg://checkpoint/torque/UNIQ1": 1, "msg://checkpoint/torque/UNIQ2": 1}, subjects,
		"two checkpoints, two subjects; the failed duplicate wrote no event")
}

// A create has no previous state: the payload omits "from" rather than
// sending an empty string.
func TestStateEvents_CreateOmitsFromEntirely(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.CreateTask(sampleTask("CW-RUN-9")))
	_, err := s.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUN-9", Executor: "cli"})
	require.NoError(t, err)
	setupTaskForCheckpoint(t, s, "CW-CP-9")
	require.NoError(t, s.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "CW-CP-9", CorrelationID: "C9", Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system"}))

	for _, entity := range []string{"run", "checkpoint"} {
		rows := eventsFor(t, s, entity)
		require.NotEmpty(t, rows)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(rows[0].Env.Payload, &payload))
		_, hasFrom := payload["from"]
		assert.False(t, hasFrom, "%s create must omit 'from', not send an empty string", entity)
	}
}
