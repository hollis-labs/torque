package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

func requestError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, sqlstore.ErrRequestNotFound):
		code = http.StatusNotFound
	case errors.Is(err, sqlstore.ErrRequestConflict):
		code = http.StatusConflict
	case errors.Is(err, sqlstore.ErrRequestInvalid):
		code = http.StatusBadRequest
	}
	writeError(w, code, err.Error())
}

func (s *Server) registerRequest(w http.ResponseWriter, r *http.Request) {
	var in sqlstore.RequestRegistration
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	rec, err := s.svc.Store().RegisterRequest(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		requestError(w, err)
		return
	}
	writeJSON(w, 200, rec)
}
func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	rec, err := s.svc.Store().GetRequest(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		requestError(w, err)
		return
	}
	writeJSON(w, 200, rec)
}
func (s *Server) transitionRequest(w http.ResponseWriter, r *http.Request) {
	var in sqlstore.RequestTransition
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	rec, err := s.svc.Store().TransitionRequest(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		requestError(w, err)
		return
	}
	writeJSON(w, 200, rec)
}
func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	awaiting := false
	if raw := q.Get("awaiting_ack"); raw != "" {
		var err error
		awaiting, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, 400, "invalid awaiting_ack")
			return
		}
	}
	var due time.Time
	if raw := q.Get("due_before"); raw != "" {
		var err error
		due, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, 400, "invalid due_before")
			return
		}
	}
	limit := 100
	if raw := q.Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			writeError(w, 400, "limit must be between 1 and 1000")
			return
		}
	}
	items, err := s.svc.Store().ListRequests(r.Context(), awaiting, due, q.Get("after_task_id"), limit)
	if err != nil {
		requestError(w, err)
		return
	}
	next := ""
	if len(items) == limit {
		next = items[len(items)-1].TaskID
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_after_task_id": next})
}
