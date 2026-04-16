package scheduler

import (
	"database/sql"
	"encoding/json"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// ParentRollupTick derives each non-terminal parent task's status from its
// children referenced in metadata.children. Called from Scheduler.Tick
// before pick/dispatch so parents don't leak into the executor path.
//
// Rules (spec §3.4):
//   - Parents in terminal states (done, archived) are skipped.
//   - Parents with no metadata.children are skipped (the rollup has no
//     signal to act on; orchestrator will populate children later).
//   - Any child in "blocked" → parent transitions to blocked with
//     "child blocked" as BlockedReason.
//   - All children in done or archived → parent transitions per its
//     on_done rule: close → done, review (or default) → review,
//     notify → done + task.notify bus event.
//   - Otherwise (work in flight, missing children) → parent is left
//     unchanged so the next tick re-evaluates.
func ParentRollupTick(store *sqlstore.Store) error {
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
			target := "review"
			if p.OnDone == "close" {
				target = "done"
			}
			if err := store.TransitionTask(p.ID, target); err != nil {
				return err
			}
		}
	}
	return nil
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
