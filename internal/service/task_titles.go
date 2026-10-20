package service

import (
	"fmt"
	"regexp"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

var taskBriefID = regexp.MustCompile(`^[A-Z]{2,6}-\d{8}-\d{3,6}$`)

// maxTaskTitles caps one TaskTitles call.
const maxTaskTitles = 100

// TaskBrief is the title and status shown beside a task reference.
type TaskBrief struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	UpdatedAt string `json:"updated_at"`
}

func briefOf(t sqlstore.TaskRecord) TaskBrief {
	return TaskBrief{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, UpdatedAt: t.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")}
}

// TaskTitles resolves Torque task ids to a brief each; an unknown id maps to
// nil. At most 100 ids per call.
func (s *TaskService) TaskTitles(ids []string) (map[string]*TaskBrief, error) {
	if len(ids) > maxTaskTitles {
		return nil, &ValidationError{Field: "ids", Message: fmt.Sprintf("at most %d ids per call", maxTaskTitles)}
	}
	out := make(map[string]*TaskBrief, len(ids))
	for _, id := range ids {
		if !taskBriefID.MatchString(id) {
			return nil, &ValidationError{Field: "ids", Message: fmt.Sprintf("%q is not a Torque task id", id)}
		}
		t, err := s.store.GetTask(id)
		if err != nil {
			out[id] = nil // unknown (or unreadable) ids are shown as unresolved, not an error
			continue
		}
		b := briefOf(*t)
		out[id] = &b
	}
	return out, nil
}
