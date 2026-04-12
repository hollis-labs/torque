package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RunEventRecord mirrors a row in the run_events table.
// RunID is nullable — pre-run task transitions (e.g., todo → doing) may be
// logged with no run context.
type RunEventRecord struct {
	ID        int64
	RunID     sql.NullInt64
	TaskID    string
	Type      string // "log", "signal", "artifact", "task_transitioned", "run_started", "run_completed", "error"
	Payload   string // JSON blob or raw content, caller decides schema per Type
	CreatedAt time.Time
}

// RunEventFilter narrows a ListRunEvents query.
// Any zero-value field is ignored. SinceID provides monotonic cursor semantics:
// only events with id > SinceID are returned, ordered by id ASC.
// Limit defaults to 100 when zero; pass a positive value to override.
type RunEventFilter struct {
	RunID   int64
	TaskID  string
	Types   []string
	SinceID int64
	Limit   int
}

// AppendRunEvent inserts a single row and returns the auto-assigned ID.
// The CreatedAt field on the input is ignored — the DB assigns it.
func (s *Store) AppendRunEvent(evt *RunEventRecord) (int64, error) {
	if evt.TaskID == "" {
		return 0, fmt.Errorf("run event missing task_id")
	}
	if evt.Type == "" {
		return 0, fmt.Errorf("run event missing type")
	}

	const q = `INSERT INTO run_events (run_id, task_id, type, payload) VALUES (?, ?, ?, ?)`
	res, err := s.db.Exec(q, evt.RunID, evt.TaskID, evt.Type, evt.Payload)
	if err != nil {
		return 0, fmt.Errorf("append run event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	evt.ID = id
	return id, nil
}

// ListRunEvents returns events matching the filter, ordered by id ASC.
// SinceID is a monotonic cursor — pass the last-seen id to get the next page.
func (s *Store) ListRunEvents(f RunEventFilter) ([]RunEventRecord, error) {
	var (
		clauses []string
		args    []interface{}
	)

	if f.RunID > 0 {
		clauses = append(clauses, "run_id = ?")
		args = append(args, f.RunID)
	}
	if f.TaskID != "" {
		clauses = append(clauses, "task_id = ?")
		args = append(args, f.TaskID)
	}
	if len(f.Types) > 0 {
		placeholders := make([]string, len(f.Types))
		for i, t := range f.Types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		clauses = append(clauses, "type IN ("+strings.Join(placeholders, ",")+")")
	}
	if f.SinceID > 0 {
		clauses = append(clauses, "id > ?")
		args = append(args, f.SinceID)
	}

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	q := "SELECT id, run_id, task_id, type, payload, created_at FROM run_events" +
		where + " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()

	var out []RunEventRecord
	for rows.Next() {
		var r RunEventRecord
		if err := rows.Scan(&r.ID, &r.RunID, &r.TaskID, &r.Type, &r.Payload, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
