package sqlstore

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Subtodo is one checklist entry on a task. See migration 011.
// evidence is free-form — typically an artifact id, commit SHA, or URL
// the agent cites when it ticks the item via torque_task_subtodo_done.
type Subtodo struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
	Done     bool   `json:"done"`
	Evidence string `json:"evidence,omitempty"`
}

// EncodeSubtodos marshals a subtodo slice to JSON. Returns a NullString
// that is invalid (NULL in SQL) when the slice is empty.
func EncodeSubtodos(items []Subtodo) (sql.NullString, error) {
	if len(items) == 0 {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(items)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode subtodos: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// DecodeSubtodos parses a NULL-able JSON array. NULL / empty string -> nil.
func DecodeSubtodos(s sql.NullString) ([]Subtodo, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var out []Subtodo
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil, fmt.Errorf("decode subtodos: %w", err)
	}
	return out, nil
}

// GetSubtodos returns the subtodo list for a task, or nil when unset.
func (s *Store) GetSubtodos(taskID string) ([]Subtodo, error) {
	var raw sql.NullString
	err := s.ReadDB().QueryRow(`SELECT subtodos FROM tasks WHERE id = ?`, taskID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
	}
	if err != nil {
		return nil, err
	}
	return DecodeSubtodos(raw)
}

// SetSubtodos overwrites the entire subtodo list for a task. Empty slice
// clears the column (stores SQL NULL).
func (s *Store) SetSubtodos(taskID string, items []Subtodo) error {
	ns, err := EncodeSubtodos(items)
	if err != nil {
		return err
	}
	var arg any
	if ns.Valid {
		arg = ns.String
	} else {
		arg = nil
	}
	res, err := s.db.Exec(
		`UPDATE tasks SET subtodos = ?, updated_at = ? WHERE id = ?`,
		arg, updatedAtNow(), taskID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %s: %w", taskID, ErrTaskNotFound)
	}
	return nil
}

// ClearSubtodos removes the subtodo column contents (sets it to SQL NULL).
func (s *Store) ClearSubtodos(taskID string) error {
	return s.SetSubtodos(taskID, nil)
}

// SetSubtodoDone marks a single subtodo item as done and records its
// evidence string. Returns ErrTaskNotFound if the task doesn't exist, and
// a descriptive error when the item id isn't in the list.
func (s *Store) SetSubtodoDone(taskID, itemID, evidence string) error {
	items, err := s.GetSubtodos(taskID)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].ID == itemID {
			items[i].Done = true
			if evidence != "" {
				items[i].Evidence = evidence
			}
			return s.SetSubtodos(taskID, items)
		}
	}
	return fmt.Errorf("subtodo %q not found on task %s", itemID, taskID)
}
