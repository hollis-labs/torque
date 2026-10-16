package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func (s *Server) taskProgress(w http.ResponseWriter, r *http.Request) { s.selfReport(w, r, false) }
func (s *Server) taskData(w http.ResponseWriter, r *http.Request)     { s.selfReport(w, r, true) }
func (s *Server) selfReport(w http.ResponseWriter, r *http.Request, data bool) {
	// Bound request decoding; inline data has a separate exact 16KiB cap.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.UseNumber()
	var in map[string]any
	if err := dec.Decode(&in); err != nil || in == nil {
		writeError(w, http.StatusBadRequest, "expected a JSON object")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "expected a single JSON object")
		return
	}
	id := chi.URLParam(r, "id")
	if requested, ok := in["task_id"]; ok && requested != id {
		writeError(w, http.StatusBadRequest, "task_id does not match path")
		return
	}
	in["task_id"] = id
	route := "POST /tasks/{id}/progress"
	if data {
		route = "POST /tasks/{id}/data"
	}
	ctx := httpTrigger(r, route).Context()
	var out map[string]any
	var err error
	if data {
		out, err = s.svc.Task.EmitData(ctx, in)
	} else {
		out, err = s.svc.Task.Progress(ctx, in)
	}
	if err != nil {
		code, status, message := "internal", http.StatusInternalServerError, "self-report failed"
		var report *service.SelfReportError
		var invalid *service.ValidationError
		switch {
		case errors.As(err, &report):
			code, message, status = report.Code, report.Message, http.StatusBadRequest
			if code == "idempotency_conflict" {
				status = http.StatusConflict
			}
			if code == "not_found" {
				status = http.StatusNotFound
			}
			if code == "data_too_large" {
				status = http.StatusRequestEntityTooLarge
			}
		case errors.As(err, &invalid):
			code, message, status = "arg_invalid", invalid.Error(), http.StatusBadRequest
		case errors.Is(err, sqlstore.ErrTaskNotFound):
			code, message, status = "not_found", err.Error(), http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": message, "code": code})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
