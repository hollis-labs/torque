package scheduler

import (
	"database/sql"
	"encoding/json"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
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
			if err := store.TransitionTaskWithReason(p.ID, "blocked", "child blocked"); err != nil {
				return err
			}
		case allDone:
			if err := applyParentOnDone(store, bus, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyParentOnDone maps the parent's on_done rule to a concrete transition.
// Kept separate so the notify branch can publish cleanly and the rollup
// switch stays readable.
func applyParentOnDone(store *sqlstore.Store, bus *EventBus, p sqlstore.TaskRecord) error {
	switch p.OnDone {
	case "close":
		return store.TransitionTask(p.ID, "done")
	case "notify":
		if err := store.TransitionTask(p.ID, "done"); err != nil {
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
		return store.TransitionTask(p.ID, "review")
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
