package service

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// TemplateService owns CRUD + versioning + instantiation for task
// templates. It delegates to the store for persistence and to
// TaskService.Create for the resulting task (so validation + tag wiring
// stays in one place).
type TemplateService struct {
	store *sqlstore.Store
	tasks *TaskService
}

// TemplateCreateInput captures every templatable field. JSON-blob fields
// are Go-native (slice/map) — the service marshals to TEXT before insert.
type TemplateCreateInput struct {
	ID                   string
	Name                 string
	Description          string
	Kind                 string
	AutoExecute          bool
	Executor             string
	AgentProfile         string
	SystemPrompt         string
	WorkingDir           string // supports {{var}} resolution at Instantiate
	Tools                []string
	Permissions          map[string]any
	Environment          map[string]string
	CostBudget           *float64
	MaxRetries           *int
	MaxDurationMs        *int64
	TokenBudget          *int64
	OnDone               string
	OnFail               string
	OnReview             string
	OnDoneMerge          string
	EscalationChain      []string
	QualityGates         []string
	Deliverables         []Deliverable
	CheckpointMode       string
	OnCheckpointResponse string
	MetadataTemplate     map[string]any
	RequiredVars         []string
	Tags                 []string
}

// TemplateUpdateInput collects only the fields allowed to change on
// Update. A new row is inserted at version = max+1; the old row stays
// for historical references.
type TemplateUpdateInput struct {
	Name                 string
	Description          string
	Kind                 string
	AutoExecute          *bool
	Executor             *string
	AgentProfile         *string
	SystemPrompt         *string
	WorkingDir           *string // pointer so empty-vs-unset is distinguishable
	Tools                []string
	Permissions          map[string]any
	Environment          map[string]string
	CostBudget           *float64
	MaxRetries           *int
	MaxDurationMs        *int64
	TokenBudget          *int64
	OnDone               string
	OnFail               string
	OnReview             string
	OnDoneMerge          string
	EscalationChain      []string
	QualityGates         []string
	Deliverables         []Deliverable
	CheckpointMode       string
	OnCheckpointResponse string
	MetadataTemplate     map[string]any
	RequiredVars         []string
	Tags                 []string
}

// TemplateInstantiateInput instantiates (id, version) into a real task.
// Title is always caller-owned; Description falls back to the template.
// Vars resolves placeholders in string fields + metadata_template.
// Overrides is a flat map of task-field → value applied last.
type TemplateInstantiateInput struct {
	TemplateID      string
	TemplateVersion int // 0 = latest non-archived
	Title           string
	Description     string
	Vars            map[string]string
	Overrides       map[string]any
	SprintID        string
	ProjectID       string
	EpicID          string
	Tags            []string
}

// TemplateListOpts filters List. IncludeArchived surfaces retired rows.
type TemplateListOpts struct {
	IncludeArchived bool
	Kind            string
}

// Create inserts a new template at version 1. Duplicate IDs get caught by
// the PRIMARY KEY constraint on (id, version); callers should Update to
// add a new version of an existing template.
func (s *TemplateService) Create(in TemplateCreateInput) (*sqlstore.TemplateRecord, error) {
	if in.ID == "" {
		return nil, &ValidationError{Field: "id", Message: "template id required"}
	}
	if in.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "template name required"}
	}
	if in.Kind != "" && !validKinds[in.Kind] {
		return nil, &ValidationError{Field: "kind", Message: "invalid template kind"}
	}
	next, err := s.store.NextTemplateVersion(in.ID)
	if err != nil {
		return nil, err
	}
	rec := buildTemplateRecord(in, in.ID, next)
	if err := s.store.CreateTemplate(rec); err != nil {
		return nil, err
	}
	return s.store.GetTemplate(in.ID, next)
}

// Update appends a new version row carrying the caller's changes overlaid
// on the latest version. Fields left zero on TemplateUpdateInput inherit
// from the previous version.
func (s *TemplateService) Update(id string, in TemplateUpdateInput) (*sqlstore.TemplateRecord, error) {
	prev, err := s.store.GetLatestTemplate(id)
	if err != nil {
		if errors.Is(err, sqlstore.ErrTemplateNotFound) {
			return nil, &ValidationError{Field: "id", Message: "template not found: " + id}
		}
		return nil, err
	}
	merged := mergeTemplateUpdate(prev, in)
	merged.Version, err = s.store.NextTemplateVersion(id)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateTemplate(merged); err != nil {
		return nil, err
	}
	return s.store.GetTemplate(id, merged.Version)
}

// Get returns (id, version). Version=0 returns the latest non-archived.
func (s *TemplateService) Get(id string, version int) (*sqlstore.TemplateRecord, error) {
	if version == 0 {
		return s.store.GetLatestTemplate(id)
	}
	return s.store.GetTemplate(id, version)
}

// Archive flips is_archived on a single (id, version).
func (s *TemplateService) Archive(id string, version int) error {
	return s.store.ArchiveTemplate(id, version)
}

// Delete hard-deletes every version of a template. Refuses with a
// ConflictError when tasks still reference any version, steering the
// caller toward Archive (the recommended lifecycle end-state per spec
// §5.2).
func (s *TemplateService) Delete(id string) error {
	err := s.store.DeleteTemplate(id)
	var refErr *sqlstore.TemplateReferencedError
	if errors.As(err, &refErr) {
		return &ConflictError{
			Message: fmt.Sprintf(
				"template %s has %d referencing task(s); archive instead of deleting",
				refErr.ID, refErr.Count,
			),
		}
	}
	return err
}

// List returns matching templates.
func (s *TemplateService) List(opts TemplateListOpts) ([]sqlstore.TemplateRecord, error) {
	return s.store.ListTemplates(opts.IncludeArchived, opts.Kind)
}

// Instantiate builds a TaskCreateInput from a template + caller vars and
// delegates to TaskService.Create. The resulting task carries
// metadata.template_ref = {id, version} so callers can trace lineage
// even after the template is archived or deleted-in-error.
func (s *TemplateService) Instantiate(in TemplateInstantiateInput) (*sqlstore.TaskRecord, error) {
	if in.TemplateID == "" {
		return nil, &ValidationError{Field: "template_id", Message: "template_id required"}
	}
	if in.Title == "" {
		return nil, &ValidationError{Field: "title", Message: "title required"}
	}

	tpl, err := s.loadTemplateForInstantiation(in.TemplateID, in.TemplateVersion)
	if err != nil {
		return nil, err
	}
	if tpl.IsArchived && in.TemplateVersion == 0 {
		return nil, &ValidationError{
			Field:   "template_id",
			Message: "template archived; specify an explicit version for historical instantiation",
		}
	}

	if err := validateRequiredVars(tpl, in.Vars); err != nil {
		return nil, err
	}

	input, err := s.applyTemplateToInput(tpl, in)
	if err != nil {
		return nil, err
	}
	applyOverrides(&input, in.Overrides)

	input.Title = in.Title
	if in.Description != "" {
		input.Description = in.Description
	}
	if in.SprintID != "" {
		input.SprintID = in.SprintID
	}
	if in.ProjectID != "" {
		input.ProjectID = in.ProjectID
	}
	if in.EpicID != "" {
		input.EpicID = in.EpicID
	}
	if len(in.Tags) > 0 {
		input.Tags = append(input.Tags, in.Tags...)
	}

	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	input.Metadata["template_ref"] = map[string]any{
		"id":      tpl.ID,
		"version": tpl.Version,
	}

	return s.tasks.Create(input)
}

// loadTemplateForInstantiation resolves (id, version-or-zero) into a row.
// version=0 picks the latest non-archived.
func (s *TemplateService) loadTemplateForInstantiation(id string, version int) (*sqlstore.TemplateRecord, error) {
	var tpl *sqlstore.TemplateRecord
	var err error
	if version == 0 {
		tpl, err = s.store.GetLatestTemplate(id)
	} else {
		tpl, err = s.store.GetTemplate(id, version)
	}
	if errors.Is(err, sqlstore.ErrTemplateNotFound) {
		return nil, &ValidationError{Field: "template_id", Message: "template not found: " + id}
	}
	return tpl, err
}

// validateRequiredVars surfaces any missing required variable as a single
// 422 so the caller can correct the whole set in one round-trip.
func validateRequiredVars(tpl *sqlstore.TemplateRecord, vars map[string]string) error {
	if !tpl.RequiredVars.Valid || tpl.RequiredVars.String == "" {
		return nil
	}
	var required []string
	if err := unmarshalJSON([]byte(tpl.RequiredVars.String), &required); err != nil {
		return fmt.Errorf("template required_vars unmarshal: %w", err)
	}
	for _, k := range required {
		if _, ok := vars[k]; !ok {
			return &ValidationError{
				Field:   "vars." + k,
				Message: "required variable " + k + " not supplied",
			}
		}
	}
	return nil
}

// applyTemplateToInput copies the template's task-shaping fields into a
// fresh TaskCreateInput, resolving {{var}} placeholders in the fields
// that support them (description, system_prompt, environment values,
// string leaves of metadata_template).
func (s *TemplateService) applyTemplateToInput(tpl *sqlstore.TemplateRecord, in TemplateInstantiateInput) (TaskCreateInput, error) {
	vars := in.Vars
	if vars == nil {
		vars = map[string]string{}
	}

	input := TaskCreateInput{
		Description:          tpl.Description,
		Manual:               !tpl.AutoExecute,
		Kind:                 tpl.Kind,
		CheckpointMode:       tpl.CheckpointMode,
		OnCheckpointResponse: tpl.OnCheckpointResponse,
		OnDone:               tpl.OnDone,
		OnFail:               tpl.OnFail,
		OnReview:             tpl.OnReview,
		OnDoneMerge:          tpl.OnDoneMerge,
	}

	// Resolve templatable strings.
	resolvedDesc, err := ResolveVars(tpl.Description, vars)
	if err != nil {
		return input, &ValidationError{Field: "description", Message: err.Error()}
	}
	input.Description = resolvedDesc

	if tpl.Executor.Valid {
		input.Executor = tpl.Executor.String
	}
	if tpl.AgentProfile.Valid {
		input.AgentProfile = tpl.AgentProfile.String
	}
	if tpl.SystemPrompt.Valid {
		resolved, err := ResolveVars(tpl.SystemPrompt.String, vars)
		if err != nil {
			return input, &ValidationError{Field: "system_prompt", Message: err.Error()}
		}
		input.SystemPrompt = resolved
	}
	if tpl.WorkingDir.Valid {
		resolved, err := ResolveVars(tpl.WorkingDir.String, vars)
		if err != nil {
			return input, &ValidationError{Field: "working_dir", Message: err.Error()}
		}
		input.WorkingDir = resolved
	}

	// Tools / escalation / quality gates — unmarshal JSON arrays; leave
	// as-is (no var substitution in these for MVP).
	input.Tools = unmarshalStringArray(tpl.Tools)
	input.EscalationChain = unmarshalStringArray(tpl.EscalationChain)
	input.QualityGates = unmarshalStringArray(tpl.QualityGates)

	// Permissions — generic map.
	if tpl.Permissions.Valid && tpl.Permissions.String != "" {
		var perms map[string]any
		if err := unmarshalJSON([]byte(tpl.Permissions.String), &perms); err == nil {
			input.Permissions = perms
		}
	}

	// Environment — values get var resolution.
	if tpl.Environment.Valid && tpl.Environment.String != "" {
		var env map[string]string
		if err := unmarshalJSON([]byte(tpl.Environment.String), &env); err == nil {
			resolved := make(map[string]string, len(env))
			for k, v := range env {
				rv, err := ResolveVars(v, vars)
				if err != nil {
					return input, &ValidationError{Field: "environment." + k, Message: err.Error()}
				}
				resolved[k] = rv
			}
			input.Environment = resolved
		}
	}

	// Deliverables — opaque typed list.
	if tpl.Deliverables.Valid && tpl.Deliverables.String != "" {
		var dels []Deliverable
		if err := unmarshalJSON([]byte(tpl.Deliverables.String), &dels); err == nil {
			input.Deliverables = dels
		}
	}

	// Budgets.
	if tpl.CostBudget.Valid {
		v := tpl.CostBudget.Float64
		input.CostBudget = &v
	}
	if tpl.MaxDurationMs.Valid {
		v := tpl.MaxDurationMs.Int64
		input.MaxDurationMs = &v
	}
	if tpl.TokenBudget.Valid {
		v := tpl.TokenBudget.Int64
		input.TokenBudget = &v
	}
	if tpl.MaxRetries > 0 {
		v := tpl.MaxRetries
		input.MaxRetries = &v
	}

	// metadata_template — deep-resolve string leaves into Metadata.
	if tpl.MetadataTemplate.Valid && tpl.MetadataTemplate.String != "" {
		var md map[string]any
		if err := unmarshalJSON([]byte(tpl.MetadataTemplate.String), &md); err == nil {
			resolved, err := ResolveVarsInMap(md, vars)
			if err != nil {
				return input, &ValidationError{Field: "metadata_template", Message: err.Error()}
			}
			input.Metadata = resolved
		}
	}

	// Tags — template-supplied tags land first; caller tags append later.
	if tpl.Tags.Valid && tpl.Tags.String != "" {
		input.Tags = unmarshalStringArray(tpl.Tags)
	}

	return input, nil
}

// applyOverrides is a conservative last-wins merge that only understands
// the subset of fields templates commonly override. Richer override
// shapes are deferred.
func applyOverrides(input *TaskCreateInput, overrides map[string]any) {
	if len(overrides) == 0 {
		return
	}
	if v, ok := overrides["description"].(string); ok && v != "" {
		input.Description = v
	}
	if v, ok := overrides["executor"].(string); ok && v != "" {
		input.Executor = v
	}
	if v, ok := overrides["agent_profile"].(string); ok && v != "" {
		input.AgentProfile = v
	}
	if v, ok := overrides["system_prompt"].(string); ok && v != "" {
		input.SystemPrompt = v
	}
	if v, ok := overrides["manual"].(bool); ok {
		input.Manual = v
	}
	if v, ok := overrides["priority"].(float64); ok {
		input.Priority = int(v)
	}
	if v, ok := overrides["priority"].(int); ok {
		input.Priority = v
	}
}

// buildTemplateRecord turns a TemplateCreateInput into a storable row.
// Go-native JSON fields are marshaled here so the store stays JSON-agnostic.
func buildTemplateRecord(in TemplateCreateInput, id string, version int) *sqlstore.TemplateRecord {
	rec := &sqlstore.TemplateRecord{
		ID:                   id,
		Version:              version,
		Name:                 in.Name,
		Description:          in.Description,
		Kind:                 in.Kind,
		AutoExecute:          in.AutoExecute,
		OnDone:               in.OnDone,
		OnFail:               in.OnFail,
		OnReview:             in.OnReview,
		OnDoneMerge:          in.OnDoneMerge,
		CheckpointMode:       in.CheckpointMode,
		OnCheckpointResponse: in.OnCheckpointResponse,
	}
	if in.Executor != "" {
		rec.Executor = sql.NullString{String: in.Executor, Valid: true}
	}
	if in.AgentProfile != "" {
		rec.AgentProfile = sql.NullString{String: in.AgentProfile, Valid: true}
	}
	if in.SystemPrompt != "" {
		rec.SystemPrompt = sql.NullString{String: in.SystemPrompt, Valid: true}
	}
	if in.WorkingDir != "" {
		rec.WorkingDir = sql.NullString{String: in.WorkingDir, Valid: true}
	}
	if len(in.Tools) > 0 {
		rec.Tools = sql.NullString{String: marshalJSON(in.Tools), Valid: true}
	}
	if len(in.Permissions) > 0 {
		rec.Permissions = sql.NullString{String: marshalJSON(in.Permissions), Valid: true}
	}
	if len(in.Environment) > 0 {
		rec.Environment = sql.NullString{String: marshalJSON(in.Environment), Valid: true}
	}
	if in.CostBudget != nil {
		rec.CostBudget = sql.NullFloat64{Float64: *in.CostBudget, Valid: true}
	}
	if in.MaxRetries != nil {
		rec.MaxRetries = *in.MaxRetries
	}
	if in.MaxDurationMs != nil {
		rec.MaxDurationMs = sql.NullInt64{Int64: *in.MaxDurationMs, Valid: true}
	}
	if in.TokenBudget != nil {
		rec.TokenBudget = sql.NullInt64{Int64: *in.TokenBudget, Valid: true}
	}
	if len(in.EscalationChain) > 0 {
		rec.EscalationChain = sql.NullString{String: marshalJSON(in.EscalationChain), Valid: true}
	}
	if len(in.QualityGates) > 0 {
		rec.QualityGates = sql.NullString{String: marshalJSON(in.QualityGates), Valid: true}
	}
	if len(in.Deliverables) > 0 {
		rec.Deliverables = sql.NullString{String: marshalJSON(in.Deliverables), Valid: true}
	}
	if len(in.MetadataTemplate) > 0 {
		rec.MetadataTemplate = sql.NullString{String: marshalJSON(in.MetadataTemplate), Valid: true}
	}
	if len(in.RequiredVars) > 0 {
		rec.RequiredVars = sql.NullString{String: marshalJSON(in.RequiredVars), Valid: true}
	}
	if len(in.Tags) > 0 {
		rec.Tags = sql.NullString{String: marshalJSON(in.Tags), Valid: true}
	}
	return rec
}

// mergeTemplateUpdate returns a new record where every zero-valued field in
// TemplateUpdateInput inherits from prev. Supplied fields replace.
func mergeTemplateUpdate(prev *sqlstore.TemplateRecord, in TemplateUpdateInput) *sqlstore.TemplateRecord {
	next := *prev
	if in.Name != "" {
		next.Name = in.Name
	}
	if in.Description != "" {
		next.Description = in.Description
	}
	if in.Kind != "" {
		next.Kind = in.Kind
	}
	if in.AutoExecute != nil {
		next.AutoExecute = *in.AutoExecute
	}
	if in.Executor != nil {
		next.Executor = sql.NullString{String: *in.Executor, Valid: *in.Executor != ""}
	}
	if in.AgentProfile != nil {
		next.AgentProfile = sql.NullString{String: *in.AgentProfile, Valid: *in.AgentProfile != ""}
	}
	if in.SystemPrompt != nil {
		next.SystemPrompt = sql.NullString{String: *in.SystemPrompt, Valid: *in.SystemPrompt != ""}
	}
	if in.WorkingDir != nil {
		next.WorkingDir = sql.NullString{String: *in.WorkingDir, Valid: *in.WorkingDir != ""}
	}
	if in.Tools != nil {
		next.Tools = sql.NullString{String: marshalJSON(in.Tools), Valid: true}
	}
	if in.Permissions != nil {
		next.Permissions = sql.NullString{String: marshalJSON(in.Permissions), Valid: true}
	}
	if in.Environment != nil {
		next.Environment = sql.NullString{String: marshalJSON(in.Environment), Valid: true}
	}
	if in.CostBudget != nil {
		next.CostBudget = sql.NullFloat64{Float64: *in.CostBudget, Valid: true}
	}
	if in.MaxRetries != nil {
		next.MaxRetries = *in.MaxRetries
	}
	if in.MaxDurationMs != nil {
		next.MaxDurationMs = sql.NullInt64{Int64: *in.MaxDurationMs, Valid: true}
	}
	if in.TokenBudget != nil {
		next.TokenBudget = sql.NullInt64{Int64: *in.TokenBudget, Valid: true}
	}
	if in.OnDone != "" {
		next.OnDone = in.OnDone
	}
	if in.OnFail != "" {
		next.OnFail = in.OnFail
	}
	if in.OnReview != "" {
		next.OnReview = in.OnReview
	}
	if in.OnDoneMerge != "" {
		next.OnDoneMerge = in.OnDoneMerge
	}
	if in.EscalationChain != nil {
		next.EscalationChain = sql.NullString{String: marshalJSON(in.EscalationChain), Valid: true}
	}
	if in.QualityGates != nil {
		next.QualityGates = sql.NullString{String: marshalJSON(in.QualityGates), Valid: true}
	}
	if in.Deliverables != nil {
		next.Deliverables = sql.NullString{String: marshalJSON(in.Deliverables), Valid: true}
	}
	if in.CheckpointMode != "" {
		next.CheckpointMode = in.CheckpointMode
	}
	if in.OnCheckpointResponse != "" {
		next.OnCheckpointResponse = in.OnCheckpointResponse
	}
	if in.MetadataTemplate != nil {
		next.MetadataTemplate = sql.NullString{String: marshalJSON(in.MetadataTemplate), Valid: true}
	}
	if in.RequiredVars != nil {
		next.RequiredVars = sql.NullString{String: marshalJSON(in.RequiredVars), Valid: true}
	}
	if in.Tags != nil {
		next.Tags = sql.NullString{String: marshalJSON(in.Tags), Valid: true}
	}
	return &next
}

func unmarshalStringArray(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var out []string
	if err := unmarshalJSON([]byte(ns.String), &out); err != nil {
		return nil
	}
	return out
}
