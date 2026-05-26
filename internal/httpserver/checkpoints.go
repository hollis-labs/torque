package httpserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// checkpointJSON projects a CheckpointRecord into the HTTP response shape —
// lowercase snake_case keys with proper null handling for nullable fields.
func checkpointJSON(cp *sqlstore.CheckpointRecord, policy *hitl.RequiredWorkflowPolicy) map[string]interface{} {
	body := map[string]interface{}{
		"id":                    cp.ID,
		"task_id":               cp.TaskID,
		"run_id":                nullInt(cp.RunID),
		"correlation_id":        cp.CorrelationID,
		"type":                  cp.Type,
		"payload_json":          cp.PayloadJSON,
		"response_json":         nullStr(cp.ResponseJSON),
		"emitter_source_type":   cp.EmitterSourceType,
		"emitter_source_ref":    nullStr(cp.EmitterSourceRef),
		"responder_source_type": nullStr(cp.ResponderSourceType),
		"responder_source_ref":  nullStr(cp.ResponderSourceRef),
		"emitted_at":            cp.EmittedAt,
		"responded_at":          nullTime(cp.RespondedAt),
		"timeout_at":            nullTime(cp.TimeoutAt),
		"status":                cp.Status,
	}
	if policy != nil {
		responderSourceType := ""
		if cp.ResponderSourceType.Valid {
			responderSourceType = cp.ResponderSourceType.String
		}
		body["required_workflow_policy"] = *policy
		body["required_workflow_satisfaction"] = hitl.CheckpointSatisfiesRequiredWorkflow(*policy, hitl.CheckpointState{
			Type:                cp.Type,
			Status:              cp.Status,
			ResponderSourceType: responderSourceType,
		})
	}
	return body
}

func (s *Server) emitCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID            string `json:"task_id"`
		Type              string `json:"type"`
		PayloadJSON       string `json:"payload_json"`
		EmitterSourceType string `json:"emitter_source_type,omitempty"`
		EmitterSourceRef  string `json:"emitter_source_ref,omitempty"`
		TimeoutAt         string `json:"timeout_at,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	in := service.CheckpointEmitInput{
		TaskID:            req.TaskID,
		Type:              req.Type,
		PayloadJSON:       req.PayloadJSON,
		EmitterSourceType: req.EmitterSourceType,
		EmitterSourceRef:  req.EmitterSourceRef,
	}
	if req.TimeoutAt != "" {
		t, err := time.Parse(time.RFC3339, req.TimeoutAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid timeout_at: "+err.Error())
			return
		}
		in.TimeoutAt = &t
	}

	out, err := s.svc.Checkpoint.Emit(in)
	if err != nil {
		s.writeCheckpointError(w, err)
		return
	}
	cp, err := s.svc.Checkpoint.Get(out.CorrelationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("checkpoint.emitted", map[string]interface{}{
		"correlation_id": out.CorrelationID,
		"task_id":        req.TaskID,
	})
	writeJSON(w, http.StatusCreated, checkpointJSON(cp, s.requiredWorkflowPolicyForTask(cp.TaskID)))
}

func (s *Server) respondCheckpoint(w http.ResponseWriter, r *http.Request) {
	corr := chi.URLParam(r, "correlation_id")
	var req struct {
		ResponseJSON        string `json:"response_json"`
		ResponderSourceType string `json:"responder_source_type"`
		ResponderSourceRef  string `json:"responder_source_ref,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Checkpoint.Respond(r.Context(), service.CheckpointRespondInput{
		CorrelationID:       corr,
		ResponseJSON:        req.ResponseJSON,
		ResponderSourceType: req.ResponderSourceType,
		ResponderSourceRef:  req.ResponderSourceRef,
	}); err != nil {
		s.writeCheckpointError(w, err)
		return
	}
	cp, err := s.svc.Checkpoint.Get(corr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("checkpoint.responded", map[string]interface{}{
		"correlation_id": corr,
		"task_id":        cp.TaskID,
	})
	writeJSON(w, http.StatusOK, checkpointJSON(cp, s.requiredWorkflowPolicyForTask(cp.TaskID)))
}

func (s *Server) cancelCheckpoint(w http.ResponseWriter, r *http.Request) {
	corr := chi.URLParam(r, "correlation_id")
	var req struct {
		Reason             string `json:"reason,omitempty"`
		CancelerSourceType string `json:"canceler_source_type,omitempty"`
		CancelerSourceRef  string `json:"canceler_source_ref,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      corr,
		Reason:             req.Reason,
		CancelerSourceType: req.CancelerSourceType,
		CancelerSourceRef:  req.CancelerSourceRef,
	}); err != nil {
		s.writeCheckpointError(w, err)
		return
	}
	cp, err := s.svc.Checkpoint.Get(corr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("checkpoint.canceled", map[string]interface{}{
		"correlation_id": corr,
		"task_id":        cp.TaskID,
	})
	writeJSON(w, http.StatusOK, checkpointJSON(cp, s.requiredWorkflowPolicyForTask(cp.TaskID)))
}

func (s *Server) getCheckpoint(w http.ResponseWriter, r *http.Request) {
	corr := chi.URLParam(r, "correlation_id")
	cp, err := s.svc.Checkpoint.Get(corr)
	if err != nil {
		s.writeCheckpointError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checkpointJSON(cp, s.requiredWorkflowPolicyForTask(cp.TaskID)))
}

func (s *Server) listTaskCheckpoints(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	list, err := s.svc.Checkpoint.ListForTask(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, len(list))
	policy := s.requiredWorkflowPolicyForTask(taskID)
	for i := range list {
		out[i] = checkpointJSON(&list[i], policy)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": out})
}

func (s *Server) listPendingCheckpoints(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.Checkpoint.ListPending()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, len(list))
	policies := s.requiredWorkflowPoliciesForCheckpoints(list)
	for i := range list {
		out[i] = checkpointJSON(&list[i], policies[list[i].TaskID])
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": out})
}

func (s *Server) requiredWorkflowPolicyForTask(taskID string) *hitl.RequiredWorkflowPolicy {
	task, err := s.svc.Task.Get(taskID)
	if err != nil {
		return nil
	}
	policy, ok := requiredWorkflowPolicyFromTask(task)
	if !ok {
		return nil
	}
	return &policy
}

func (s *Server) requiredWorkflowPoliciesForCheckpoints(list []sqlstore.CheckpointRecord) map[string]*hitl.RequiredWorkflowPolicy {
	policies := make(map[string]*hitl.RequiredWorkflowPolicy)
	for i := range list {
		taskID := list[i].TaskID
		if _, seen := policies[taskID]; seen {
			continue
		}
		policies[taskID] = s.requiredWorkflowPolicyForTask(taskID)
	}
	return policies
}

// writeCheckpointError maps service-layer errors to the canonical HTTP codes.
// Order matters: ConflictError before NotFound so the more-specific status
// wins when both apply (e.g. respond on a canceled checkpoint returns 409,
// not 404).
func (s *Server) writeCheckpointError(w http.ResponseWriter, err error) {
	var verr *service.ValidationError
	if errors.As(err, &verr) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var cerr *service.ConflictError
	if errors.As(err, &cerr) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, sqlstore.ErrCheckpointNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
