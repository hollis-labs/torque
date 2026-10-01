package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// runResponse wraps sqlstore.RunRecord with derived provider/model fields
// extracted from the run's metadata JSON. The embedded RunRecord fields are
// marshaled at the top level (unchanged from the previous shape), so this is
// purely additive for existing API consumers.
type runResponse struct {
	sqlstore.RunRecord
	Provider string `json:",omitempty"`
	Model    string `json:",omitempty"`
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{}
	for _, key := range []string{"task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until", "limit", "offset", "sort_by", "sort_dir", "cursor", "include_total"} {
		allowed[key] = true
	}
	q, e := parseStrictQuery(r, allowed)
	if e != nil {
		writeError(w, http.StatusBadRequest, e.Error())
		return
	}
	query := service.RunQuery{TaskID: q.Get("task_id"), ProjectID: q.Get("project_id"), SprintID: q.Get("sprint_id"), EpicID: q.Get("epic_id"), Statuses: strings.Split(q.Get("status"), ","), Executors: strings.Split(q.Get("executor"), ","), Profiles: strings.Split(q.Get("profile"), ","), Since: q.Get("since"), Until: q.Get("until"), SortBy: q.Get("sort_by"), SortDir: q.Get("sort_dir"), Cursor: q.Get("cursor")}
	query.Limit, e = queryInt(q, "limit")
	if e != nil {
		writeError(w, http.StatusBadRequest, e.Error())
		return
	}
	query.OffsetSet = q.Has("offset")
	query.Offset, e = queryInt(q, "offset")
	if e != nil {
		writeError(w, http.StatusBadRequest, e.Error())
		return
	}
	if raw, ok := q["include_total"]; ok {
		v, err := strconv.ParseBool(raw[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "include_total must be a boolean")
			return
		}
		query.IncludeTotal = v
	}
	result, err := s.svc.Run.Query(query)
	if err != nil {
		var ve *service.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	items := make([]runResponse, 0, len(result.Runs))
	for _, rec := range result.Runs {
		items = append(items, decorateRun(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "meta": result.Meta})
}

// decorateRun extracts provider and model from the run's metadata JSON.
// Provider falls back to the executor field so dashboards always have a
// non-empty value to group by; model is left empty when absent.
func decorateRun(r sqlstore.RunRecord) runResponse {
	out := runResponse{RunRecord: r}

	var meta map[string]interface{}
	if r.Metadata.Valid && r.Metadata.String != "" {
		_ = json.Unmarshal([]byte(r.Metadata.String), &meta)
	}

	if v, ok := meta["provider"].(string); ok && v != "" {
		out.Provider = v
	} else {
		out.Provider = r.Executor
	}
	if v, ok := meta["model"].(string); ok {
		out.Model = v
	}
	return out
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run ID")
		return
	}
	run, err := s.svc.Run.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}
