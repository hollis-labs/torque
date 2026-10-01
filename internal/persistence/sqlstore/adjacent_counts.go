package sqlstore

import "strings"

// countFiltered receives table names only from store-owned literal call sites.
func (s *Store) countFiltered(table string, where []string, args []any) (int, error) {
	query := "SELECT COUNT(*) FROM " + table
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	err := s.ReadDB().QueryRow(query, args...).Scan(&total)
	return total, err
}

// CountTasks counts the matching cohort, without pagination predicates.
func (s *Store) CountTasks(f TaskFilter) (int, error) {
	f.Limit, f.Offset = 0, 0
	f.AfterSortValue, f.AfterID = "", ""
	where, args, err := s.taskListPredicates(f)
	if err != nil {
		return 0, err
	}
	return s.countFiltered("tasks", where, args)
}
