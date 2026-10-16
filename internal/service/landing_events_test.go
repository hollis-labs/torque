package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/landing"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

type errSC struct{}

func (errSC) PR(context.Context, string) (landing.PRState, error) {
	return landing.PRState{}, errors.New("gh unavailable")
}

type outboxEv struct {
	Kind, Trigger, Result, GateID, To string
	Blocking                          bool
	ID, Causation                     string
}

func outboxFor(t *testing.T, store *sqlstore.Store, taskID string) []outboxEv {
	t.Helper()
	rows, err := store.DB().Query(`SELECT kind, trigger_kind, envelope FROM event_outbox WHERE task_id = ? ORDER BY seq`, taskID)
	require.NoError(t, err)
	defer rows.Close()
	var out []outboxEv
	for rows.Next() {
		var e outboxEv
		var env string
		require.NoError(t, rows.Scan(&e.Kind, &e.Trigger, &env))
		var ev struct {
			ID   string
			Corr struct {
				CausationID string `json:"causation_id"`
			}
			Payload json.RawMessage
		}
		require.NoError(t, json.Unmarshal([]byte(env), &ev))
		var p struct {
			Gate     struct{ ID string }
			Result   string
			Blocking bool
			To       string
		}
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		e.Result, e.GateID, e.Blocking, e.To = p.Result, p.Gate.ID, p.Blocking, p.To
		e.ID, e.Causation = ev.ID, ev.Corr.CausationID
		out = append(out, e)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestEnforceLandingRecordsTheEvaluation(t *testing.T) {
	ctx := context.Background()
	svc, store := setupServiceWithStore(t)
	sc := &stubSC{state: "OPEN"}
	svc.Task.SetSourceControl(sc)
	mk := func(meta map[string]any) string {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: "t", OnDoneMerge: "pr", Metadata: meta})
		require.NoError(t, err)
		return task.ID
	}

	t.Run("fail is recorded with the block, before it", func(t *testing.T) {
		id := mk(map[string]any{"landing": map[string]any{"pr_url": "u"}})
		require.Error(t, svc.Task.Transition(ctx, id, "done"))
		ev := outboxFor(t, store, id)
		require.Len(t, ev, 2)
		require.Equal(t, outboxEv{Kind: "gate.evaluated", Trigger: "hook", Result: "fail", GateID: id + ":landing", Blocking: true, ID: ev[0].ID}, ev[0])
		require.Equal(t, "task.state_changed", ev[1].Kind)
		require.Equal(t, "blocked", ev[1].To)
		require.Equal(t, ev[0].ID, ev[1].Causation, "the block names the gate that decided it")
	})

	t.Run("pass is recorded before the done it allows", func(t *testing.T) {
		sc.state = "MERGED"
		defer func() { sc.state = "OPEN" }()
		id := mk(map[string]any{"landing": map[string]any{"pr_url": "u"}})
		require.NoError(t, svc.Task.Transition(ctx, id, "done"))
		ev := outboxFor(t, store, id)
		require.Len(t, ev, 2)
		require.Equal(t, "pass", ev[0].Result)
		require.False(t, ev[0].Blocking)
		require.Equal(t, "done", ev[1].To)
	})

	t.Run("a waiver is a skip, never a pass", func(t *testing.T) {
		id := mk(map[string]any{"landing": map[string]any{"waived_by": "chris", "waive_reason": "docs only"}})
		require.NoError(t, svc.Task.Transition(ctx, id, "done"))
		ev := outboxFor(t, store, id)
		require.Equal(t, "skipped", ev[0].Result)
		require.False(t, ev[0].Blocking)
	})

	t.Run("an unreadable PR is blocked, not failed", func(t *testing.T) {
		svc.Task.SetSourceControl(errSC{})
		defer svc.Task.SetSourceControl(sc)
		id := mk(map[string]any{"landing": map[string]any{"pr_url": "u"}})
		require.Error(t, svc.Task.Transition(ctx, id, "done"))
		ev := outboxFor(t, store, id)
		require.Equal(t, "blocked", ev[0].Result)
		require.True(t, ev[0].Blocking, "it still stops acceptance, as before")
	})

	t.Run("no policy emits no gate event", func(t *testing.T) {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: "free", OnDoneMerge: "none"})
		require.NoError(t, err)
		require.NoError(t, svc.Task.Transition(ctx, task.ID, "done"))
		ev := outboxFor(t, store, task.ID)
		require.Len(t, ev, 1)
		require.Equal(t, "task.state_changed", ev[0].Kind)
	})

	t.Run("force is a skip with its reason, never a pass", func(t *testing.T) {
		id := mk(nil)
		require.NoError(t, svc.Task.ForceTransition(ctx, id, "done"))
		ev := outboxFor(t, store, id)
		require.Len(t, ev, 2)
		require.Equal(t, "skipped", ev[0].Result)
		require.False(t, ev[0].Blocking)
		require.Equal(t, "done", ev[1].To)
		require.Equal(t, ev[0].ID, ev[1].Causation)
	})

	t.Run("force through the comment path is a skip too", func(t *testing.T) {
		id := mk(nil)
		require.NoError(t, svc.Task.TransitionWithComment(ctx, id, "done", "overriding", "chris", true))
		ev := outboxFor(t, store, id)
		require.Equal(t, "skipped", ev[0].Result)
	})

	t.Run("force on a task with no landing policy records no gate", func(t *testing.T) {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: "free", OnDoneMerge: "none"})
		require.NoError(t, err)
		require.NoError(t, svc.Task.ForceTransition(ctx, task.ID, "done"))
		require.Len(t, outboxFor(t, store, task.ID), 1)
	})
}

// movingSC changes the task's status while the gate reads the PR, as a
// concurrent request would.
type movingSC struct {
	state string
	move  func()
}

func (m *movingSC) PR(context.Context, string) (landing.PRState, error) {
	m.move()
	return landing.PRState{URL: "u", State: m.state}, nil
}

// The gate event is written in the done's transaction, after confirming the
// task is still in the status the gate was decided against. If it moved, no
// event is written about a decision that no longer applies.
func TestEnforceLandingWritesNothingWhenTheTaskMovedMeanwhile(t *testing.T) {
	ctx := context.Background()
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t", OnDoneMerge: "pr", Metadata: map[string]any{"landing": map[string]any{"pr_url": "u"}}})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(ctx, task.ID, "doing"))

	for _, state := range []string{"MERGED", "OPEN"} { // pass path, then fail path
		sc := &movingSC{state: state}
		sc.move = func() { require.NoError(t, svc.Task.ForceTransition(ctx, task.ID, "review")) }
		svc.Task.SetSourceControl(sc)
		before := len(outboxFor(t, store, task.ID))
		require.NoError(t, svc.Task.ForceTransition(ctx, task.ID, "doing"))
		before++ // the reset above is one state change
		err := svc.Task.Transition(ctx, task.ID, "done")
		var terr *service.TransitionError
		require.ErrorAs(t, err, &terr, state)
		for _, e := range outboxFor(t, store, task.ID)[before:] {
			require.NotEqual(t, "gate.evaluated", e.Kind, "%s: no gate event for a stale decision", state)
		}
	}
}
