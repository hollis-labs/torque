package httpserver

import (
	"net/http"

	"github.com/hollis-labs/torque/internal/service"
	"strings"
)

func (s *Server) runFacets(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{}
	for _, key := range []string{"task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until", "dimensions", "bucket_limit"} {
		allowed[key] = true
	}
	q, e := parseStrictQuery(r, allowed)
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	limit, e := queryInt(q, "bucket_limit")
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	opts := service.FacetOptions{BucketLimit: limit}
	if _, ok := q["dimensions"]; ok {
		opts.Dimensions = splitHTTPFacetCSV(q.Get("dimensions"))
	}
	result, err := s.svc.Run.Facets(service.RunFacetQuery{RunQuery: service.RunQuery{TaskID: q.Get("task_id"), ProjectID: q.Get("project_id"), SprintID: q.Get("sprint_id"), EpicID: q.Get("epic_id"), Statuses: strings.Split(q.Get("status"), ","), Executors: strings.Split(q.Get("executor"), ","), Profiles: strings.Split(q.Get("profile"), ","), Since: q.Get("since"), Until: q.Get("until")}, FacetOptions: opts})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
