package httpserver

import (
	"github.com/hollis-labs/torque/internal/service"
	"net/http"
	"strings"
)

func (s *Server) runTimeSeries(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{}
	for _, key := range []string{"task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until", "bucket", "tz_offset_minutes"} {
		allowed[key] = true
	}
	q, e := parseStrictQuery(r, allowed)
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	offset, e := queryInt(q, "tz_offset_minutes")
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	result, err := s.svc.Run.TimeSeries(service.RunTimeSeriesQuery{RunQuery: service.RunQuery{TaskID: q.Get("task_id"), ProjectID: q.Get("project_id"), SprintID: q.Get("sprint_id"), EpicID: q.Get("epic_id"), Statuses: strings.Split(q.Get("status"), ","), Executors: strings.Split(q.Get("executor"), ","), Profiles: strings.Split(q.Get("profile"), ","), Since: q.Get("since"), Until: q.Get("until")}, Bucket: q.Get("bucket"), TZOffsetMinutes: offset})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
