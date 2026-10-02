package service

import (
	"strconv"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

type SubtodoPage struct {
	Items     []sqlstore.Subtodo
	Positions []int
	Total     *int
	Query     NormalizedCursorQuery
}

// QuerySubtodos bounds the response over the parent JSON checklist. Position
// is unique within that array; concurrent edits can move positions.
func (s *TaskService) QuerySubtodos(taskID string, q CursorQuery) (SubtodoPage, error) {
	n, err := normalizeCursorQuery(q, pagination.DefaultLimit, pagination.MaxLimit, "position", "asc", []string{"position"})
	if err != nil {
		return SubtodoPage{}, err
	}
	after := -1
	if n.AfterID != "" {
		after, err = strconv.Atoi(n.AfterSortValue)
		if err != nil || after < 0 {
			return SubtodoPage{}, &ValidationError{Field: "cursor", Message: "invalid checklist cursor position"}
		}
	}
	all, err := s.ListSubtodos(taskID)
	if err != nil {
		return SubtodoPage{}, err
	}
	page := SubtodoPage{Items: []sqlstore.Subtodo{}, Positions: []int{}, Query: n}
	if q.IncludeTotal {
		v := len(all)
		page.Total = &v
	}
	start, step := 0, 1
	if n.SortDir == "desc" {
		start, step = len(all)-1, -1
	}
	if after >= 0 {
		if n.SortDir == "asc" {
			if after >= len(all) {
				return page, nil
			}
			start = after + 1
		} else if after < len(all) {
			start = after - 1
		}
	}
	for i := start; i >= 0 && i < len(all) && len(page.Items) < n.Limit+1; i += step {
		page.Items = append(page.Items, all[i])
		page.Positions = append(page.Positions, i)
	}
	return page, nil
}
