package httpserver

import (
	"net/http"

	"github.com/hollis-labs/torque/internal/service"
)

func (s *Server) entityFacets(entity string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed := map[string]bool{"status": true, "include_archived": true, "dimensions": true, "bucket_limit": true, "search": true, "rollup_ids": true}
		if entity != "projects" {
			allowed["project_id"] = true
		}
		if entity == "sprints" {
			for _, key := range []string{"over_budget", "cost_budget_min", "cost_budget_max"} {
				allowed[key] = true
			}
		}
		q, qerr := parseStrictQuery(r, allowed)
		if qerr != nil {
			writeHTTPQueryError(w, qerr)
			return
		}
		archived, qerr := queryBool(q, "include_archived")
		if qerr != nil {
			writeHTTPQueryError(w, qerr)
			return
		}
		limit, qerr := queryInt(q, "bucket_limit")
		if qerr != nil {
			writeHTTPQueryError(w, qerr)
			return
		}
		opts := service.FacetOptions{BucketLimit: limit}
		if _, ok := q["dimensions"]; ok {
			opts.Dimensions = splitHTTPFacetCSV(q.Get("dimensions"))
		}
		if _, ok := q["rollup_ids"]; ok {
			opts.RollupIDs = splitHTTPFacetCSV(q.Get("rollup_ids"))
		}
		status := q.Get("status")
		switch entity {
		case "projects":
			result, err := s.svc.Project.Facets(service.ProjectFacetQuery{ProjectQuery: service.ProjectQuery{Status: status, Search: q.Get("search"), IncludeArchived: archived}, FacetOptions: opts})
			if err != nil {
				writeAdjacentServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
		case "epics":
			result, err := s.svc.Epic.Facets(service.EpicFacetQuery{EpicQuery: service.EpicQuery{Status: status, ProjectID: q.Get("project_id"), Search: q.Get("search"), IncludeArchived: archived}, FacetOptions: opts})
			if err != nil {
				writeAdjacentServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
		case "sprints":
			over, qerr := queryBool(q, "over_budget")
			if qerr != nil {
				writeHTTPQueryError(w, qerr)
				return
			}
			min, qerr := queryFloatPtr(q, "cost_budget_min")
			if qerr != nil {
				writeHTTPQueryError(w, qerr)
				return
			}
			max, qerr := queryFloatPtr(q, "cost_budget_max")
			if qerr != nil {
				writeHTTPQueryError(w, qerr)
				return
			}
			result, err := s.svc.Sprint.Facets(service.SprintFacetQuery{SprintQuery: service.SprintQuery{Status: status, Search: q.Get("search"), ProjectID: q.Get("project_id"), IncludeArchived: archived, OverBudget: over, CostBudgetMin: min, CostBudgetMax: max}, FacetOptions: opts})
			if err != nil {
				writeAdjacentServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
		}
	}
}
