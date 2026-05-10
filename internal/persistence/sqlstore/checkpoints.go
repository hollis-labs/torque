package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrCheckpointNotFound is returned by Get/Respond/Cancel when no matching
// pending checkpoint row exists (either the correlation_id is unknown or the
// row has already left the pending state).
var ErrCheckpointNotFound = errors.New("checkpoint not found")

// CheckpointRecord mirrors a row in the checkpoints table (migration 008).
// Payload and response are opaque JSON strings — schema vocabulary will land
// with go-envelope (BLG-030), not in this package.
type CheckpointRecord struct {
	ID                  int64
	TaskID              string
	RunID               sql.NullInt64
	CorrelationID       string
	Type                string
	PayloadJSON         string
	ResponseJSON        sql.NullString
	EmitterSourceType   string
	EmitterSourceRef    sql.NullString
	ResponderSourceType sql.NullString
	ResponderSourceRef  sql.NullString
	EmittedAt           time.Time
	RespondedAt         sql.NullTime
	TimeoutAt           sql.NullTime
	Status              string
}

const checkpointSelectCols = `id, task_id, run_id, correlation_id, type,
	payload_json, response_json, emitter_source_type, emitter_source_ref,
	responder_source_type, responder_source_ref, emitted_at, responded_at,
	timeout_at, status`

func scanCheckpoint(row interface {
	Scan(...any) error
}) (*CheckpointRecord, error) {
	cp := &CheckpointRecord{}
	err := row.Scan(
		&cp.ID, &cp.TaskID, &cp.RunID, &cp.CorrelationID, &cp.Type,
		&cp.PayloadJSON, &cp.ResponseJSON, &cp.EmitterSourceType, &cp.EmitterSourceRef,
		&cp.ResponderSourceType, &cp.ResponderSourceRef, &cp.EmittedAt, &cp.RespondedAt,
		&cp.TimeoutAt, &cp.Status,
	)
	if err != nil {
		return nil, err
	}
	return cp, nil
}

// CreateCheckpoint inserts a new pending checkpoint. Status defaults to
// "pending" if empty. Populates cp.ID with the inserted row ID.
func (s *Store) CreateCheckpoint(cp *CheckpointRecord) error {
	status := cp.Status
	if status == "" {
		status = "pending"
	}
	res, err := s.db.Exec(`
		INSERT INTO checkpoints (task_id, run_id, correlation_id, type,
			payload_json, emitter_source_type, emitter_source_ref, timeout_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cp.TaskID, cp.RunID, cp.CorrelationID, cp.Type,
		cp.PayloadJSON, cp.EmitterSourceType, cp.EmitterSourceRef,
		cp.TimeoutAt, status,
	)
	if err != nil {
		return fmt.Errorf("insert checkpoint: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	cp.ID = id
	cp.Status = status
	return nil
}

// GetCheckpointByCorrelation fetches a checkpoint by its correlation_id.
// Returns ErrCheckpointNotFound wrapped if no row matches.
func (s *Store) GetCheckpointByCorrelation(correlationID string) (*CheckpointRecord, error) {
	row := s.ReadDB().QueryRow(`SELECT `+checkpointSelectCols+` FROM checkpoints WHERE correlation_id = ?`, correlationID)
	cp, err := scanCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
	}
	if err != nil {
		return nil, err
	}
	return cp, nil
}

// ListCheckpointsForTask returns all checkpoints associated with a task,
// ordered newest-first by emitted_at.
func (s *Store) ListCheckpointsForTask(taskID string) ([]CheckpointRecord, error) {
	rows, err := s.ReadDB().Query(`SELECT `+checkpointSelectCols+` FROM checkpoints WHERE task_id = ? ORDER BY emitted_at DESC, id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckpointRecord
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cp)
	}
	return out, rows.Err()
}

// ListPendingCheckpoints returns every pending checkpoint across all tasks,
// oldest-first (so the scheduler can process them in emission order).
func (s *Store) ListPendingCheckpoints() ([]CheckpointRecord, error) {
	rows, err := s.ReadDB().Query(`SELECT ` + checkpointSelectCols + ` FROM checkpoints WHERE status = 'pending' ORDER BY emitted_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckpointRecord
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cp)
	}
	return out, rows.Err()
}

// RespondCheckpoint writes a response to a pending checkpoint and flips its
// status to "responded". Only pending rows may be responded to; calling on a
// terminal row returns ErrCheckpointNotFound to match the "not found in
// respondable state" semantics callers expect.
func (s *Store) RespondCheckpoint(correlationID, responseJSON, responderType, responderRef string, at time.Time) error {
	res, err := s.db.Exec(`
		UPDATE checkpoints SET status = 'responded', response_json = ?,
			responder_source_type = ?, responder_source_ref = ?, responded_at = ?
		WHERE correlation_id = ? AND status = 'pending'`,
		responseJSON, responderType, nullableString(responderRef), at, correlationID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
	}
	return nil
}

// CancelCheckpoint marks a pending checkpoint "canceled" and records the
// canceler as the responder along with a {canceled, reason} JSON body.
// Returns ErrCheckpointNotFound if the row is not pending.
func (s *Store) CancelCheckpoint(correlationID, reason, cancelerType, cancelerRef string, at time.Time) error {
	body, err := json.Marshal(map[string]any{"canceled": true, "reason": reason})
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`
		UPDATE checkpoints SET status = 'canceled', response_json = ?,
			responder_source_type = ?, responder_source_ref = ?, responded_at = ?
		WHERE correlation_id = ? AND status = 'pending'`,
		string(body), cancelerType, nullableString(cancelerRef), at, correlationID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
	}
	return nil
}

// SetCheckpointTimeout updates the timeout_at column for a checkpoint. Used
// by the service layer when the emit payload carries a timeout.
func (s *Store) SetCheckpointTimeout(correlationID string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE checkpoints SET timeout_at = ? WHERE correlation_id = ?`, at, correlationID)
	return err
}

// SweepTimedOutCheckpoints flips every pending row with timeout_at < now()
// to "timed_out" and returns the number affected. Callers drive this on the
// scheduler tick.
func (s *Store) SweepTimedOutCheckpoints(now time.Time) (int, error) {
	res, err := s.db.Exec(`
		UPDATE checkpoints SET status = 'timed_out'
		WHERE status = 'pending' AND timeout_at IS NOT NULL AND timeout_at < ?`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
