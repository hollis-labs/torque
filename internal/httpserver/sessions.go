package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
)

// requireSessions writes a 503 envelope when the session manager is not
// wired (mcp-only / test paths). Returns false to short-circuit handlers.
func (s *Server) requireSessions(w http.ResponseWriter) bool {
	if s.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "sessions disabled: manager not configured")
		return false
	}
	return true
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := s.sessions.List(
		sessionmgr.Status(q.Get("state")),
		q.Get("task_id"),
		q.Get("project_id"),
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": out})
}

type launchSessionBody struct {
	AgentProfile string            `json:"agent_profile"`
	Workdir      string            `json:"workdir"`
	ProjectID    string            `json:"project_id,omitempty"`
	TaskID       string            `json:"task_id,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	Env          []string          `json:"env,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`
}

func (s *Server) launchSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	var body launchSessionBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	id, err := s.sessions.Launch(r.Context(), sessionmgr.LaunchRequest{
		AgentProfile: body.AgentProfile,
		Workdir:      body.Workdir,
		ProjectID:    body.ProjectID,
		TaskID:       body.TaskID,
		SystemPrompt: body.SystemPrompt,
		Env:          body.Env,
		SessionMeta:  body.Meta,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.sessions.Stop(ctx, id); err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotRunning) {
			writeJSON(w, http.StatusOK, map[string]interface{}{"stopped": false, "reason": err.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"stopped": true, "id": id})
}

type waitSessionBody struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

func (s *Server) waitSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var body waitSessionBody
	_ = readJSON(r, &body)
	ctx := r.Context()
	if body.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(body.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	code, err := s.sessions.Wait(ctx, id)
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotRunning) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "exit_code": code})
}

type resizeBody struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

func (s *Server) resizeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var body resizeBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.sessions.Resize(id, body.Rows, body.Cols); err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotRunning) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"resized": true, "id": id})
}

type checkpointBody struct {
	Payload string `json:"payload"`
	Note    string `json:"note,omitempty"`
}

func (s *Server) checkpointSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var body checkpointBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	cp, err := s.sessions.Checkpoint(sessionmgr.CheckpointRequest{
		SessionID: id,
		Payload:   body.Payload,
		Note:      body.Note,
	})
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cp)
}

func (s *Server) listSessionCheckpoints(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cps, err := s.sessions.ListCheckpoints(id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": cps})
}

type resumeBody struct {
	CheckpointID string   `json:"checkpoint_id,omitempty"`
	AgentProfile string   `json:"agent_profile,omitempty"`
	Workdir      string   `json:"workdir,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	Env          []string `json:"env,omitempty"`
}

func (s *Server) resumeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var body resumeBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	newID, err := s.sessions.Resume(r.Context(), sessionmgr.ResumeRequest{
		SessionID:    id,
		CheckpointID: body.CheckpointID,
		AgentProfile: body.AgentProfile,
		Workdir:      body.Workdir,
		SystemPrompt: body.SystemPrompt,
		Env:          body.Env,
	})
	if err != nil {
		switch {
		case errors.Is(err, sessionmgr.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, sessionmgr.ErrNoCheckpoint):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	sess, err := s.sessions.Get(newID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}
