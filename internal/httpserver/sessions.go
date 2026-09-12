package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/sessioninput"
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
		agent.Status(q.Get("state")),
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
	LaunchProfile string            `json:"launch_profile,omitempty"`
	AgentProfile  string            `json:"agent_profile,omitempty"`
	Workdir       string            `json:"workdir"`
	ProjectID     string            `json:"project_id,omitempty"`
	TaskID        string            `json:"task_id,omitempty"`
	SystemPrompt  string            `json:"system_prompt,omitempty"`
	Env           []string          `json:"env,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
}

func (b *launchSessionBody) UnmarshalJSON(data []byte) error {
	type wire launchSessionBody
	var raw struct {
		wire
		Meta json.RawMessage `json:"meta,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = launchSessionBody(raw.wire)
	if len(raw.Meta) != 0 {
		meta, err := sessioninput.DecodeStringMapJSON(raw.Meta, "meta")
		if err != nil {
			return err
		}
		b.Meta = meta
	}
	return nil
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
	// Validate the legacy agent_profile only when launch_profile is empty —
	// when launch_profile is supplied it drives the resolver's compatibility
	// path directly, and an empty agent_profile is the expected shape.
	if body.LaunchProfile == "" && s.sessions != nil && s.sessions.KnownProfiles() != nil {
		if err := config.ValidateProfileName(s.sessions.KnownProfiles(), body.AgentProfile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	envMap := envSliceToMap(body.Env)
	sess, err := s.sessions.Boot(r.Context(), agent.Options{
		Mode:          agent.ModeLongLived,
		LaunchProfile: body.LaunchProfile,
		AgentProfile:  body.AgentProfile,
		Workdir:       body.Workdir,
		ProjectID:     body.ProjectID,
		TaskID:        body.TaskID,
		SystemPrompt:  body.SystemPrompt,
		Env:           envMap,
		SessionMeta:   body.Meta,
	})
	if err != nil {
		// Distinguish caller-fault validation (400) from server-side Boot
		// failures (loopback bind, bootdir/workspace creation, runtime
		// prepare, Manager.Start). agent.Boot wraps server-side failures
		// with ErrBootFailed; validation errors (Validate(), bad workdir,
		// missing ParentSessionID, etc.) propagate as plain errors.
		// Per Copilot review feedback on PR #19.
		switch {
		case errors.Is(err, agent.ErrBootFailed):
			writeError(w, http.StatusInternalServerError, err.Error())
		case errors.Is(err, agent.ErrAdapterNotFound):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, agent.ErrWorkdirRequired),
			errors.Is(err, agent.ErrParentSessionRequired),
			errors.Is(err, agent.ErrResumeCheckpointRequired):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

// envSliceToMap converts the legacy []string "K=V" env shape (carried by
// the HTTP API for back-compat with the old sessionmgr.LaunchRequest) into
// the map[string]string shape agent.Options expects. Malformed entries
// (no '=') are silently skipped.
func envSliceToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessions(w) {
		return
	}
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
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
		if errors.Is(err, agent.ErrSessionNotRunning) {
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
	writeWaitResponse(w, id, code, err)
}

// writeWaitResponse encodes the (code, err) result from a session-wait
// operation into the HTTP response. Termination errors (*ExitError,
// *exec.ExitError) are the SUCCESS path of a wait — the session ended;
// here's how. Surface them as 200 OK with structured cause/signal/killed
// fields in the body. Reserve 5xx for wait-operation failures only
// (context cancellation maps to 408, ErrSessionNotRunning to 404).
//
// Per go-agent-sessions v0.7.0: Wait propagates Session.Wait's error
// verbatim instead of swallowing it. Consumers errors.As-classify
// supervised terminations via xe.Cause without string-matching.
func writeWaitResponse(w http.ResponseWriter, id string, code int, err error) {
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotRunning) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusRequestTimeout, err.Error())
			return
		}
		// Termination error — extract structured ExitError fields when present
		// so consumers can classify watchdog_kill vs idle_timeout vs ordinary
		// non-zero exits without string-matching err.Error().
		resp := map[string]interface{}{"id": id, "exit_code": code}
		var xe *agentsessions.ExitError
		if errors.As(err, &xe) {
			resp["cause"] = xe.Cause
			resp["signal"] = xe.Signal
			resp["killed"] = xe.Killed
		} else {
			resp["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, resp)
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
		if errors.Is(err, agent.ErrSessionNotRunning) {
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
	cp, err := s.sessions.Checkpoint(agent.CheckpointRequest{
		SessionID: id,
		Payload:   body.Payload,
		Note:      body.Note,
	})
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
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
	CheckpointID  string   `json:"checkpoint_id,omitempty"`
	LaunchProfile string   `json:"launch_profile,omitempty"`
	AgentProfile  string   `json:"agent_profile,omitempty"`
	Workdir       string   `json:"workdir,omitempty"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	Env           []string `json:"env,omitempty"`
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
	newID, err := s.sessions.Resume(r.Context(), agent.ResumeRequest{
		SessionID:     id,
		CheckpointID:  body.CheckpointID,
		LaunchProfile: body.LaunchProfile,
		AgentProfile:  body.AgentProfile,
		Workdir:       body.Workdir,
		SystemPrompt:  body.SystemPrompt,
		Env:           body.Env,
	})
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, agent.ErrNoCheckpoint):
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
