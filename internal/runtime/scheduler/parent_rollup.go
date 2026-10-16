package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// ParentRollupTick derives each non-terminal parent task's status from its
// children referenced in metadata.children. Called from Scheduler.Tick
// before pick/dispatch so parents don't leak into the executor path.
// bus is optional (nil is fine for contexts that don't subscribe) and is
// used to publish task.notify when on_done=notify fires.
//
// Rules (spec §3.4):
//   - Parents in terminal states (done, archived) are skipped.
//   - Parents with no metadata.children are skipped (the rollup has no
//     signal to act on; orchestrator will populate children later).
//   - Any child in "blocked" → parent transitions to blocked with
//     "child blocked" as BlockedReason.
//   - All children in done or archived → parent transitions per its
//     on_done rule: close → done, notify → done + task.notify event,
//     review (or default/unknown) → review.
//   - Otherwise (work in flight, missing children) → parent is left
//     unchanged so the next tick re-evaluates.
func ParentRollupTick(store *sqlstore.Store, bus *EventBus) error {
	return ParentRollupTickGated(store, bus, nil)
}

// landingBlockedPrefix starts every blocked reason the landing gate writes. A
// parent blocked by it is left alone until a person changes its status, so the
// gate does not re-run and re-escalate on every tick.
const landingBlockedPrefix = "landing evidence missing"

// ParentRollupTickGated is ParentRollupTick with a landing gate applied to the
// close/notify exits. gate returns the unmet requirements for a parent about to
// become done, or ""; nil disables it.
func ParentRollupTickGated(store *sqlstore.Store, bus *EventBus, gate func(context.Context, *sqlstore.TaskRecord) string) error {
	return ParentRollupTickEvaluated(store, bus, evaluationFromReason(gate))
}

// evaluationFromReason adapts a string landing gate to the typed form: a
// non-empty reason is a blocking failure, "" reports nothing.
func evaluationFromReason(gate func(context.Context, *sqlstore.TaskRecord) string) func(context.Context, *sqlstore.TaskRecord) sqlstore.GateEvaluation {
	if gate == nil {
		return nil
	}
	return func(ctx context.Context, t *sqlstore.TaskRecord) sqlstore.GateEvaluation {
		if reason := gate(ctx, t); reason != "" {
			return sqlstore.GateEvaluation{Kind: sqlstore.GateKindLanding, Result: sqlstore.GateFail, Blocking: true, Reason: reason}
		}
		return sqlstore.GateEvaluation{}
	}
}

// ParentRollupTickEvaluated is ParentRollupTickGated with a typed gate. The
// evaluation of a parent about to become done is recorded as gate.evaluated in
// the transaction of the transition it decides (the block, or the done).
func ParentRollupTickEvaluated(store *sqlstore.Store, bus *EventBus, eval func(context.Context, *sqlstore.TaskRecord) sqlstore.GateEvaluation) error {
	parents, err := store.ListTasks(sqlstore.TaskFilter{Kind: "parent"})
	if err != nil {
		return err
	}
	for _, p := range parents {
		if p.Status == "done" || p.Status == "archived" {
			continue
		}
		childIDs := extractChildIDs(p.Metadata)
		if len(childIDs) == 0 {
			continue
		}

		allDone, anyBlocked := true, false
		for _, id := range childIDs {
			c, err := store.GetTask(id)
			if err != nil {
				// Missing child — can't claim "all done". Leave the parent
				// alone; next tick retries once the orchestrator populates
				// or corrects the metadata.
				allDone = false
				continue
			}
			if c.Status == "blocked" {
				anyBlocked = true
			}
			if c.Status != "done" && c.Status != "archived" {
				allDone = false
			}
		}

		switch {
		case anyBlocked && p.Status != "blocked":
			if err := store.TransitionTaskOpts(context.Background(), p.ID, "blocked", schedReason("child blocked")); err != nil {
				return err
			}
		case allDone:
			if p.Status == "blocked" && strings.HasPrefix(p.BlockedReason, landingBlockedPrefix) {
				continue
			}
			var evals []sqlstore.GateEvaluation
			if eval != nil && (p.OnDone == "close" || p.OnDone == "notify") {
				ev := eval(context.Background(), &p)
				if ev.Kind != "" {
					evals = append(evals, ev)
				}
				if ev.Blocking {
					if err := transitionWithGates(store, p.ID, "blocked", schedReason(ev.Reason), evals); err != nil {
						return err
					}
					continue
				}
			}
			if err := applyParentOnDone(store, bus, p, evals); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyParentOnDone maps the parent's on_done rule to a concrete transition.
// Kept separate so the notify branch can publish cleanly and the rollup
// switch stays readable.
func applyParentOnDone(store *sqlstore.Store, bus *EventBus, p sqlstore.TaskRecord, evals []sqlstore.GateEvaluation) error {
	switch p.OnDone {
	case "close":
		return transitionWithGates(store, p.ID, "done", schedPlain(), evals)
	case "notify":
		if err := transitionWithGates(store, p.ID, "done", schedPlain(), evals); err != nil {
			return err
		}
		if bus != nil {
			bus.Publish(SchedulerEvent{
				Type:   "task.notify",
				TaskID: p.ID,
				Data:   map[string]interface{}{"reason": "parent rollup: all children done"},
			})
		}
		return nil
	default: // "review" and any unexpected value
		return store.TransitionTaskOpts(context.Background(), p.ID, "review", schedPlain())
	}
}

// extractChildIDs reads metadata.children as []string. Non-string elements
// are skipped (defensive — the array ought to be all-string but we don't
// hard-fail on a lone stray entry).
func extractChildIDs(meta sql.NullString) []string {
	if !meta.Valid || meta.String == "" {
		return nil
	}
	var md map[string]any
	if err := json.Unmarshal([]byte(meta.String), &md); err != nil {
		return nil
	}
	arr, ok := md["children"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// transitionWithGates moves a task and appends the gate evaluations that
// decided the move in the same transaction, ahead of its task.state_changed,
// which names the deciding evaluation as its cause.
func transitionWithGates(store *sqlstore.Store, id, to string, opts sqlstore.TransitionOpts, evals []sqlstore.GateEvaluation) error {
	if len(evals) == 0 {
		return store.TransitionTaskOpts(context.Background(), id, to, opts)
	}
	w, err := store.BeginWriteTx(context.Background())
	if err != nil {
		return err
	}
	defer w.Rollback()
	deciding, err := w.AppendGateEvaluations(id, 0, evals, opts.Trigger)
	if err != nil {
		return err
	}
	opts.CausationID = deciding
	if err := w.TransitionTaskOpts(id, to, opts); err != nil {
		return err
	}
	return w.Commit()
}
