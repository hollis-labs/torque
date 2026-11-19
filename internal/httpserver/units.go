package httpserver

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

type dispatchUnitRequest struct {
	TaskID        string `json:"task_id"`
	Workers       int    `json:"workers"`
	WorkerRuntime string `json:"worker_runtime"`
}

// dispatchUnit is the explicit "dispatch unit" action: it forms a Tether team
// for a task with an execution plan, briefs the orchestrator and starts
// tracking the run. Idempotent: a task with a live run returns that run
// (reused=true). Works whether or not the scheduler is enabled.
func (s *Server) dispatchUnit(w http.ResponseWriter, r *http.Request) {
	var req dispatchUnitRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	res, err := s.svc.Unit.Dispatch(r.Context(), service.UnitDispatchInput{
		TaskID: req.TaskID, Workers: req.Workers, WorkerRuntime: req.WorkerRuntime,
	})
	if err != nil {
		var verr *service.ValidationError
		switch {
		case errors.As(err, &verr):
			writeFieldError(w, http.StatusBadRequest, verr.Field, verr.Message)
		case errors.Is(err, sqlstore.ErrTaskNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			// Formation or briefing failed; the task was annotated and any run dissolved.
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}
