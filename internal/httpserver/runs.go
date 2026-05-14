package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

const (
	defaultRunsLimit = 200
	maxRunsLimit     = 1000
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
	q := r.URL.Query()

	filter := sqlstore.RunFilter{
		TaskID:    q.Get("task_id"),
		ProjectID: q.Get("project_id"),
	}

	if raw := strings.TrimSpace(q.Get("status")); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				filter.Statuses = append(filter.Statuses, s)
			}
		}
	}

	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		since, err := parseSince(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since: "+err.Error())
			return
		}
		filter.Since = since
	}

	filter.Limit = parseLimit(q.Get("limit"))

	runs, err := s.svc.Run.ListFiltered(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]runResponse, 0, len(runs))
	for _, rec := range runs {
		resp = append(resp, decorateRun(rec))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"runs": resp})
}

// parseLimit clamps the requested limit to [1, maxRunsLimit]. Invalid or
// missing values fall back to defaultRunsLimit so aggregate dashboards get
// a bounded response by default.
func parseLimit(raw string) int {
	if raw == "" {
		return defaultRunsLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultRunsLimit
	}
	if n > maxRunsLimit {
		return maxRunsLimit
	}
	return n
}

// parseSince accepts either an RFC3339 timestamp or a unix millisecond
// integer. The widgets use RFC3339; the unix path is for quick curl calls.
func parseSince(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	return time.Time{}, errInvalidSince
}

var errInvalidSince = &parseError{"expected RFC3339 timestamp or unix millis"}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

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
