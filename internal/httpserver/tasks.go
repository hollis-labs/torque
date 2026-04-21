package httpserver

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// taskJSON converts a TaskRecord to a JSON-friendly map with snake_case keys
// and proper null handling for sql.Null* types. Tags, the run aggregate,
// and subtodos are passed in so the caller can batch-load them rather than
// requiring a store handle here. When agg is nil, stats render as zeroes.
// Nil subtodos is serialized as an empty array so the client can always
// rely on the key being present.
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord, agg *sqlstore.TaskRunAggregate, subtodos []sqlstore.Subtodo) map[string]interface{} {
	stats := map[string]interface{}{
		"run_count":         0,
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"cost":              0.0,
	}
	if agg != nil {
		stats["run_count"] = agg.Count
		stats["prompt_tokens"] = agg.PromptTokens
		stats["completion_tokens"] = agg.CompletionTokens
		stats["cost"] = agg.Cost
	}
	if subtodos == nil {
		subtodos = []sqlstore.Subtodo{}
	}
	return map[string]interface{}{
		"id":                 t.ID,
		"title":              t.Title,
		"description":        t.Description,
		"status":             t.Status,
		"priority":           t.Priority,
		"tags":               tagsJSON(tags),
		"manual":             t.Manual,
		"executor":           t.Executor,
		"agent_profile":      t.AgentProfile,
		"working_dir":        t.WorkingDir,
		"tools":              parseStringArray(t.Tools),
		"permissions":        parseFreeMap(t.Permissions),
		"environment":        parseStringMap(t.Environment),
		"system_prompt":      t.SystemPrompt,
		"agent_file":         t.AgentFile,
		"files":              parseStringArray(t.Files),
		"cost_budget":        nullFloat(t.CostBudget),
		"max_retries":        t.MaxRetries,
		"max_duration_ms":    nullInt(t.MaxDurationMs),
		"token_budget":       nullInt(t.TokenBudget),
		"on_done":            t.OnDone,
		"on_fail":            t.OnFail,
		"on_review":          t.OnReview,
		"on_done_merge":      t.OnDoneMerge,
		"escalation_chain":   parseStringArray(t.EscalationChain),
		"quality_gates":      parseStringArray(t.QualityGates),
		"deliverables":       parseDeliverables(t.Deliverables),
		"deliverable_preset": t.DeliverablePreset,
		"depends_on":         parseStringArray(t.DependsOn),
		"blocked_reason":     t.BlockedReason,
		"metadata":           parseFreeMap(t.Metadata),
		"sprint_id":          nullStr(t.SprintID),
		"project_id":         nullStr(t.ProjectID),
		"epic_id":            nullStr(t.EpicID),
		"created_at":         t.CreatedAt,
		"updated_at":         t.UpdatedAt,

		"kind":                   t.Kind,
		"source_type":            t.SourceType,
		"source_ref":             nullStr(t.SourceRef),
		"trust":                  t.Trust,
		"checkpoint_mode":        t.CheckpointMode,
		"on_checkpoint_response": t.OnCheckpointResponse,

		"parent_id": nullStr(t.ParentID),

		// Run roll-up — prompt/completion/cost summed across all recorded
		// runs for this task, plus a turn count. Nil aggregate renders
		// zeroes so clients can rely on the keys always being present.
		"stats": stats,

		// Structural checklist — auto-extracted from markdown `- [ ]` in the
		// description at create time, also writable via MCP/HTTP. Empty array
		// (never null) so the client can map without a guard.
		"subtodos": subtodos,
	}
}

// tasksJSON converts a slice of TaskRecord to a JSON-friendly slice.
// Loads linked tags, run aggregates, and subtodos per-task (N+1 — acceptable
// at current scale; each is an indexed single-row read per task).
func (s *Server) tasksJSON(tasks []sqlstore.TaskRecord) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, len(tasks))
	for i := range tasks {
		tags, err := s.svc.Task.ListTags(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		agg, err := s.svc.Run.Aggregate(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		subs, err := s.svc.Task.ListSubtodos(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		out[i] = taskJSON(&tasks[i], tags, agg, subs)
	}
	return out, nil
}

func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullFloat(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

func nullInt(ni sql.NullInt64) interface{} {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func nullTime(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

// TaskCreateRequest is the JSON request body for POST /api/v1/tasks.
// Field types are non-pointer where the zero value is acceptable as
// "not provided" (e.g. empty string), and pointer where we need to
// distinguish "not provided" from "explicit zero" (numeric nullables).
type TaskCreateRequest struct {
	Title             string                `json:"title"`
	Description       string                `json:"description,omitempty"`
	Priority          int                   `json:"priority,omitempty"`
	Tags              []string              `json:"tags,omitempty"`
	Manual            bool                  `json:"manual,omitempty"`
	Executor          string                `json:"executor,omitempty"`
	AgentProfile      string                `json:"agent_profile,omitempty"`
	WorkingDir        string                `json:"working_dir,omitempty"`
	Tools             []string              `json:"tools,omitempty"`
	Permissions       map[string]any        `json:"permissions,omitempty"`
	Environment       map[string]string     `json:"environment,omitempty"`
	SystemPrompt      string                `json:"system_prompt,omitempty"`
	AgentFile         string                `json:"agent_file,omitempty"`
	Files             []string              `json:"files,omitempty"`
	CostBudget        *float64              `json:"cost_budget,omitempty"`
	MaxRetries        *int                  `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                `json:"token_budget,omitempty"`
	OnDone            string                `json:"on_done,omitempty"`
	OnFail            string                `json:"on_fail,omitempty"`
	OnReview          string                `json:"on_review,omitempty"`
	OnDoneMerge       string                `json:"on_done_merge,omitempty"`
	EscalationChain   []string              `json:"escalation_chain,omitempty"`
	QualityGates      []string              `json:"quality_gates,omitempty"`
	Deliverables      []service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset string                `json:"deliverable_preset,omitempty"`
	DependsOn         []string              `json:"depends_on,omitempty"`
	BlockedReason     string                `json:"blocked_reason,omitempty"`
	Metadata          map[string]any        `json:"metadata,omitempty"`
	SprintID          string                `json:"sprint_id,omitempty"`
	ProjectID         string                `json:"project_id,omitempty"`
	EpicID            string                `json:"epic_id,omitempty"`

	// Facet fields (migration 007)
	Kind                 string `json:"kind,omitempty"`
	SourceType           string `json:"source_type,omitempty"`
	SourceRef            string `json:"source_ref,omitempty"`
	Trust                string `json:"trust,omitempty"`
	CheckpointMode       string `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse string `json:"on_checkpoint_response,omitempty"`

	// Parent linkage (migration 013)
	ParentID string `json:"parent_id,omitempty"`
}

// TaskUpdateRequest is the JSON request body for PUT /api/v1/tasks/:id.
// Every field is a pointer so "not provided" (nil) is distinguishable from
// "explicit zero/empty value". The Status field is present only so the
// handler can detect and reject it — status changes go through the
// dedicated /transition endpoint instead.
type TaskUpdateRequest struct {
	Title             *string                `json:"title,omitempty"`
	Description       *string                `json:"description,omitempty"`
	Priority          *int                   `json:"priority,omitempty"`
	Tags              *[]string              `json:"tags,omitempty"`
	Manual            *bool                  `json:"manual,omitempty"`
	Executor          *string                `json:"executor,omitempty"`
	AgentProfile      *string                `json:"agent_profile,omitempty"`
	WorkingDir        *string                `json:"working_dir,omitempty"`
	Tools             *[]string              `json:"tools,omitempty"`
	Permissions       *map[string]any        `json:"permissions,omitempty"`
	Environment       *map[string]string     `json:"environment,omitempty"`
	SystemPrompt      *string                `json:"system_prompt,omitempty"`
	AgentFile         *string                `json:"agent_file,omitempty"`
	Files             *[]string              `json:"files,omitempty"`
	CostBudget        *float64               `json:"cost_budget,omitempty"`
	MaxRetries        *int                   `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                 `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                 `json:"token_budget,omitempty"`
	OnDone            *string                `json:"on_done,omitempty"`
	OnFail            *string                `json:"on_fail,omitempty"`
	OnReview          *string                `json:"on_review,omitempty"`
	OnDoneMerge       *string                `json:"on_done_merge,omitempty"`
	EscalationChain   *[]string              `json:"escalation_chain,omitempty"`
	QualityGates      *[]string              `json:"quality_gates,omitempty"`
	Deliverables      *[]service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset *string                `json:"deliverable_preset,omitempty"`
	DependsOn         *[]string              `json:"depends_on,omitempty"`
	BlockedReason     *string                `json:"blocked_reason,omitempty"`
	Metadata          *map[string]any        `json:"metadata,omitempty"`
	SprintID          *string                `json:"sprint_id,omitempty"`
	ProjectID         *string                `json:"project_id,omitempty"`
	EpicID            *string                `json:"epic_id,omitempty"`

	// Facet fields (migration 007)
	Kind                 *string `json:"kind,omitempty"`
	SourceType           *string `json:"source_type,omitempty"`
	SourceRef            *string `json:"source_ref,omitempty"`
	Trust                *string `json:"trust,omitempty"`
	CheckpointMode       *string `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse *string `json:"on_checkpoint_response,omitempty"`

	// Parent linkage (migration 013). Empty string clears the parent.
	ParentID *string `json:"parent_id,omitempty"`

	// Status is included only for detection — the handler returns 400 if it
	// is non-nil and points the caller to POST /tasks/:id/transition.
	Status *string `json:"status,omitempty"`
}

// parseStringArray parses a NullString containing a JSON-encoded []string.
// Returns an empty slice (not nil) on missing or invalid data so the JSON
// response shape stays stable.
func parseStringArray(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// parseStringMap parses a NullString containing a JSON-encoded map[string]string.
func parseStringMap(ns sql.NullString) map[string]string {
	if !ns.Valid || ns.String == "" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// parseFreeMap parses a NullString containing a JSON-encoded free-form map.
func parseFreeMap(ns sql.NullString) map[string]any {
	if !ns.Valid || ns.String == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// parseDeliverables parses a NullString containing a JSON-encoded []Deliverable.
func parseDeliverables(ns sql.NullString) []service.Deliverable {
	if !ns.Valid || ns.String == "" {
		return []service.Deliverable{}
	}
	var out []service.Deliverable
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []service.Deliverable{}
	}
	return out
}

// nullJSONString marshals v to JSON and wraps the result in a *sql.NullString
// suitable for assigning to a TaskUpdate pointer field. Returns a nil-valued
// NullString if marshaling fails (should never happen for well-formed Go values
// the request layer accepts).
func nullJSONString(v any) *sql.NullString {
	raw, err := json.Marshal(v)
	if err != nil {
		return &sql.NullString{Valid: false}
	}
	return &sql.NullString{String: string(raw), Valid: true}
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	filter := sqlstore.TaskFilter{}
	if v := r.URL.Query().Get("status"); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) == 1 {
			filter.Status = parts[0]
		} else {
			filter.Statuses = parts
		}
	}
	if v := r.URL.Query().Get("priority"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			filter.Priority = p
		}
	}
	if v := r.URL.Query().Get("sprint_id"); v != "" {
		filter.SprintID = v
	}
	if v := r.URL.Query().Get("project_id"); v != "" {
		filter.ProjectID = v
	}
	if v := r.URL.Query().Get("epic_id"); v != "" {
		filter.EpicID = v
	}
	if v := r.URL.Query().Get("executor"); v != "" {
		filter.Executor = v
	}
	if v := r.URL.Query().Get("kind"); v != "" {
		filter.Kind = v
	}
	if v := r.URL.Query().Get("source_type"); v != "" {
		filter.SourceType = v
	}
	if v := r.URL.Query().Get("source_ref"); v != "" {
		filter.SourceRef = v
	}
	if v := r.URL.Query().Get("trust"); v != "" {
		filter.Trust = v
	}
	if v := r.URL.Query().Get("checkpoint_mode"); v != "" {
		filter.CheckpointMode = v
	}
	// parent_id filter — special value "null" returns roots (parent IS NULL).
	if _, ok := r.URL.Query()["parent_id"]; ok {
		v := r.URL.Query().Get("parent_id")
		if v == "" || v == "null" {
			filter.ParentIDNull = true
		} else {
			filter.ParentID = v
		}
	}
	// manual filter — accepts the canonical `true`/`false` plus the UI-layer
	// `manual`/`auto` aliases. Unrecognized values are silently ignored so the
	// caller can omit the param to mean "no filter".
	if v := r.URL.Query().Get("manual"); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "manual":
			t := true
			filter.Manual = &t
		case "false", "0", "auto":
			f := false
			filter.Manual = &f
		}
	}
	if v := r.URL.Query().Get("tags"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		filter.TagSlugs = out
	} else if v := r.URL.Query().Get("tag"); v != "" {
		filter.TagSlugs = []string{strings.TrimSpace(v)}
	}
	if v := r.URL.Query().Get("search"); v != "" {
		filter.Search = v
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}

	tasks, err := s.svc.Task.List(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// `total` matches the FE contract (api.ts's `listTasks` return type).
	// While no pagination is applied from this handler, `len(out)` equals the
	// full match count. If/when Limit/Offset get plumbed end-to-end, replace
	// this with a separate COUNT query using the same filter.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks": out,
		"total": len(out),
	})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(task, tags, agg, subs))
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req TaskCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	// Safety override per CW-20260417-0133: force manual=true on every task
	// create until portfolio callers stop shipping manual=false (explicitly or
	// by default). The bug: non-manual tasks are picked up by the scheduler
	// and, lacking a valid agent_profile, fail-and-burn-retries immediately.
	// Temporary. Pairs with CW-20260417-0134 (removal) and CW-20260417-0413
	// (permanent create-time validation). Update path is UNCHANGED so
	// operators can still promote reviewed tasks to manual=false explicitly.
	if !req.Manual {
		log.Printf("POST /tasks: forcing manual=true on %q (caller passed manual=false) — safety override per CW-20260417-0133", req.Title)
		req.Manual = true
	}

	input := service.TaskCreateInput{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Tags:              req.Tags,
		Manual:            req.Manual,
		Executor:          req.Executor,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		Tools:             req.Tools,
		Permissions:       req.Permissions,
		Environment:       req.Environment,
		SystemPrompt:      req.SystemPrompt,
		AgentFile:         req.AgentFile,
		Files:             req.Files,
		CostBudget:        req.CostBudget,
		MaxRetries:        req.MaxRetries,
		MaxDurationMs:     req.MaxDurationMs,
		TokenBudget:       req.TokenBudget,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		EscalationChain:   req.EscalationChain,
		QualityGates:      req.QualityGates,
		Deliverables:      req.Deliverables,
		DeliverablePreset: req.DeliverablePreset,
		DependsOn:         req.DependsOn,
		BlockedReason:     req.BlockedReason,
		Metadata:          req.Metadata,
		SprintID:          req.SprintID,
		ProjectID:         req.ProjectID,
		EpicID:            req.EpicID,

		Kind:                 req.Kind,
		SourceType:           req.SourceType,
		SourceRef:            req.SourceRef,
		Trust:                req.Trust,
		CheckpointMode:       req.CheckpointMode,
		OnCheckpointResponse: req.OnCheckpointResponse,

		ParentID: req.ParentID,
	}

	task, err := s.svc.Task.Create(input)
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Auto-extracted subtodos are persisted during Create; fetch them so the
	// client sees them on the very first response instead of needing a
	// follow-up GET.
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("task.created", map[string]interface{}{"task_id": task.ID, "title": task.Title})
	writeJSON(w, http.StatusCreated, taskJSON(task, tags, nil, subs))
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req TaskUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Status != nil {
		writeError(w, http.StatusBadRequest,
			"status updates are not allowed via PUT /tasks/:id — use POST /tasks/:id/transition")
		return
	}

	update := sqlstore.TaskUpdate{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Manual:            req.Manual,
		Executor:          req.Executor,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		SystemPrompt:      req.SystemPrompt,
		AgentFile:         req.AgentFile,
		MaxRetries:        req.MaxRetries,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		DeliverablePreset: req.DeliverablePreset,
		BlockedReason:     req.BlockedReason,
	}

	// JSON-blob fields wrap as *sql.NullString
	if req.Tools != nil {
		update.Tools = nullJSONString(*req.Tools)
	}
	if req.Permissions != nil {
		update.Permissions = nullJSONString(*req.Permissions)
	}
	if req.Environment != nil {
		update.Environment = nullJSONString(*req.Environment)
	}
	if req.Files != nil {
		update.Files = nullJSONString(*req.Files)
	}
	if req.EscalationChain != nil {
		update.EscalationChain = nullJSONString(*req.EscalationChain)
	}
	if req.QualityGates != nil {
		update.QualityGates = nullJSONString(*req.QualityGates)
	}
	if req.Deliverables != nil {
		update.Deliverables = nullJSONString(*req.Deliverables)
	}
	if req.DependsOn != nil {
		update.DependsOn = nullJSONString(*req.DependsOn)
	}
	if req.Metadata != nil {
		update.Metadata = nullJSONString(*req.Metadata)
	}

	// Numeric nullables wrap as *sql.NullFloat64 / *sql.NullInt64
	if req.CostBudget != nil {
		update.CostBudget = &sql.NullFloat64{Float64: *req.CostBudget, Valid: true}
	}
	if req.MaxDurationMs != nil {
		update.MaxDurationMs = &sql.NullInt64{Int64: *req.MaxDurationMs, Valid: true}
	}
	if req.TokenBudget != nil {
		update.TokenBudget = &sql.NullInt64{Int64: *req.TokenBudget, Valid: true}
	}

	// Association fields wrap as *sql.NullString
	if req.SprintID != nil {
		update.SprintID = &sql.NullString{String: *req.SprintID, Valid: *req.SprintID != ""}
	}
	if req.ProjectID != nil {
		update.ProjectID = &sql.NullString{String: *req.ProjectID, Valid: *req.ProjectID != ""}
	}
	if req.EpicID != nil {
		update.EpicID = &sql.NullString{String: *req.EpicID, Valid: *req.EpicID != ""}
	}

	// Facet fields (migration 007). String facets map directly; source_ref
	// wraps as *sql.NullString so the empty string clears the column.
	update.Kind = req.Kind
	update.SourceType = req.SourceType
	update.Trust = req.Trust
	update.CheckpointMode = req.CheckpointMode
	update.OnCheckpointResponse = req.OnCheckpointResponse
	if req.SourceRef != nil {
		update.SourceRef = &sql.NullString{String: *req.SourceRef, Valid: *req.SourceRef != ""}
	}
	// Parent linkage (migration 013). Empty string clears the parent.
	if req.ParentID != nil {
		update.ParentID = &sql.NullString{String: *req.ParentID, Valid: *req.ParentID != ""}
	}

	input := service.TaskUpdateInput{TaskUpdate: update, Tags: req.Tags}

	if err := s.svc.Task.Update(id, input); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.updated", map[string]interface{}{"task_id": id})
	writeJSON(w, http.StatusOK, taskJSON(task, tags, agg, subs))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Task.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transitionTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := s.svc.Task.Transition(id, req.Status); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	writeJSON(w, http.StatusOK, taskJSON(task, tags, agg, subs))
}

func (s *Server) bulkTransitionTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	updated, _ := s.svc.Task.BulkTransition(req.IDs, req.Status)
	for _, id := range req.IDs[:updated] {
		s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

func (s *Server) searchTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	tasks, err := s.svc.Task.Search(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}
