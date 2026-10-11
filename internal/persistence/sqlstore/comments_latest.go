package sqlstore

import (
	"strings"
	"time"
)

// LatestCommentAt returns, for each id that has comments, the time of its
// newest comment on that entity type. Ids with no comments are absent. Used
// by the planner board: Torque does not bump a task's updated_at on a
// comment, so a working agent would otherwise look idle.
func (s *Store) LatestCommentAt(entityType string, ids []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, entityType)
	for _, id := range ids {
		args = append(args, id)
	}
	q := `SELECT entity_id, created_at FROM comments WHERE entity_type = ? AND entity_id IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		if cur, ok := out[id]; !ok || at.After(cur) {
			out[id] = at
		}
	}
	return out, rows.Err()
}
