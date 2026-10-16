package scheduler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/require"
)

type gateRow struct {
	Kind     string
	Trigger  string
	Env      runtimeevents.Event
	GateID   string
	GateKind string
	Subject  string
	Result   string
	Blocking bool
	Reason   string
	Missing  []string
	To       string
}

// outboxKinds reads the outbox in order, decoding gate and state payloads.
func outboxRows(t *testing.T, store *sqlstore.Store) []gateRow {
	t.Helper()
	rows, err := store.DB().Query(`SELECT kind, trigger_kind, envelope FROM event_outbox ORDER BY seq`)
	require.NoError(t, err)
	defer rows.Close()
	var out []gateRow
	for rows.Next() {
		var r gateRow
		var env string
		require.NoError(t, rows.Scan(&r.Kind, &r.Trigger, &env))
		require.NoError(t, json.Unmarshal([]byte(env), &r.Env))
		switch r.Kind {
		case "gate.evaluated":
			var p struct {
				Gate     struct{ ID, Kind string }
				Subject  struct{ Type, ID string }
				Result   string
				Blocking bool
				Reason   string
				Missing  []string
			}
			require.NoError(t, json.Unmarshal(r.Env.Payload, &p))
			r.GateID, r.GateKind, r.Subject, r.Result, r.Blocking, r.Reason, r.Missing =
				p.Gate.ID, p.Gate.Kind, p.Subject.Type+"/"+p.Subject.ID, p.Result, p.Blocking, p.Reason, p.Missing
		case "task.state_changed":
			var p struct{ To string }
			require.NoError(t, json.Unmarshal(r.Env.Payload, &p))
			r.To = p.To
		}
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func gateTask(t *testing.T, store *sqlstore.Store, id string, mut func(*sqlstore.TaskRecord)) {
	t.Helper()
	rec := &sqlstore.TaskRecord{ID: id, Title: "Task", Status: "doing", Executor: "cli", OnDone: "close", MaxRetries: 3}
	if mut != nil {
		mut(rec)
	}
	require.NoError(t, store.CreateTask(rec))
}

func newGateLM(t *testing.T) (*sqlstore.Store, *scheduler.LifecycleManager) {
	t.Helper()
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	t.Cleanup(bus.Close)
	return store, scheduler.NewLifecycleManager(store, bus)
}

func TestGateEvents_AllPassThenDone(t *testing.T) {
	store, lm := newGateLM(t)
	lm.SetLandingEvaluator(func(context.Context, *sqlstore.TaskRecord) sqlstore.GateEvaluation {
		return sqlstore.GateEvaluation{Kind: sqlstore.GateKindLanding, Result: sqlstore.GatePass}
	})
	gateTask(t, store, "CW-G1", func(r *sqlstore.TaskRecord) {
		r.Deliverables = sql.NullString{String: `[{"type":"diff","required":true}]`, Valid: true}
	})
	require.NoError(t, store.SetSubtodos("CW-G1", []sqlstore.Subtodo{{ID: "a", Text: "a", Required: true, Done: true}, {ID: "b", Text: "b"}}))

	require.NoError(t, lm.HandleResult("CW-G1", 7, &executor.ExecutionResult{Status: "done", Artifacts: []executor.Artifact{{Type: "diff", Content: "x"}}}))

	rows := outboxRows(t, store)
	require.Len(t, rows, 4)
	for i, want := range []string{"deliverables", "subtodos", "landing"} {
		r := rows[i]
		require.Equal(t, "gate.evaluated", r.Kind)
		require.Equal(t, want, r.GateKind)
		require.Equal(t, "CW-G1:"+want, r.GateID, "the gate id is stable across evaluations")
		require.Equal(t, "task/CW-G1", r.Subject)
		require.Equal(t, "pass", r.Result)
		require.False(t, r.Blocking)
		require.Equal(t, "7", r.Env.Scope.RunID)
		require.Equal(t, "CW-G1", r.Env.Scope.TaskID)
		require.Equal(t, "scheduler", r.Trigger)
		r.Env.Sequence = 1
		require.NoError(t, r.Env.Validate())
	}
	require.Equal(t, "task.state_changed", rows[3].Kind, "the checked gates come before the change they cause")
	require.Equal(t, "done", rows[3].To)
}

func TestGateEvents_FailedSubtodosBlockAndSkipLaterGates(t *testing.T) {
	store, lm := newGateLM(t)
	called := false
	lm.SetLandingEvaluator(func(context.Context, *sqlstore.TaskRecord) sqlstore.GateEvaluation {
		called = true
		return sqlstore.GateEvaluation{Kind: sqlstore.GateKindLanding, Result: sqlstore.GatePass}
	})
	gateTask(t, store, "CW-G2", nil)
	require.NoError(t, store.SetSubtodos("CW-G2", []sqlstore.Subtodo{{ID: "a", Text: "a", Required: true}, {ID: "b", Text: "b", Required: true}}))

	require.NoError(t, lm.HandleResult("CW-G2", 1, &executor.ExecutionResult{Status: "done"}))

	rows := outboxRows(t, store)
	require.Len(t, rows, 2)
	require.Equal(t, "subtodos", rows[0].GateKind)
	require.Equal(t, "fail", rows[0].Result)
	require.True(t, rows[0].Blocking)
	require.Equal(t, []string{"a", "b"}, rows[0].Missing)
	require.Equal(t, "blocked", rows[1].To)
	require.False(t, called, "a gate the flow never reaches is not evaluated or reported")
}

func TestGateEvents_MissingDeliverablesFailThenRetry(t *testing.T) {
	store, lm := newGateLM(t)
	gateTask(t, store, "CW-G3", func(r *sqlstore.TaskRecord) {
		r.Deliverables = sql.NullString{String: `[{"type":"diff","required":true},{"type":"test-results","required":true}]`, Valid: true}
	})
	require.NoError(t, lm.HandleResult("CW-G3", 1, &executor.ExecutionResult{Status: "done", Artifacts: []executor.Artifact{{Type: "diff", Content: "x"}}}))

	rows := outboxRows(t, store)
	require.Len(t, rows, 2)
	require.Equal(t, "deliverables", rows[0].GateKind)
	require.Equal(t, "fail", rows[0].Result)
	require.True(t, rows[0].Blocking)
	require.Equal(t, []string{"test-results"}, rows[0].Missing)
	require.Equal(t, "todo", rows[1].To, "the retry carries the evaluation that caused it")
}

func TestGateEvents_UnreadableDeliverablesReportBlockedButDoNotStopAcceptance(t *testing.T) {
	store, lm := newGateLM(t)
	gateTask(t, store, "CW-G4", func(r *sqlstore.TaskRecord) {
		r.Deliverables = sql.NullString{String: `not json`, Valid: true}
	})
	require.NoError(t, lm.HandleResult("CW-G4", 1, &executor.ExecutionResult{Status: "done"}))

	rows := outboxRows(t, store)
	require.Len(t, rows, 2)
	require.Equal(t, "blocked", rows[0].Result, "the gate could not decide")
	require.False(t, rows[0].Blocking, "and today's flow carries on, so it is not marked as blocking")
	require.Equal(t, "done", rows[1].To)
}

func TestGateEvents_LandingResults(t *testing.T) {
	cases := []struct {
		name     string
		eval     sqlstore.GateEvaluation
		wantTo   string
		wantRes  string
		blocking bool
	}{
		{"fail", sqlstore.GateEvaluation{Kind: "landing", Result: sqlstore.GateFail, Blocking: true, Reason: "landing evidence missing: no PR", Missing: []string{"no PR"}}, "blocked", "fail", true},
		{"cannot decide yet", sqlstore.GateEvaluation{Kind: "landing", Result: sqlstore.GateBlocked, Blocking: true, Reason: "landing evidence missing: check ci has not finished"}, "blocked", "blocked", true},
		{"waived is a skip, not a pass", sqlstore.GateEvaluation{Kind: "landing", Result: sqlstore.GateSkipped, Reason: "landing evidence waived"}, "done", "skipped", false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, lm := newGateLM(t)
			ev := tc.eval
			lm.SetLandingEvaluator(func(context.Context, *sqlstore.TaskRecord) sqlstore.GateEvaluation { return ev })
			id := "CW-GL" + string(rune('0'+i))
			gateTask(t, store, id, nil)
			require.NoError(t, lm.HandleResult(id, 1, &executor.ExecutionResult{Status: "done"}))
			rows := outboxRows(t, store)
			require.Len(t, rows, 2)
			require.Equal(t, tc.wantRes, rows[0].Result)
			require.Equal(t, tc.blocking, rows[0].Blocking)
			require.Equal(t, tc.wantTo, rows[1].To)
			task, err := store.GetTask(id)
			require.NoError(t, err)
			require.Equal(t, tc.wantTo, task.Status)
		})
	}
}

func TestGateEvents_LegacyLandingGateStillBlocksAndReports(t *testing.T) {
	store, lm := newGateLM(t)
	lm.SetLandingGate(func(context.Context, *sqlstore.TaskRecord) string { return "landing evidence missing: x" })
	gateTask(t, store, "CW-G6", nil)
	require.NoError(t, lm.HandleResult("CW-G6", 1, &executor.ExecutionResult{Status: "done"}))
	rows := outboxRows(t, store)
	require.Len(t, rows, 2)
	require.Equal(t, "fail", rows[0].Result)
	require.Equal(t, "blocked", rows[1].To)
}

func TestGateEvents_NoGatesConfiguredEmitsNoGateEvents(t *testing.T) {
	store, lm := newGateLM(t)
	gateTask(t, store, "CW-G7", nil)
	require.NoError(t, lm.HandleResult("CW-G7", 1, &executor.ExecutionResult{Status: "done"}))
	rows := outboxRows(t, store)
	require.Len(t, rows, 1)
	require.Equal(t, "task.state_changed", rows[0].Kind)
}
