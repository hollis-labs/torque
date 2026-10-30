package sqlstore

import "fmt"

// BindRunTether records the Tether session a run launched. Torque keeps this
// session-to-task mapping itself: Tether drops task_id from non-operator
// callers. An empty launch or workstream keeps the stored value, so the
// workstream can be filled in after launch.
func (w *WriteTx) BindRunTether(runID int64, tetherSessionID, launchID, workstreamID string) error {
	res, err := w.tx.Exec(`UPDATE runs SET
		tether_session_id = COALESCE(NULLIF(?, ''), tether_session_id),
		launch_id = COALESCE(NULLIF(?, ''), launch_id),
		workstream_id = COALESCE(NULLIF(?, ''), workstream_id)
		WHERE id = ?`, tetherSessionID, launchID, workstreamID, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlstore: bind run %d: no such run", runID)
	}
	return nil
}
