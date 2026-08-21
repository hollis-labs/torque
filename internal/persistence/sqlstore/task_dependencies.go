package sqlstore

import "strings"

// SetTaskDependencies replaces all dependency edges for the given task in a
// single transaction. Preserves input order via explicit sort_order
// (0-indexed), mirroring SetTaskTags. An empty depIDs slice clears all
// dependencies.
//
// Existence of each depIDs entry is validated by the service layer before
// this is called (TaskService.validateTaskWrites); a caller that bypasses
// that check will hit the depends_on_task_id FOREIGN KEY constraint here
// instead.
func (s *Store) SetTaskDependencies(taskID string, depIDs []string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := setTaskDependenciesExec(tx, taskID, depIDs); err != nil {
		return err
	}

	return tx.Commit()
}

// setTaskDependenciesExec holds SetTaskDependencies' delete+reinsert logic,
// parameterized over dbExecer so it can run inside SetTaskDependencies' own
// single-purpose transaction (Store.SetTaskDependencies, above) or composed
// into a caller-managed *sql.Tx (WriteTx.SetTaskDependencies, write_tx.go —
// FIX-007) alongside other task writes that must commit together.
func setTaskDependenciesExec(ex dbExecer, taskID string, depIDs []string) error {
	if _, err := ex.Exec(`DELETE FROM task_dependencies WHERE task_id = ?`, taskID); err != nil {
		return err
	}

	// Bind a pre-formatted SQLiteDatetimeLayout string (not a raw time.Time)
	// so this column's created_at is byte-identical to every other
	// table's, matching updatedAtNow's own rationale (tasks.go) — see its
	// doc comment for why a raw time.Time bind here would silently break
	// any future cursor pagination over task_dependencies.created_at.
	now := updatedAtNow()
	for i, depID := range depIDs {
		_, err := ex.Exec(
			`INSERT INTO task_dependencies (task_id, depends_on_task_id, sort_order, created_at)
			 VALUES (?, ?, ?, ?)`,
			taskID, depID, i, now,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// ListTaskDependencyIDs returns the depends_on_task_id values linked to a
// task, ordered by the caller's original insertion order (sort_order ASC).
// Every ID returned still exists in tasks — ON DELETE CASCADE on
// depends_on_task_id prunes the row automatically when the dependency task
// is deleted, so this never surfaces a dangling reference.
func (s *Store) ListTaskDependencyIDs(taskID string) ([]string, error) {
	rows, err := s.ReadDB().Query(
		`SELECT depends_on_task_id FROM task_dependencies WHERE task_id = ? ORDER BY sort_order ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListTaskDependencyIDsBulk is ListTaskDependencyIDs for many tasks at once,
// one query instead of one-per-task — the scheduler picker's per-tick
// dependency check (picker.go) is the motivating caller, batching what used
// to be a per-candidate round trip. A taskID with no dependency edges is
// simply absent from the returned map (same "no error, no entry" contract as
// the single-task version returning a nil slice). Empty input short-circuits
// to an empty map with no query.
func (s *Store) ListTaskDependencyIDsBulk(taskIDs []string) (map[string][]string, error) {
	result := make(map[string][]string, len(taskIDs))
	if len(taskIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(taskIDs))
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := s.ReadDB().Query(
		`SELECT task_id, depends_on_task_id FROM task_dependencies WHERE task_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY task_id, sort_order ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var taskID, depID string
		if err := rows.Scan(&taskID, &depID); err != nil {
			return nil, err
		}
		result[taskID] = append(result[taskID], depID)
	}
	return result, rows.Err()
}
