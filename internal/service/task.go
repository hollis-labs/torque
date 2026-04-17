package service

import (
	"database/sql"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// validTransitions defines the allowed FSM transitions for task status.
var validTransitions = map[string][]string{
	"todo":    {"doing", "blocked", "paused", "archived"},
	"doing":   {"review", "done", "blocked", "paused", "todo", "archived"},
	"review":  {"done", "doing", "todo", "blocked", "paused", "archived"},
	"blocked": {"todo", "archived"},
	"paused":  {"todo", "archived"},
	"done":    {"archived"},
}

// TaskCreateInput holds user-facing fields for creating a task.
type TaskCreateInput struct {
	Title             string
	Description       string
	Priority          int
	Tags              []string
	Manual            bool
	Executor          string
	AgentProfile      string
	WorkingDir        string
	Tools             []string
	SystemPrompt      string
	AgentFile         string
	Files             []string
	CostBudget        *float64
	MaxRetries        *int
	OnDone            string
	OnFail            string
	OnReview          string
	OnDoneMerge       string
	DeliverablePreset string
	DependsOn         []string
	SprintID          string
	ProjectID         string
	EpicID            string

	// Project 2 canonical task fields. These are persisted by Create's
	// record build below and validated by validateTaskWrites.
	Permissions     map[string]any
	Environment     map[string]string
	MaxDurationMs   *int64
	TokenBudget     *int64
	EscalationChain []string
	QualityGates    []string
	Deliverables    []Deliverable
	BlockedReason   string
	Metadata        map[string]any

	// Facet fields (migration 007)
	Kind                 string
	SourceType           string
	SourceRef            string
	Trust                string // if empty, resolved via ResolveTrust
	CheckpointMode       string
	OnCheckpointResponse string

	// Parent linkage (migration 013). Empty = no parent (root). Validated
	// against ancestor chain to prevent cycles.
	ParentID string

	// Subtodos (migration 011). If nil, Create auto-extracts checklist
	// items from Description. Non-nil (including empty) disables auto-
	// extraction and stores the caller-provided list as-is.
	Subtodos []sqlstore.Subtodo
}

// TaskService provides business logic for tasks.
type TaskService struct {
	store   *sqlstore.Store
	feature *FeatureService
	tags    *TagService
}

// Create validates and creates a new task.
func (s *TaskService) Create(input TaskCreateInput) (*sqlstore.TaskRecord, error) {
	if input.Title == "" {
		return nil, &ValidationError{Field: "title", Message: "title is required"}
	}

	priority := input.Priority
	if priority == 0 {
		priority = 2
	}

	// Validate write-time invariants (enums, numeric bounds, deliverables, depends_on).
	fields, err := extractCreateFields(input)
	if err != nil {
		return nil, err
	}
	if err := s.validateTaskWrites(fields); err != nil {
		return nil, err
	}

	// Agent file existence check (CW-20260417-0082). Contents are not read
	// here — only the path is validated so stale/deleted files surface at
	// dispatch as a blocked run, not as a task-creation error.
	if err := agentfile.Validate(input.AgentFile, input.WorkingDir); err != nil {
		return nil, &ValidationError{Field: "agent_file", Message: err.Error()}
	}

	// Facet validation — uses effective values (after defaulting) to match
	// what will be stored. Executor defaults to "cli" only for kind=agent;
	// other kinds leave it as the caller set it so validateTaskKind can
	// enforce kind-specific rules (e.g. external forbids executor).
	effectiveKind := orDefault(input.Kind, "agent")
	effectiveExecutor := input.Executor
	if effectiveKind == "agent" && effectiveExecutor == "" {
		effectiveExecutor = "cli"
	}
	effectiveSourceType := orDefault(input.SourceType, "user")
	effectiveCheckpointMode := orDefault(input.CheckpointMode, "none")
	effectiveOnCheckpointResponse := orDefault(input.OnCheckpointResponse, "resume")
	effectiveTrust := orDefault(input.Trust, ResolveTrust(effectiveSourceType, input.SourceRef))

	// Parent tasks fan out work to child tasks, so their own "execution" is
	// the coordination wait — typically tens of minutes. The default profile
	// timeout (20m) is too short for large bundles, so inject a 1800s
	// override when the caller didn't set one. See CW-20260417-0032.
	if effectiveKind == "parent" {
		if input.Metadata == nil {
			input.Metadata = map[string]any{}
		}
		if _, set := input.Metadata["timeout_seconds_override"]; !set {
			input.Metadata["timeout_seconds_override"] = 1800
		}
	}

	if err := validateTaskKind(
		effectiveKind,
		effectiveExecutor,
		effectiveSourceType,
		effectiveTrust,
		effectiveCheckpointMode,
		effectiveOnCheckpointResponse,
		input.Manual,
		input.Metadata,
	); err != nil {
		return nil, err
	}

	// Validate sprint association
	if input.SprintID != "" {
		if s.feature != nil && s.feature.IsEnabled("sprints") {
			sprint, err := s.store.GetSprint(input.SprintID)
			if err != nil {
				return nil, &ValidationError{Field: "sprint_id", Message: "sprint not found: " + input.SprintID}
			}
			if sprint.Status == "inactive" {
				return nil, &ValidationError{Field: "sprint_id", Message: "cannot add tasks to an inactive sprint"}
			}
		} else {
			// Feature not enabled — silently clear the association
			input.SprintID = ""
		}
	}

	// Validate project association
	if input.ProjectID != "" {
		if s.feature != nil && s.feature.IsEnabled("projects") {
			if _, err := s.store.GetProject(input.ProjectID); err != nil {
				return nil, &ValidationError{Field: "project_id", Message: "project not found: " + input.ProjectID}
			}
		} else {
			input.ProjectID = ""
		}
	}

	// Validate epic association
	if input.EpicID != "" {
		if s.feature != nil && s.feature.IsEnabled("epics") {
			if _, err := s.store.GetEpic(input.EpicID); err != nil {
				return nil, &ValidationError{Field: "epic_id", Message: "epic not found: " + input.EpicID}
			}
		} else {
			input.EpicID = ""
		}
	}

	// Parent linkage (migration 013). Cycle check uses the existing task graph;
	// since the candidate task has no ID yet, self-reference only applies at
	// Update time. Here we just require the parent to exist.
	if input.ParentID != "" {
		if _, err := s.store.GetTask(input.ParentID); err != nil {
			return nil, &ValidationError{Field: "parent_id", Message: "parent task not found: " + input.ParentID}
		}
	}

	id, err := s.store.NextTaskID()
	if err != nil {
		return nil, err
	}

	rec := &sqlstore.TaskRecord{
		ID:                   id,
		Title:                input.Title,
		Description:          input.Description,
		Priority:             priority,
		Manual:               input.Manual,
		Executor:             effectiveExecutor,
		AgentProfile:         input.AgentProfile,
		WorkingDir:           input.WorkingDir,
		SystemPrompt:         input.SystemPrompt,
		AgentFile:            input.AgentFile,
		MaxRetries:           orDefaultInt(input.MaxRetries, 3),
		OnDone:               orDefault(input.OnDone, "review"),
		OnFail:               orDefault(input.OnFail, "retry"),
		OnReview:             orDefault(input.OnReview, "pause"),
		OnDoneMerge:          orDefault(input.OnDoneMerge, "none"),
		DeliverablePreset:    input.DeliverablePreset,
		BlockedReason:        input.BlockedReason,
		Kind:                 effectiveKind,
		SourceType:           effectiveSourceType,
		Trust:                effectiveTrust,
		CheckpointMode:       effectiveCheckpointMode,
		OnCheckpointResponse: effectiveOnCheckpointResponse,
	}
	if input.SourceRef != "" {
		rec.SourceRef = sql.NullString{String: input.SourceRef, Valid: true}
	}

	if len(input.Tools) > 0 {
		rec.Tools = sql.NullString{String: marshalJSON(input.Tools), Valid: true}
	}
	if len(input.Files) > 0 {
		rec.Files = sql.NullString{String: marshalJSON(input.Files), Valid: true}
	}
	if len(input.DependsOn) > 0 {
		rec.DependsOn = sql.NullString{String: marshalJSON(input.DependsOn), Valid: true}
	}
	if input.CostBudget != nil {
		rec.CostBudget = sql.NullFloat64{Float64: *input.CostBudget, Valid: true}
	}
	if len(input.Permissions) > 0 {
		rec.Permissions = sql.NullString{String: marshalJSON(input.Permissions), Valid: true}
	}
	if len(input.Environment) > 0 {
		rec.Environment = sql.NullString{String: marshalJSON(input.Environment), Valid: true}
	}
	if input.MaxDurationMs != nil {
		rec.MaxDurationMs = sql.NullInt64{Int64: *input.MaxDurationMs, Valid: true}
	}
	if input.TokenBudget != nil {
		rec.TokenBudget = sql.NullInt64{Int64: *input.TokenBudget, Valid: true}
	}
	if len(input.EscalationChain) > 0 {
		rec.EscalationChain = sql.NullString{String: marshalJSON(input.EscalationChain), Valid: true}
	}
	if len(input.QualityGates) > 0 {
		rec.QualityGates = sql.NullString{String: marshalJSON(input.QualityGates), Valid: true}
	}
	if len(input.Deliverables) > 0 {
		rec.Deliverables = sql.NullString{String: marshalJSON(input.Deliverables), Valid: true}
	}
	if len(input.Metadata) > 0 {
		rec.Metadata = sql.NullString{String: marshalJSON(input.Metadata), Valid: true}
	}
	if input.SprintID != "" {
		rec.SprintID = sql.NullString{String: input.SprintID, Valid: true}
	}
	if input.ProjectID != "" {
		rec.ProjectID = sql.NullString{String: input.ProjectID, Valid: true}
	}
	if input.EpicID != "" {
		rec.EpicID = sql.NullString{String: input.EpicID, Valid: true}
	}
	if input.ParentID != "" {
		rec.ParentID = sql.NullString{String: input.ParentID, Valid: true}
	}

	if err := s.store.CreateTask(rec); err != nil {
		return nil, err
	}

	// Subtodos: caller-provided list wins; otherwise auto-extract top-level
	// "- [ ]" checkboxes from the description so agents get structural
	// acceptance gating for free. Write only when non-empty to avoid a
	// redundant UPDATE immediately after INSERT.
	subtodos := input.Subtodos
	if subtodos == nil {
		subtodos = ExtractSubtodosFromDescription(input.Description)
	}
	if len(subtodos) > 0 {
		if err := s.store.SetSubtodos(id, subtodos); err != nil {
			return nil, err
		}
	}

	// Resolve tag names/slugs and link them to the task
	if len(input.Tags) > 0 {
		slugs, err := s.tags.ResolveNames(input.Tags)
		if err != nil {
			return nil, err
		}
		if err := s.store.SetTaskTags(id, slugs); err != nil {
			return nil, err
		}
	}

	return s.store.GetTask(id)
}

// Get fetches a task by ID.
func (s *TaskService) Get(id string) (*sqlstore.TaskRecord, error) {
	return s.store.GetTask(id)
}

// List returns tasks matching the filter.
func (s *TaskService) List(filter sqlstore.TaskFilter) ([]sqlstore.TaskRecord, error) {
	return s.store.ListTasks(filter)
}

// TaskUpdateInput wraps the store-level TaskUpdate and adds a tags field.
// The store-level TaskUpdate no longer carries tags because they live in
// the task_tags link table, not a column on tasks.
type TaskUpdateInput struct {
	sqlstore.TaskUpdate
	Tags *[]string // nil = no change; non-nil = replace all linked tags
}

// Update applies a partial update to a task. If Tags is non-nil, linked
// tags are resolved and replaced.
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
	fields, err := extractUpdateFields(input)
	if err != nil {
		return err
	}
	if err := s.validateTaskWrites(fields); err != nil {
		return err
	}

	// Facet validation against effective values = existing overlaid with update.
	existing, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	effectiveKind := ptrOrDefault(input.Kind, existing.Kind)
	effectiveExecutor := ptrOrDefault(input.Executor, existing.Executor)
	effectiveSourceType := ptrOrDefault(input.SourceType, existing.SourceType)
	effectiveTrust := ptrOrDefault(input.Trust, existing.Trust)
	effectiveCheckpointMode := ptrOrDefault(input.CheckpointMode, existing.CheckpointMode)
	effectiveOnCheckpointResponse := ptrOrDefault(input.OnCheckpointResponse, existing.OnCheckpointResponse)
	effectiveManual := existing.Manual
	if input.Manual != nil {
		effectiveManual = *input.Manual
	}
	effectiveMetadata := map[string]any{}
	if existing.Metadata.Valid && existing.Metadata.String != "" {
		_ = unmarshalJSON([]byte(existing.Metadata.String), &effectiveMetadata)
	}
	if input.Metadata != nil && input.Metadata.Valid && input.Metadata.String != "" {
		overlay := map[string]any{}
		if err := unmarshalJSON([]byte(input.Metadata.String), &overlay); err == nil {
			for k, v := range overlay {
				effectiveMetadata[k] = v
			}
		}
	}
	if err := validateTaskKind(
		effectiveKind,
		effectiveExecutor,
		effectiveSourceType,
		effectiveTrust,
		effectiveCheckpointMode,
		effectiveOnCheckpointResponse,
		effectiveManual,
		effectiveMetadata,
	); err != nil {
		return err
	}

	// Agent file existence check (CW-20260417-0082). Resolve against the
	// effective working_dir (existing overlaid with update). An explicit
	// empty string clears the agent_file and skips the file check; any
	// non-empty value must resolve and stat successfully.
	effectiveAgentFile := ptrOrDefault(input.AgentFile, existing.AgentFile)
	effectiveWorkingDir := ptrOrDefault(input.WorkingDir, existing.WorkingDir)
	if err := agentfile.Validate(effectiveAgentFile, effectiveWorkingDir); err != nil {
		return &ValidationError{Field: "agent_file", Message: err.Error()}
	}

	// Parent linkage cycle check (migration 013). Runs before the store write
	// so cycle-producing updates are rejected with a ValidationError rather
	// than committed. Only evaluated when the caller is touching parent_id.
	if input.ParentID != nil {
		if err := s.validateParentID(id, *input.ParentID); err != nil {
			return err
		}
	}

	if err := s.store.UpdateTask(id, input.TaskUpdate); err != nil {
		return err
	}
	if input.Tags != nil {
		slugs, err := s.tags.ResolveNames(*input.Tags)
		if err != nil {
			return err
		}
		return s.store.SetTaskTags(id, slugs)
	}
	return nil
}

// Delete removes a task by ID.
func (s *TaskService) Delete(id string) error {
	return s.store.DeleteTask(id)
}

// validateParentID enforces parent_id invariants for updates (migration 013).
// A NullString with Valid=false clears the parent — always allowed. Otherwise:
//
//   - parent must not equal the task itself (self-reference).
//   - parent must exist.
//   - no cycle: walking ancestors from the candidate parent must never reach
//     the task being updated.
//
// Cycle detection caps at 256 ancestors to guard against malformed graphs.
func (s *TaskService) validateParentID(taskID string, candidate sql.NullString) error {
	if !candidate.Valid || candidate.String == "" {
		return nil
	}
	parentID := candidate.String
	if parentID == taskID {
		return &ValidationError{Field: "parent_id", Message: "parent_id cannot reference the task itself"}
	}
	parent, err := s.store.GetTask(parentID)
	if err != nil {
		return &ValidationError{Field: "parent_id", Message: "parent task not found: " + parentID}
	}
	// Walk ancestors of the proposed parent; if we encounter taskID, a cycle
	// would be created by this update.
	cursor := parent
	for hops := 0; hops < 256; hops++ {
		if !cursor.ParentID.Valid || cursor.ParentID.String == "" {
			return nil
		}
		if cursor.ParentID.String == taskID {
			return &ValidationError{Field: "parent_id", Message: "parent_id would create a cycle through task " + taskID}
		}
		next, err := s.store.GetTask(cursor.ParentID.String)
		if err != nil {
			// Broken ancestor chain — treat as no cycle to avoid false positives.
			return nil
		}
		cursor = next
	}
	return &ValidationError{Field: "parent_id", Message: "parent_id ancestor chain exceeds 256 hops"}
}

// ListTags returns the tags linked to a task.
func (s *TaskService) ListTags(taskID string) ([]sqlstore.TagRecord, error) {
	return s.store.ListTaskTags(taskID)
}

// Transition moves a task to a new status if the FSM allows it.
func (s *TaskService) Transition(id, newStatus string) error {
	task, err := s.store.GetTask(id)
	if err != nil {
		return err
	}

	allowed, ok := validTransitions[task.Status]
	if !ok {
		return &TransitionError{From: task.Status, To: newStatus, Message: "unknown source status"}
	}
	for _, a := range allowed {
		if a == newStatus {
			return s.store.TransitionTask(id, newStatus)
		}
	}
	return &TransitionError{
		From:    task.Status,
		To:      newStatus,
		Message: "transition not permitted",
	}
}

// Search performs a text search over tasks.
func (s *TaskService) Search(query string) ([]sqlstore.TaskRecord, error) {
	return s.store.SearchTasks(query)
}

// ListSubtodos returns the subtodo checklist for a task.
func (s *TaskService) ListSubtodos(taskID string) ([]sqlstore.Subtodo, error) {
	return s.store.GetSubtodos(taskID)
}

// AddSubtodo appends a checklist item. Caller supplies the id to keep it
// deterministic across repeated emits (e.g. "item-3"); duplicate ids are
// rejected so downstream mark-done calls stay unambiguous.
func (s *TaskService) AddSubtodo(taskID string, item sqlstore.Subtodo) ([]sqlstore.Subtodo, error) {
	if item.ID == "" {
		return nil, &ValidationError{Field: "id", Message: "id is required"}
	}
	if item.Text == "" {
		return nil, &ValidationError{Field: "text", Message: "text is required"}
	}
	existing, err := s.store.GetSubtodos(taskID)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if e.ID == item.ID {
			return nil, &ValidationError{Field: "id", Message: "duplicate subtodo id: " + item.ID}
		}
	}
	existing = append(existing, item)
	if err := s.store.SetSubtodos(taskID, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// MarkSubtodoDone ticks a single item and records its evidence.
func (s *TaskService) MarkSubtodoDone(taskID, itemID, evidence string) ([]sqlstore.Subtodo, error) {
	if err := s.store.SetSubtodoDone(taskID, itemID, evidence); err != nil {
		return nil, err
	}
	return s.store.GetSubtodos(taskID)
}

// BulkTransition applies the same status transition to multiple tasks.
// It returns the count of successful transitions and a slice of errors for failures.
func (s *TaskService) BulkTransition(ids []string, newStatus string) (int, []error) {
	var errs []error
	success := 0
	for _, id := range ids {
		if err := s.Transition(id, newStatus); err != nil {
			errs = append(errs, err)
		} else {
			success++
		}
	}
	return success, errs
}
