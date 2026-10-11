package httpserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

type claimRequest struct {
	TeamID         string   `json:"team_id"`
	TeamRunID      string   `json:"team_run_id"`
	Actor          string   `json:"actor"`
	EnvironmentID  string   `json:"environment_id"`
	ProjectScope   []string `json:"project_scope"`
	IdempotencyKey string   `json:"idempotency_key"`
}

// claimWorkstream is claim-next: a free team run asks for the next cleared
// workstream. Dispatch, Defer and Refuse are all 200; the body's outcome says
// which. Go/pause comes from the scheduler's enabled flag, so a process with
// no scheduler cannot hand out work.
func (s *Server) claimWorkstream(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	res, err := s.svc.Workstream.ClaimNext(r.Context(), service.ClaimInput{
		TeamID: req.TeamID, TeamRunID: req.TeamRunID, Actor: req.Actor,
		EnvironmentID: req.EnvironmentID, ProjectScope: req.ProjectScope,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) heartbeatWorkstreamClaim(w http.ResponseWriter, r *http.Request) {
	until, err := s.svc.Workstream.Heartbeat(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeWorkstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claim_id": chi.URLParam(r, "id"), "lease_expires_at": until})
}

func (s *Server) releaseWorkstreamClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Outcome    string `json:"outcome"`
		ResultRefs string `json:"result_refs"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	res, err := s.svc.Workstream.Release(r.Context(), chi.URLParam(r, "id"), req.Outcome, req.ResultRefs)
	if err != nil {
		writeWorkstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeWorkstreamError(w http.ResponseWriter, err error) {
	var verr *service.ValidationError
	var terr *service.TransitionError
	switch {
	case errors.Is(err, sqlstore.ErrClaimNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &verr):
		writeFieldError(w, http.StatusBadRequest, verr.Field, verr.Message)
	case errors.As(err, &terr):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// listWorkstreamClaims lists claims, default status active. Each item carries
// expired=true when its lease has run out.
func (s *Server) listWorkstreamClaims(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "released" && status != "all" {
		writeError(w, http.StatusBadRequest, "status must be active, released or all")
		return
	}
	q := status
	if status == "all" {
		q = ""
	}
	claims, err := s.svc.Workstream.ListClaims(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	items := make([]map[string]any, 0, len(claims))
	for _, c := range claims {
		items = append(items, map[string]any{
			"claim_id": c.ID, "task_id": c.TaskID, "project_id": c.ProjectID,
			"team_id": c.TeamID, "team_run_id": c.TeamRunID, "actor": c.Actor,
			"environment_id": c.EnvironmentID, "status": c.Status, "outcome": c.Outcome,
			"lease_expires_at": c.LeaseExpiresAt,
			"expired":          c.Status == "active" && c.LeaseExpiresAt.Before(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
