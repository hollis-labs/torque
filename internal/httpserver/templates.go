package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// templateJSON projects a TemplateRecord into the HTTP response shape.
// Snake-case keys; sql.Null* fields collapse to their scalar value or nil.
func templateJSON(t *sqlstore.TemplateRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":                     t.ID,
		"version":                t.Version,
		"name":                   t.Name,
		"description":            t.Description,
		"kind":                   t.Kind,
		"auto_execute":           t.AutoExecute,
		"executor":               nullStr(t.Executor),
		"agent_profile":          nullStr(t.AgentProfile),
		"system_prompt":          nullStr(t.SystemPrompt),
		"working_dir":            nullStr(t.WorkingDir),
		"tools":                  parseStringArray(t.Tools),
		"permissions":            parseFreeMap(t.Permissions),
		"environment":            parseStringMap(t.Environment),
		"cost_budget":            nullFloat(t.CostBudget),
		"max_retries":            t.MaxRetries,
		"max_duration_ms":        nullInt(t.MaxDurationMs),
		"token_budget":           nullInt(t.TokenBudget),
		"on_done":                t.OnDone,
		"on_fail":                t.OnFail,
		"on_review":              t.OnReview,
		"on_done_merge":          t.OnDoneMerge,
		"escalation_chain":       parseStringArray(t.EscalationChain),
		"quality_gates":          parseStringArray(t.QualityGates),
		"deliverables":           parseDeliverables(t.Deliverables),
		"checkpoint_mode":        t.CheckpointMode,
		"on_checkpoint_response": t.OnCheckpointResponse,
		"metadata_template":      parseFreeMap(t.MetadataTemplate),
		"required_vars":          parseStringArray(t.RequiredVars),
		"tags":                   parseStringArray(t.Tags),
		"is_archived":            t.IsArchived,
		"created_at":             t.CreatedAt,
		"updated_at":             t.UpdatedAt,
	}
}

type templateCreateRequest struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Description          string                `json:"description"`
	Kind                 string                `json:"kind"`
	AutoExecute          *bool                 `json:"auto_execute,omitempty"`
	Executor             string                `json:"executor,omitempty"`
	AgentProfile         string                `json:"agent_profile,omitempty"`
	SystemPrompt         string                `json:"system_prompt,omitempty"`
	WorkingDir           string                `json:"working_dir,omitempty"`
	Tools                []string              `json:"tools,omitempty"`
	Permissions          map[string]any        `json:"permissions,omitempty"`
	Environment          map[string]string     `json:"environment,omitempty"`
	CostBudget           *float64              `json:"cost_budget,omitempty"`
	MaxRetries           *int                  `json:"max_retries,omitempty"`
	MaxDurationMs        *int64                `json:"max_duration_ms,omitempty"`
	TokenBudget          *int64                `json:"token_budget,omitempty"`
	OnDone               string                `json:"on_done,omitempty"`
	OnFail               string                `json:"on_fail,omitempty"`
	OnReview             string                `json:"on_review,omitempty"`
	OnDoneMerge          string                `json:"on_done_merge,omitempty"`
	EscalationChain      []string              `json:"escalation_chain,omitempty"`
	QualityGates         []string              `json:"quality_gates,omitempty"`
	Deliverables         []service.Deliverable `json:"deliverables,omitempty"`
	CheckpointMode       string                `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse string                `json:"on_checkpoint_response,omitempty"`
	MetadataTemplate     map[string]any        `json:"metadata_template,omitempty"`
	RequiredVars         []string              `json:"required_vars,omitempty"`
	Tags                 []string              `json:"tags,omitempty"`
}

type templateUpdateRequest struct {
	Name                 string                `json:"name,omitempty"`
	Description          string                `json:"description,omitempty"`
	Kind                 string                `json:"kind,omitempty"`
	AutoExecute          *bool                 `json:"auto_execute,omitempty"`
	Executor             *string               `json:"executor,omitempty"`
	AgentProfile         *string               `json:"agent_profile,omitempty"`
	SystemPrompt         *string               `json:"system_prompt,omitempty"`
	WorkingDir           *string               `json:"working_dir,omitempty"`
	Tools                []string              `json:"tools,omitempty"`
	Permissions          map[string]any        `json:"permissions,omitempty"`
	Environment          map[string]string     `json:"environment,omitempty"`
	CostBudget           *float64              `json:"cost_budget,omitempty"`
	MaxRetries           *int                  `json:"max_retries,omitempty"`
	MaxDurationMs        *int64                `json:"max_duration_ms,omitempty"`
	TokenBudget          *int64                `json:"token_budget,omitempty"`
	OnDone               string                `json:"on_done,omitempty"`
	OnFail               string                `json:"on_fail,omitempty"`
	OnReview             string                `json:"on_review,omitempty"`
	OnDoneMerge          string                `json:"on_done_merge,omitempty"`
	EscalationChain      []string              `json:"escalation_chain,omitempty"`
	QualityGates         []string              `json:"quality_gates,omitempty"`
	Deliverables         []service.Deliverable `json:"deliverables,omitempty"`
	CheckpointMode       string                `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse string                `json:"on_checkpoint_response,omitempty"`
	MetadataTemplate     map[string]any        `json:"metadata_template,omitempty"`
	RequiredVars         []string              `json:"required_vars,omitempty"`
	Tags                 []string              `json:"tags,omitempty"`
}

type templateInstantiateRequest struct {
	TemplateVersion int               `json:"template_version,omitempty"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Vars            map[string]string `json:"vars,omitempty"`
	Overrides       map[string]any    `json:"overrides,omitempty"`
	SprintID        string            `json:"sprint_id,omitempty"`
	ProjectID       string            `json:"project_id,omitempty"`
	EpicID          string            `json:"epic_id,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	in := service.TemplateCreateInput{
		ID:                   req.ID,
		Name:                 req.Name,
		Description:          req.Description,
		Kind:                 req.Kind,
		AutoExecute:          true, // default; overridden below if explicit
		Executor:             req.Executor,
		AgentProfile:         req.AgentProfile,
		SystemPrompt:         req.SystemPrompt,
		WorkingDir:           req.WorkingDir,
		Tools:                req.Tools,
		Permissions:          req.Permissions,
		Environment:          req.Environment,
		CostBudget:           req.CostBudget,
		MaxRetries:           req.MaxRetries,
		MaxDurationMs:        req.MaxDurationMs,
		TokenBudget:          req.TokenBudget,
		OnDone:               req.OnDone,
		OnFail:               req.OnFail,
		OnReview:             req.OnReview,
		OnDoneMerge:          req.OnDoneMerge,
		EscalationChain:      req.EscalationChain,
		QualityGates:         req.QualityGates,
		Deliverables:         req.Deliverables,
		CheckpointMode:       req.CheckpointMode,
		OnCheckpointResponse: req.OnCheckpointResponse,
		MetadataTemplate:     req.MetadataTemplate,
		RequiredVars:         req.RequiredVars,
		Tags:                 req.Tags,
	}
	if req.AutoExecute != nil {
		in.AutoExecute = *req.AutoExecute
	}

	tpl, err := s.svc.Template.Create(in)
	if err != nil {
		s.writeTemplateError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, templateJSON(tpl))
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req templateUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	in := service.TemplateUpdateInput{
		Name:                 req.Name,
		Description:          req.Description,
		Kind:                 req.Kind,
		AutoExecute:          req.AutoExecute,
		Executor:             req.Executor,
		AgentProfile:         req.AgentProfile,
		SystemPrompt:         req.SystemPrompt,
		WorkingDir:           req.WorkingDir,
		Tools:                req.Tools,
		Permissions:          req.Permissions,
		Environment:          req.Environment,
		CostBudget:           req.CostBudget,
		MaxRetries:           req.MaxRetries,
		MaxDurationMs:        req.MaxDurationMs,
		TokenBudget:          req.TokenBudget,
		OnDone:               req.OnDone,
		OnFail:               req.OnFail,
		OnReview:             req.OnReview,
		OnDoneMerge:          req.OnDoneMerge,
		EscalationChain:      req.EscalationChain,
		QualityGates:         req.QualityGates,
		Deliverables:         req.Deliverables,
		CheckpointMode:       req.CheckpointMode,
		OnCheckpointResponse: req.OnCheckpointResponse,
		MetadataTemplate:     req.MetadataTemplate,
		RequiredVars:         req.RequiredVars,
		Tags:                 req.Tags,
	}
	tpl, err := s.svc.Template.Update(id, in)
	if err != nil {
		s.writeTemplateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templateJSON(tpl))
}

// getTemplate returns a single template.
//
//   - `GET /templates/{id}`           → latest non-archived version via
//     GetLatestTemplate. Returns 404 if every version is archived.
//   - `GET /templates/{id}?version=N` → exact (id, version) lookup via
//     GetTemplate. No is_archived filter on this path, so archived rows
//     are reachable here for historical `metadata.template_ref` lookups
//     (spec §5.2: "historical references survive").
//
// The `include_archived` query flag applies only to list endpoints; it
// is intentionally ignored here. Callers who want to inspect an archived
// row must know its version.
func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	version := 0
	if v := r.URL.Query().Get("version"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			version = n
		}
	}
	tpl, err := s.svc.Template.Get(id, version)
	if err != nil {
		s.writeTemplateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templateJSON(tpl))
}

func (s *Server) archiveTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be an integer")
		return
	}
	if err := s.svc.Template.Archive(id, version); err != nil {
		s.writeTemplateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Template.Delete(id); err != nil {
		s.writeTemplateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	opts := service.TemplateListOpts{
		Kind: r.URL.Query().Get("kind"),
	}
	if r.URL.Query().Get("include_archived") == "true" {
		opts.IncludeArchived = true
	}
	list, err := s.svc.Template.List(opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, len(list))
	for i := range list {
		out[i] = templateJSON(&list[i])
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": out})
}

func (s *Server) instantiateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req templateInstantiateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	task, err := s.svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID:      id,
		TemplateVersion: req.TemplateVersion,
		Title:           req.Title,
		Description:     req.Description,
		Vars:            req.Vars,
		Overrides:       req.Overrides,
		SprintID:        req.SprintID,
		ProjectID:       req.ProjectID,
		EpicID:          req.EpicID,
		Tags:            req.Tags,
	})
	if err != nil {
		s.writeTemplateError(w, err)
		return
	}
	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Template-generated task is freshly created — no collection yet.
	writeJSON(w, http.StatusCreated, taskJSON(task, tags, nil, subs, ""))
}

// writeTemplateError maps template-related service errors to HTTP codes.
// ConflictError → 409 (referenced template delete), ValidationError →
// 422, ErrTemplateNotFound → 404, everything else → 500.
func (s *Server) writeTemplateError(w http.ResponseWriter, err error) {
	var cerr *service.ConflictError
	if errors.As(err, &cerr) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	var verr *service.ValidationError
	if errors.As(err, &verr) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if errors.Is(err, sqlstore.ErrTemplateNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
