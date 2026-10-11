package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Direct references from the planner to Torque: the live title and status of
// task ids an item points at, and the tasks behind a workstream. These read
// the task store in process; the planner no longer goes through a REST adapter.

// maxTaskTitles caps one TaskTitles call.
const maxTaskTitles = 100

// workstreamTaskLimit is how many open tasks a workstream view lists.
const workstreamTaskLimit = 25

// PlannerTaskBrief is the part of a Torque task the planner shows beside a reference.
type PlannerTaskBrief struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	UpdatedAt string `json:"updated_at"`
}

func briefOf(t sqlstore.TaskRecord) PlannerTaskBrief {
	return PlannerTaskBrief{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, UpdatedAt: t.UpdatedAt.UTC().Format(boardISO)}
}

// TaskTitles resolves Torque task ids to a brief each; an unknown id maps to
// nil. At most 100 ids per call.
func (s *PlannerService) TaskTitles(ids []string) (map[string]*PlannerTaskBrief, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if len(ids) > maxTaskTitles {
		return nil, &ValidationError{Field: "ids", Message: fmt.Sprintf("at most %d ids per call", maxTaskTitles)}
	}
	out := make(map[string]*PlannerTaskBrief, len(ids))
	for _, id := range ids {
		if !plannerTorqueID.MatchString(id) {
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

// PlannerWorkstreamTasks is a workstream's Torque tasks: counts per status over
// everything in scope, and the open ones newest first.
type PlannerWorkstreamTasks struct {
	WorkstreamID string             `json:"workstream_id"`
	ProjectIDs   []string           `json:"torque_project_ids"`
	Tag          string             `json:"torque_tag"`
	Counts       map[string]int     `json:"counts"`
	Total        int                `json:"total"`
	Open         []PlannerTaskBrief `json:"open"`
	OpenTotal    int                `json:"open_total"`
	Note         string             `json:"note,omitempty"`
}

// openStatuses are the statuses that still need attention.
var openStatuses = []string{"backlog", "todo", "queued", "doing", "review", "blocked", "paused"}

// WorkstreamTasks lists the Torque tasks behind a workstream: tasks in its
// torque_project_ids (a task in any of them), narrowed to its torque_tag when
// one is set. With neither, the workstream has no Torque scope and the result
// is empty with a note.
func (s *PlannerService) WorkstreamTasks(id string) (*PlannerWorkstreamTasks, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	ws, err := s.fetch("workstreams", id)
	if err != nil {
		return nil, err
	}
	out := &PlannerWorkstreamTasks{WorkstreamID: id, Counts: map[string]int{}, Open: []PlannerTaskBrief{}, ProjectIDs: []string{}}
	for _, p := range asAnySlice(ws["torque_project_ids"]) {
		out.ProjectIDs = append(out.ProjectIDs, fmt.Sprint(p))
	}
	out.Tag, _ = ws["torque_tag"].(string)
	out.Tag = strings.TrimSpace(out.Tag)
	if len(out.ProjectIDs) == 0 && out.Tag == "" {
		out.Note = "This workstream names no Torque project or tag."
		return out, nil
	}

	// One filter per project (or one unscoped, for a tag-only workstream).
	var scopes []sqlstore.TaskFilter
	base := sqlstore.TaskFilter{ExcludeInternal: true}
	if out.Tag != "" {
		base.TagSlugs = []string{out.Tag}
	}
	if len(out.ProjectIDs) == 0 {
		scopes = append(scopes, base)
	}
	for _, p := range out.ProjectIDs {
		f := base
		f.ProjectID = p
		scopes = append(scopes, f)
	}

	seen := map[string]bool{}
	var open []sqlstore.TaskRecord
	for _, f := range scopes {
		for _, st := range CanonicalStatuses {
			cf := f
			cf.Status = st
			n, err := s.store.CountTasks(cf)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				out.Counts[st] += n
				out.Total += n
			}
		}
		of := f
		of.Statuses = openStatuses
		of.SortBy, of.SortDir, of.Limit = "updated_at", "desc", workstreamTaskLimit
		page, err := s.store.ListTasksPage(of)
		if err != nil {
			return nil, err
		}
		out.OpenTotal += page.Total
		for _, t := range page.Tasks {
			if !seen[t.ID] {
				seen[t.ID] = true
				open = append(open, t)
			}
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].UpdatedAt.After(open[j].UpdatedAt) })
	if len(open) > workstreamTaskLimit {
		open = open[:workstreamTaskLimit]
	}
	for _, t := range open {
		out.Open = append(out.Open, briefOf(t))
	}
	return out, nil
}
