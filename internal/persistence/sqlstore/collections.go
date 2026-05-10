package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrCollectionNotFound is returned wrapped by GetCollection when the
// collection ID does not exist. Use errors.Is(err, ErrCollectionNotFound).
var ErrCollectionNotFound = errors.New("collection not found")

// CollectionRecord mirrors the collections table row. ArchivedAt is NULL for
// active collections; non-NULL means the collection is archived (kept for
// audit, hidden from active filters).
type CollectionRecord struct {
	ID          string
	Name        string
	Description string
	ArchivedAt  sql.NullTime
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CollectionFilter holds optional filter criteria for ListCollections. Status
// is one of "active" (archived_at IS NULL), "archived" (archived_at IS NOT
// NULL), or "all" / "" (no filter).
type CollectionFilter struct {
	Status string
	Limit  int
	Offset int
}

// CollectionUpdate holds optional fields to update; nil pointer = no change.
type CollectionUpdate struct {
	Name        *string
	Description *string
}

// CreateCollection inserts a new collection. Caller is responsible for
// supplying the ID (use NextCollectionID).
func (s *Store) CreateCollection(c *CollectionRecord) error {
	_, err := s.db.Exec(`INSERT INTO collections (id, name, description, archived_at)
		VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, c.ArchivedAt,
	)
	return err
}

// GetCollection fetches a single collection by ID. Returns an error wrapping
// ErrCollectionNotFound when no row matches.
func (s *Store) GetCollection(id string) (*CollectionRecord, error) {
	c := &CollectionRecord{}
	err := s.ReadDB().QueryRow(`SELECT id, name, description, archived_at, created_at, updated_at
		FROM collections WHERE id = ?`, id).Scan(
		&c.ID, &c.Name, &c.Description, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("collection %s: %w", id, ErrCollectionNotFound)
	}
	return c, err
}

// GetCollectionNames returns a map of collection_id → name for the given
// IDs in a single query. Unknown IDs are simply absent from the result map.
// Empty input returns an empty map without hitting the DB.
func (s *Store) GetCollectionNames(ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.ReadDB().Query(
		`SELECT id, name FROM collections WHERE id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ListCollections returns collections matching the filter, ordered by
// created_at DESC. Status: "active" (archived_at IS NULL), "archived"
// (archived_at IS NOT NULL), or "" / "all" (no filter).
func (s *Store) ListCollections(f CollectionFilter) ([]CollectionRecord, error) {
	query := `SELECT id, name, description, archived_at, created_at, updated_at FROM collections`

	var conditions []string
	switch f.Status {
	case "active":
		conditions = append(conditions, "archived_at IS NULL")
	case "archived":
		conditions = append(conditions, "archived_at IS NOT NULL")
	case "", "all":
		// no filter
	default:
		// Treat unknown as no filter to keep this layer permissive; the
		// service layer enforces the enum.
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
		if f.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", f.Offset)
		}
	}

	rows, err := s.ReadDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CollectionRecord
	for rows.Next() {
		var c CollectionRecord
		if err := rows.Scan(
			&c.ID, &c.Name, &c.Description, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCollection applies non-nil pointer fields to the collection row.
// updated_at is always bumped if at least one field changes.
func (s *Store) UpdateCollection(id string, u CollectionUpdate) error {
	var sets []string
	var args []interface{}

	if u.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *u.Name)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}

	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	q := fmt.Sprintf("UPDATE collections SET %s WHERE id = ?", strings.Join(sets, ", "))
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("collection %s: %w", id, ErrCollectionNotFound)
	}
	return nil
}

// ArchiveCollection sets archived_at = CURRENT_TIMESTAMP. Idempotent: calling
// archive on an already-archived collection refreshes the timestamp.
func (s *Store) ArchiveCollection(id string) error {
	res, err := s.db.Exec(
		`UPDATE collections SET archived_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("collection %s: %w", id, ErrCollectionNotFound)
	}
	return nil
}

// UnarchiveCollection clears archived_at, restoring the collection to active.
func (s *Store) UnarchiveCollection(id string) error {
	res, err := s.db.Exec(
		`UPDATE collections SET archived_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("collection %s: %w", id, ErrCollectionNotFound)
	}
	return nil
}

// NextCollectionID generates the next sequential collection ID for today.
// Format: COL-YYYYMMDD-NNNN.
func (s *Store) NextCollectionID() (string, error) {
	date := time.Now().Format("20060102")
	prefix := "COL-" + date + "-"

	var maxSeq int
	err := s.db.QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM collections WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}

// AddTaskToCollection assigns a task to a collection, optionally at a
// specific position. position <= 0 means "append" (max(collection_position)+1
// within the target collection). Sets added_to_collections_at on the task if
// it is currently NULL (write-once entry into the collections world).
//
// All updates run in a single transaction so the task can never end up in a
// half-assigned state.
func (s *Store) AddTaskToCollection(taskID, collectionID string, position int) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.assertCollectionExistsTx(tx, collectionID); err != nil {
		return err
	}
	if err := s.assertTaskExistsTx(tx, taskID); err != nil {
		return err
	}

	pos, err := s.resolveCollectionPositionTx(tx, collectionID, position)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE tasks
		SET collection_id = ?,
		    collection_position = ?,
		    added_to_collections_at = COALESCE(added_to_collections_at, CURRENT_TIMESTAMP),
		    updated_at = ?
		WHERE id = ?`,
		collectionID, pos, time.Now().UTC(), taskID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// ErrTaskNotInCollection is returned wrapped when a scoped remove is called
// with a collection_id that doesn't match the task's current membership.
// Use errors.Is(err, ErrTaskNotInCollection).
var ErrTaskNotInCollection = errors.New("task is not in the specified collection")

// RemoveTaskFromCollection clears collection_id and collection_position,
// returning the task to inbox. added_to_collections_at is preserved (the task
// stays visible to the collections view).
//
// expectedCollectionID is enforced: the WHERE clause requires the task to
// currently be in that collection. Pass "" to skip the scope check (used by
// MoveTaskToCollection, which is its own atomic move). When the scope check
// fails, returns ErrTaskNotInCollection so the HTTP layer can surface 409
// rather than silently detaching the task from whatever collection it sits in.
func (s *Store) RemoveTaskFromCollection(taskID, expectedCollectionID string) error {
	var res sql.Result
	var err error
	now := time.Now().UTC()
	if expectedCollectionID == "" {
		res, err = s.db.Exec(`UPDATE tasks
			SET collection_id = NULL,
			    collection_position = NULL,
			    updated_at = ?
			WHERE id = ?`,
			now, taskID,
		)
	} else {
		res, err = s.db.Exec(`UPDATE tasks
			SET collection_id = NULL,
			    collection_position = NULL,
			    updated_at = ?
			WHERE id = ? AND collection_id = ?`,
			now, taskID, expectedCollectionID,
		)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "task doesn't exist" from "task exists but not in this
		// collection" so the HTTP layer can return 404 vs 409 appropriately.
		var taskExists int
		if scanErr := s.db.QueryRow("SELECT 1 FROM tasks WHERE id = ?", taskID).Scan(&taskExists); scanErr == sql.ErrNoRows {
			return fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
		}
		if expectedCollectionID != "" {
			return fmt.Errorf("task %s not in collection %s: %w", taskID, expectedCollectionID, ErrTaskNotInCollection)
		}
		return fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
	}
	return nil
}

// ReorderCollectionTasks rewrites collection_position for the supplied
// orderedTaskIDs in a single transaction. Tasks not in the list keep their
// existing positions. Positions are assigned as 1..N in the supplied order.
// Validates that every task in orderedTaskIDs is currently in collectionID.
func (s *Store) ReorderCollectionTasks(collectionID string, orderedTaskIDs []string) error {
	if len(orderedTaskIDs) == 0 {
		return nil
	}

	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.assertCollectionExistsTx(tx, collectionID); err != nil {
		return err
	}

	// Verify membership: every task must currently belong to collectionID.
	// Bail loud rather than silently re-homing tasks across collections.
	placeholders := make([]string, len(orderedTaskIDs))
	args := make([]interface{}, 0, len(orderedTaskIDs)+1)
	for i, id := range orderedTaskIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, collectionID)

	var memberCount int
	err = tx.QueryRow(
		fmt.Sprintf(
			"SELECT COUNT(*) FROM tasks WHERE id IN (%s) AND collection_id = ?",
			strings.Join(placeholders, ","),
		),
		args...,
	).Scan(&memberCount)
	if err != nil {
		return err
	}
	if memberCount != len(orderedTaskIDs) {
		return fmt.Errorf("reorder: %d task(s) supplied but %d belong to collection %s",
			len(orderedTaskIDs), memberCount, collectionID)
	}

	now := time.Now().UTC()
	stmt, err := tx.Prepare(`UPDATE tasks
		SET collection_position = ?, updated_at = ?
		WHERE id = ? AND collection_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, taskID := range orderedTaskIDs {
		if _, err := stmt.Exec(i+1, now, taskID, collectionID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// MoveTaskToCollection atomically moves a task from its current collection
// (or inbox) to targetCollectionID at the given position (<=0 = append).
// Equivalent to RemoveTaskFromCollection + AddTaskToCollection but without
// the intermediate inbox state.
func (s *Store) MoveTaskToCollection(taskID, targetCollectionID string, position int) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.assertCollectionExistsTx(tx, targetCollectionID); err != nil {
		return err
	}
	if err := s.assertTaskExistsTx(tx, taskID); err != nil {
		return err
	}

	pos, err := s.resolveCollectionPositionTx(tx, targetCollectionID, position)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE tasks
		SET collection_id = ?,
		    collection_position = ?,
		    added_to_collections_at = COALESCE(added_to_collections_at, CURRENT_TIMESTAMP),
		    updated_at = ?
		WHERE id = ?`,
		targetCollectionID, pos, time.Now().UTC(), taskID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// AddTaskToInbox sets added_to_collections_at = CURRENT_TIMESTAMP if
// currently NULL. Idempotent: calling on a task already in inbox or in a
// collection is a no-op (write-once on first entry).
func (s *Store) AddTaskToInbox(taskID string) error {
	// Validate task exists first so a missing-task call surfaces an error
	// rather than silently no-op'ing the UPDATE.
	if err := s.assertTaskExists(taskID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE tasks
		SET added_to_collections_at = CURRENT_TIMESTAMP,
		    updated_at = ?
		WHERE id = ? AND added_to_collections_at IS NULL`,
		time.Now().UTC(), taskID,
	)
	return err
}

// ListInboxTasks returns tasks that are in the inbox: added to the
// collections world (added_to_collections_at IS NOT NULL) but not assigned to
// any collection. Ordered by added_to_collections_at DESC.
func (s *Store) ListInboxTasks() ([]TaskRecord, error) {
	q := `SELECT ` + taskSelectCols + ` FROM tasks
		WHERE added_to_collections_at IS NOT NULL AND collection_id IS NULL
		ORDER BY added_to_collections_at DESC`
	rows, err := s.ReadDB().Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TaskRecord
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListCollectionTasks returns the tasks in a collection, ordered by
// collection_position ASC (NULLs trail).
func (s *Store) ListCollectionTasks(collectionID string) ([]TaskRecord, error) {
	q := `SELECT ` + taskSelectCols + ` FROM tasks
		WHERE collection_id = ?
		ORDER BY collection_position IS NULL, collection_position ASC, created_at ASC`
	rows, err := s.ReadDB().Query(q, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TaskRecord
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ---- internal helpers -------------------------------------------------------

func (s *Store) assertCollectionExistsTx(tx *sql.Tx, collectionID string) error {
	var exists int
	err := tx.QueryRow("SELECT 1 FROM collections WHERE id = ?", collectionID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("collection %s: %w", collectionID, ErrCollectionNotFound)
	}
	return err
}

func (s *Store) assertTaskExistsTx(tx *sql.Tx, taskID string) error {
	var exists int
	err := tx.QueryRow("SELECT 1 FROM tasks WHERE id = ?", taskID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
	}
	return err
}

func (s *Store) assertTaskExists(taskID string) error {
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM tasks WHERE id = ?", taskID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
	}
	return err
}

// resolveCollectionPositionTx returns the position to write. position > 0 is
// honored verbatim; position <= 0 is resolved to max(collection_position)+1
// for the target collection (or 1 if empty).
func (s *Store) resolveCollectionPositionTx(tx *sql.Tx, collectionID string, position int) (int, error) {
	if position > 0 {
		return position, nil
	}
	var maxPos sql.NullInt64
	err := tx.QueryRow(
		"SELECT MAX(collection_position) FROM tasks WHERE collection_id = ?",
		collectionID,
	).Scan(&maxPos)
	if err != nil {
		return 0, err
	}
	if maxPos.Valid {
		return int(maxPos.Int64) + 1, nil
	}
	return 1, nil
}
