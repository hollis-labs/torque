package sqlstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type outboxRow struct {
	Seq     int64
	EventID string
	Kind    string
	Trigger string
	Task    string
	Project string
	Env     runtimeevents.Event
	Payload struct {
		Entity struct{ Type, ID string }
		From   string
		To     string
		Reason string
		Forced bool
		Trig   struct{ Kind, Ref string } `json:"trigger"`
	}
}

func readOutbox(t *testing.T, store *sqlstore.Store) []outboxRow {
	t.Helper()
	rows, err := store.DB().Query(`SELECT seq, event_id, kind, trigger_kind, COALESCE(task_id,''), COALESCE(project_id,''), envelope
		FROM event_outbox ORDER BY seq`)
	require.NoError(t, err)
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var env string
		require.NoError(t, rows.Scan(&r.Seq, &r.EventID, &r.Kind, &r.Trigger, &r.Task, &r.Project, &env))
		require.NoError(t, json.Unmarshal([]byte(env), &r.Env))
		require.NoError(t, json.Unmarshal(r.Env.Payload, &r.Payload))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestStateEvent_TransitionWritesOneEvent(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0001")))
	require.NoError(t, store.TransitionTask("CW-OB-0001", "doing"))

	rows := readOutbox(t, store)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, "task.state_changed", r.Kind)
	assert.Equal(t, "system", r.Trigger)
	assert.Equal(t, "CW-OB-0001", r.Task)
	assert.Equal(t, "todo", r.Payload.From)
	assert.Equal(t, "doing", r.Payload.To)
	assert.Equal(t, "task", r.Payload.Entity.Type)
	assert.Equal(t, "msg://task/torque/CW-OB-0001", r.Env.Subject)
	assert.Equal(t, "state", r.Env.Class.Category)
	assert.Equal(t, "CW-OB-0001", r.Env.Scope.TaskID)
	assert.Equal(t, "CW-OB-0001", r.Env.Corr.CorrelationID)
	assert.True(t, strings.HasPrefix(r.EventID, "evt_"))
	assert.Equal(t, r.EventID, r.Env.ID)
	// Sequence and generation are log-assigned on read, not stored.
	assert.Zero(t, r.Env.Sequence)
}

func TestStateEvent_EnvelopeValidatesOnceSequenceAssigned(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0002")))
	require.NoError(t, store.TransitionTask("CW-OB-0002", "doing"))
	ev := readOutbox(t, store)[0].Env
	ev.Sequence = 1
	require.NoError(t, ev.Validate())
}

func TestStateEvent_AllPathsWriteExactlyOne(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		do   func(t *testing.T, s *sqlstore.Store, id string)
		to   string
		trig string
	}{
		{"TransitionTask", func(t *testing.T, s *sqlstore.Store, id string) {
			require.NoError(t, s.TransitionTask(id, "doing"))
		}, "doing", "system"},
		{"TransitionTaskWithReason", func(t *testing.T, s *sqlstore.Store, id string) {
			require.NoError(t, s.TransitionTaskWithReason(id, "blocked", "waiting on x"))
		}, "blocked", "system"},
		{"TransitionTaskOpts trigger", func(t *testing.T, s *sqlstore.Store, id string) {
			require.NoError(t, s.TransitionTaskOpts(ctx, id, "doing", sqlstore.TransitionOpts{
				Trigger: sqlstore.Trigger{Kind: sqlstore.TriggerScheduler, Ref: "scheduler"}}))
		}, "doing", "scheduler"},
		{"context trigger", func(t *testing.T, s *sqlstore.Store, id string) {
			c := sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_transition"})
			require.NoError(t, s.TransitionTaskOpts(c, id, "doing", sqlstore.TransitionOpts{}))
		}, "doing", "agent"},
		{"WriteTx", func(t *testing.T, s *sqlstore.Store, id string) {
			w, err := s.BeginWriteTx(ctx)
			require.NoError(t, err)
			defer w.Rollback()
			require.NoError(t, w.TransitionTask(id, "doing"))
			require.NoError(t, w.Commit())
		}, "doing", "system"},
		{"WriteTx WithReason", func(t *testing.T, s *sqlstore.Store, id string) {
			w, err := s.BeginWriteTx(ctx)
			require.NoError(t, err)
			defer w.Rollback()
			require.NoError(t, w.TransitionTaskWithReason(id, "blocked", "why"))
			require.NoError(t, w.Commit())
		}, "blocked", "system"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := setupTestStore(t)
			id := "CW-OB-1" + string(rune('0'+i))
			require.NoError(t, store.CreateTask(sampleTask(id)))
			tc.do(t, store, id)
			rows := readOutbox(t, store)
			require.Len(t, rows, 1)
			assert.Equal(t, tc.to, rows[0].Payload.To)
			assert.Equal(t, tc.trig, rows[0].Trigger)
			assert.Equal(t, tc.trig, rows[0].Payload.Trig.Kind)
		})
	}
}

func TestStateEvent_ParkWritesEventOnlyWhenParked(t *testing.T) {
	store := setupTestStore(t)

	skip := sampleTask("CW-OB-0020") // checkpoint_mode none: predicate misses
	require.NoError(t, store.CreateTask(skip))
	require.NoError(t, store.TransitionTask(skip.ID, "doing"))
	before := len(readOutbox(t, store))
	parked, err := store.ParkTaskOnCheckpoint(skip.ID, "unused")
	require.NoError(t, err)
	assert.False(t, parked)
	assert.Len(t, readOutbox(t, store), before, "a missed park predicate must not write an event")

	task := sampleTask("CW-OB-0021")
	task.CheckpointMode = "blocking"
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.TransitionTask(task.ID, "doing"))
	before = len(readOutbox(t, store))
	parked, err = store.ParkTaskOnCheckpoint(task.ID, "awaiting x")
	require.NoError(t, err)
	require.True(t, parked)
	rows := readOutbox(t, store)
	require.Len(t, rows, before+1)
	last := rows[len(rows)-1]
	assert.Equal(t, "doing", last.Payload.From)
	assert.Equal(t, "review", last.Payload.To)
	assert.Equal(t, "awaiting x", last.Payload.Reason)
	assert.Equal(t, "system", last.Trigger)
	assert.Equal(t, "checkpoint.park", last.Payload.Trig.Ref)
}

func TestStateEvent_SelfTransition(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0030")))
	require.NoError(t, store.TransitionTask("CW-OB-0030", "doing"))
	base := len(readOutbox(t, store))

	require.NoError(t, store.TransitionTask("CW-OB-0030", "doing"))
	assert.Len(t, readOutbox(t, store), base, "a non-forced self-transition writes no event")

	require.NoError(t, store.TransitionTaskOpts(context.Background(), "CW-OB-0030", "doing", sqlstore.TransitionOpts{Forced: true}))
	rows := readOutbox(t, store)
	require.Len(t, rows, base+1, "a forced re-assertion writes one event")
	assert.True(t, rows[len(rows)-1].Payload.Forced)
}

func TestStateEvent_SelfTransitionStillFiresInProcessHook(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0031")))
	require.NoError(t, store.TransitionTask("CW-OB-0031", "doing"))
	n := 0
	store.RegisterTaskTransitionHook(func(sqlstore.TaskTransitionEvent) { n++ })
	require.NoError(t, store.TransitionTask("CW-OB-0031", "doing"))
	assert.Equal(t, 1, n, "the in-process hook keeps its behaviour on self-transitions")
}

func TestStateEvent_RollbackWritesNothing(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0040")))

	w, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	require.NoError(t, w.TransitionTask("CW-OB-0040", "doing"))
	require.NoError(t, w.Rollback())

	assert.Empty(t, readOutbox(t, store))
	got, err := store.GetTask("CW-OB-0040")
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status)
}

func TestStateEvent_TwoTransitionsInOneTxKeepOrder(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0041")))
	w, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	defer w.Rollback()
	require.NoError(t, w.TransitionTask("CW-OB-0041", "doing"))
	require.NoError(t, w.TransitionTask("CW-OB-0041", "review"))
	require.NoError(t, w.Commit())

	rows := readOutbox(t, store)
	require.Len(t, rows, 2)
	assert.Less(t, rows[0].Seq, rows[1].Seq)
	assert.Equal(t, "doing", rows[0].Payload.To)
	assert.Equal(t, "review", rows[1].Payload.To)
}

func TestStateEvent_FailureAfterUpdateRollsBackBoth(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0042")))
	w, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	defer w.Rollback()
	require.NoError(t, w.TransitionTask("CW-OB-0042", "doing"))
	// A later statement in the same tx fails: the caller rolls back.
	_, err = w.Exec(`INSERT INTO event_outbox (event_id) VALUES ('x')`)
	require.Error(t, err)
	require.NoError(t, w.Rollback())

	assert.Empty(t, readOutbox(t, store))
	got, err := store.GetTask("CW-OB-0042")
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status)
}

func TestStateEvent_UnknownTaskWritesNothing(t *testing.T) {
	store := setupTestStore(t)
	err := store.TransitionTask("CW-NOPE", "doing")
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.Canceled))
	assert.Empty(t, readOutbox(t, store))
}

func TestStateEvent_ReasonIsCapped(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0050")))
	long := strings.Repeat("é", 2000) // multi-byte: the cut must stay on a rune
	require.NoError(t, store.TransitionTaskWithReason("CW-OB-0050", "blocked", long))
	r := readOutbox(t, store)[0]
	assert.LessOrEqual(t, len(r.Payload.Reason), 1024)
	assert.True(t, r.Env.Truncated)
	assert.True(t, strings.HasPrefix(long, r.Payload.Reason))
	assert.False(t, r.Env.Class.Sanitized, "sanitize stage is not wired yet; the envelope says so")
}

func TestStateEvent_ScopeCarriesOnlyVerifiedColumns(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0060")))
	require.NoError(t, store.TransitionTask("CW-OB-0060", "doing"))
	sc := readOutbox(t, store)[0].Env.Scope
	require.NotNil(t, sc)
	assert.Equal(t, "CW-OB-0060", sc.TaskID)
	assert.Empty(t, sc.LaunchID)
	assert.Empty(t, sc.PhaseID)
	assert.Zero(t, sc.Attempt)
}

func TestStateEvent_WakeAfterCommit(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0070")))
	require.NoError(t, store.TransitionTask("CW-OB-0070", "doing"))
	select {
	case <-store.OutboxWake():
	default:
		t.Fatal("expected a wake-up after a committed state event")
	}
}

// A 3-byte rune at a cut that is not a boundary exercises the walk-back; the
// 2-byte case above never does, since its cap lands on a boundary.
func TestStateEvent_ReasonCutStaysOnARuneBoundary3Byte(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-OB-0051")))
	long := "aa" + strings.Repeat("€", 600) // runes start at 2+3k; 1024 is mid-rune
	require.NoError(t, store.TransitionTaskWithReason("CW-OB-0051", "blocked", long))
	r := readOutbox(t, store)[0]
	assert.LessOrEqual(t, len(r.Payload.Reason), 1024)
	assert.True(t, utf8.ValidString(r.Payload.Reason), "cut must not split a rune")
	assert.True(t, strings.HasPrefix(long, r.Payload.Reason))
	assert.Less(t, len(r.Payload.Reason), 1024, "walked back off the partial rune")
}

// Park records the checkpoint that caused it as the trigger ref.
func TestStateEvent_ParkRefNamesTheCheckpoint(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-OB-0022")
	task.CheckpointMode = "blocking"
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.TransitionTask(task.ID, "doing"))
	ok, err := store.ParkTaskOnCheckpointRef(context.Background(), task.ID, "awaiting checkpoint C1", "checkpoint:C1")
	require.NoError(t, err)
	require.True(t, ok)
	rows := readOutbox(t, store)
	assert.Equal(t, "checkpoint:C1", rows[len(rows)-1].Payload.Trig.Ref)
}

// A caller's trigger kind survives with the checkpoint as the ref.
func TestTriggerWithRef(t *testing.T) {
	assert.Equal(t, sqlstore.Trigger{Kind: sqlstore.TriggerSystem, Ref: "r"}, sqlstore.TriggerWithRef(context.Background(), "r"))
	c := sqlstore.ContextWithTrigger(context.Background(), sqlstore.Trigger{Kind: sqlstore.TriggerHuman, Ref: "http:x"})
	assert.Equal(t, sqlstore.Trigger{Kind: sqlstore.TriggerHuman, Ref: "r"}, sqlstore.TriggerWithRef(c, "r"))
}
