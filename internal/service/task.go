package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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
	Status            string
	Priority          int
	Tags              []string
	Manual            bool
	Executor          string
	LaunchProfile     string
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
	store              *sqlstore.Store
	feature            *FeatureService
	tags               *TagService
	transitionObserver TaskTransitionObserver // optional; nil disables the observer hook
}

// SetTransitionObserver installs a TaskTransitionObserver that runs after
// every successful Transition / ForceTransition. nil clears the observer.
// Not goroutine-safe with concurrent Transition calls; install once at
// bootstrap before serving traffic.
func (s *TaskService) SetTransitionObserver(o TaskTransitionObserver) {
	s.transitionObserver = o
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
	// Must run BEFORE the manual=true force below, because validation's
	// external/decision-forbids-auto-execute rule (see task_validation.go)
	// needs to see the caller-provided Manual value.
	fields, err := extractCreateFields(input)
	if err != nil {
		return nil, err
	}
	if err := s.validateTaskWrites(fields); err != nil {
		return nil, err
	}

	// Parent linkage + working_dir auto-inheritance (CW-20260508-0004).
	// Must run BEFORE agentfile.Validate because that check resolves
	// `input.AgentFile` against `input.WorkingDir`; if the caller passed a
	// relative agent_file expecting working_dir to inherit from the parent,
	// an empty WorkingDir would fail validation here even though the
	// inherit would have populated it.
	//
	// Cycle check uses the existing task graph; since the candidate task
	// has no ID yet, self-reference only applies at Update time. Here we
	// just require the parent to exist.
	if input.ParentID != "" {
		parent, err := s.store.GetTask(input.ParentID)
		if err != nil {
			return nil, &ValidationError{Field: "parent_id", Message: "parent task not found: " + input.ParentID}
		}
		// working_dir inheritance: when the caller leaves WorkingDir empty
		// AND the parent has a non-empty working_dir, inherit it. Surfaced
		// 2026-05-08 in S2.5 smoke (CW-20260508-0004): orchestrator agent
		// created kind=internal planner sub-task via torque_task_create
		// without working_dir; scheduler dispatched via cliexec ->
		// agent.Executor rejected with "working_dir is required" -> 3
		// retries -> blocked. Auto-inheritance from parent makes child
		// sub-task creation work without requiring every caller (LLM-
		// driven or otherwise) to know to pass it.
		if input.WorkingDir == "" && parent.WorkingDir != "" {
			input.WorkingDir = parent.WorkingDir
		}
	}

	// Agent file existence check (CW-20260417-0082). Contents are not read
	// here — only the path is validated so stale/deleted files surface at
	// dispatch as a blocked run, not as a task-creation error.
	if err := agentfile.Validate(input.AgentFile, input.WorkingDir); err != nil {
		return nil, &ValidationError{Field: "agent_file", Message: err.Error()}
	}

	// Facet validation — uses effective values (after defaulting) to match
	// what will be stored. The DB schema's `executor TEXT NOT NULL
	// DEFAULT 'cli'` (migration 024) supplies the default for kind=agent
	// when the caller leaves it empty; the prior service-layer
	// belt-and-suspenders that forced 'opencode' is gone with the
	// dogfood flip to codex (2026-05-10). Non-agent kinds keep their
	// caller-supplied value (empty included) so validateTaskKind can
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
	if err := validateRequiredWorkflowMetadata(input.Metadata); err != nil {
		return nil, err
	}

	// Service-level safety enforcement for CW-20260417-0133. Promoted from
	// audit-only to FORCE after Template.Instantiate was identified as a
	// bypass of the HTTP + MCP input-surface overrides. Runs AFTER all
	// kind/facet validation so external/decision+auto-execute rules still
	// fire on the caller-provided Manual value. Covers every transport
	// that funnels through service.Task.Create (HTTP createTask, MCP
	// handleTaskCreate, Template.Instantiate, and any future caller).
	// Tests / smoke-echo / direct-scheduler paths that legitimately want
	// manual=false after create must flip via store.UpdateTask (see the
	// existing smoke/e2e tests for the pattern).
	if !input.Manual {
		log.Printf("service.Task.Create: forcing manual=true on title=%q (caller passed manual=false) — safety override per CW-20260417-0133", input.Title)
		input.Manual = true
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

	// Parent existence check + working_dir auto-inheritance happens earlier
	// in the flow (before agentfile.Validate); see the parent_id block above.

	id, err := s.store.NextTaskID()
	if err != nil {
		return nil, err
	}

	rec := &sqlstore.TaskRecord{
		ID:                   id,
		Title:                input.Title,
		Description:          input.Description,
		Status:               input.Status,
		Priority:             priority,
		Manual:               input.Manual,
		Executor:             effectiveExecutor,
		LaunchProfile:        input.LaunchProfile,
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

	// depends_on lives in the task_dependencies join table (migration 027 /
	// FK-003), not a column on tasks. Existence was already checked above by
	// validateTaskWrites; a cycle check is unnecessary here — a brand-new
	// task has no ID until NextTaskID ran above, so nothing existing could
	// already hold an edge pointing at it (same reasoning validateParentID
	// uses to skip cycle checks on Create).
	if len(input.DependsOn) > 0 {
		if err := s.store.SetTaskDependencies(id, input.DependsOn); err != nil {
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

// TaskUpdateInput wraps the store-level TaskUpdate and adds tags/depends_on
// fields. The store-level TaskUpdate no longer carries these because they
// live in link tables (task_tags, task_dependencies), not columns on tasks.
type TaskUpdateInput struct {
	sqlstore.TaskUpdate
	Tags      *[]string // nil = no change; non-nil = replace all linked tags
	DependsOn *[]string // nil = no change; non-nil = replace all dependency edges
}

// Update applies a partial update to a task. If Tags is non-nil, linked
// tags are resolved and replaced.
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
	// title is the one truly-required field (mirrors Create's check). Now that
	// the MCP layer detects title presence-based (FIX-001), an explicit
	// "title": "" reaches here and must be rejected with a clean
	// ValidationError rather than falling through to a raw DB NOT NULL error.
	if input.Title != nil && *input.Title == "" {
		return &ValidationError{Field: "title", Message: "title cannot be cleared to empty"}
	}

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
	if err := validateRequiredWorkflowMetadata(effectiveMetadata); err != nil {
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

	// depends_on cycle check (migration 027 / FK-003). Same rationale as
	// validateParentID: runs before the store write so a cycle-producing
	// update (e.g. A depends_on B, B depends_on A) is rejected with a
	// ValidationError instead of committed. A mutual/circular dependency is
	// a second, self-inflicted flavor of the scheduler deadlock this task
	// exists to fix — ON DELETE CASCADE does nothing for it, since neither
	// task is ever deleted. Only evaluated when the caller is touching
	// depends_on; Create never needs this (see the comment there).
	if input.DependsOn != nil {
		if err := s.validateDependsOnCycle(id, *input.DependsOn); err != nil {
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
		if err := s.store.SetTaskTags(id, slugs); err != nil {
			return err
		}
	}
	if input.DependsOn != nil {
		if err := s.store.SetTaskDependencies(id, *input.DependsOn); err != nil {
			return err
		}
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

// validateDependsOnCycle enforces that adding the given candidate depends_on
// edges from taskID does not create a directed cycle in the depends_on
// graph (migration 027 / FK-003). Unlike parent_id, depends_on is a
// many-to-many DAG (a task can depend on several others, and several tasks
// can share a dependency), not a tree, so this walks a DFS over each
// candidate's own dependency edges — rather than a single-parent ancestor
// chain — looking for a path back to taskID.
//
// A visited-set prevents revisiting shared nodes in diamond-shaped
// dependency graphs (A depends on B and C, both depend on D) blowing up
// into exponential re-walks; a 256-hop cap guards against pathological or
// malformed graphs, mirroring validateParentID's guard.
func (s *TaskService) validateDependsOnCycle(taskID string, candidates []string) error {
	visited := map[string]bool{}
	var walk func(id string, hops int) error
	walk = func(id string, hops int) error {
		if id == taskID {
			if hops == 0 {
				return &ValidationError{Field: "depends_on", Message: "depends_on cannot reference the task itself"}
			}
			return &ValidationError{Field: "depends_on", Message: "depends_on would create a cycle through task " + taskID}
		}
		if hops > 256 {
			return &ValidationError{Field: "depends_on", Message: "depends_on chain exceeds 256 hops"}
		}
		if visited[id] {
			return nil
		}
		visited[id] = true
		deps, err := s.store.ListTaskDependencyIDs(id)
		if err != nil {
			// Broken/unreadable chain — treat as no cycle to avoid false
			// positives, mirroring validateParentID's handling of a broken
			// ancestor chain.
			return nil
		}
		for _, d := range deps {
			if err := walk(d, hops+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, depID := range candidates {
		if err := walk(depID, 0); err != nil {
			return err
		}
	}
	return nil
}

// ListTags returns the tags linked to a task.
func (s *TaskService) ListTags(taskID string) ([]sqlstore.TagRecord, error) {
	return s.store.ListTaskTags(taskID)
}

// ListDependencyIDs returns the depends_on task IDs linked to a task, in
// the order they were set (task_dependencies.sort_order ASC). Every ID
// returned still exists — ON DELETE CASCADE on depends_on_task_id prunes
// the edge automatically when the dependency task is deleted (FK-003).
func (s *TaskService) ListDependencyIDs(taskID string) ([]string, error) {
	return s.store.ListTaskDependencyIDs(taskID)
}

// Transition moves a task to a new status if the FSM allows it.
//
// ctx carries the inbound HTTP/MCP server span (when called from an
// instrumented surface) so torque.task.transition nests under it; tests pass
// context.Background() and trace as a root span.
func (s *TaskService) Transition(ctx context.Context, id, newStatus string) (err error) {
	ctx, span := feotel.StartSpan(ctx, "torque.task.transition")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("hollis.task.id", id),
		attribute.String("torque.task.to_status", newStatus),
		attribute.Bool("torque.task.forced", false),
	)
	defer func() {
		// TransitionError ("unknown source status" / "transition not permitted")
		// is a POLICY reject the caller surfaces as 422 — not an infra fault.
		// Skip RecordError for those; real store / GetTask errors still record.
		if err != nil {
			var terr *TransitionError
			if !errors.As(err, &terr) {
				span.RecordError(err)
			}
		}
		span.End()
	}()

	task, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	span.SetAttributes(attribute.String("torque.task.from_status", task.Status))

	allowed, ok := validTransitions[task.Status]
	if !ok {
		return &TransitionError{From: task.Status, To: newStatus, Message: "unknown source status"}
	}
	for _, a := range allowed {
		if a == newStatus {
			if err := s.store.TransitionTask(id, newStatus); err != nil {
				return err
			}
			s.notifyTransition(ctx, id, task.Status, newStatus)
			return nil
		}
	}
	return &TransitionError{
		From:    task.Status,
		To:      newStatus,
		Message: "transition not permitted",
	}
}

// ForceTransition writes the new status without consulting the FSM. Use only
// for explicit user-initiated cleanup (e.g., dispositioning a stuck task that
// an agent left mid-flight); programmatic callers must use Transition. The
// store still validates that the status string is a recognized value.
func (s *TaskService) ForceTransition(ctx context.Context, id, newStatus string) (err error) {
	ctx, span := feotel.StartSpan(ctx, "torque.task.transition")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("hollis.task.id", id),
		attribute.String("torque.task.to_status", newStatus),
		attribute.Bool("torque.task.forced", true),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

	task, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	span.SetAttributes(attribute.String("torque.task.from_status", task.Status))
	if err := s.store.TransitionTask(id, newStatus); err != nil {
		return err
	}
	s.notifyTransition(ctx, id, task.Status, newStatus)
	return nil
}

func (s *TaskService) notifyTransition(ctx context.Context, id, from, to string) {
	if s.transitionObserver == nil {
		return
	}
	s.transitionObserver.ObserveTaskTransition(ctx, id, from, to)
}

// Search performs a text search over tasks.
func (s *TaskService) Search(query string) ([]sqlstore.TaskRecord, error) {
	return s.store.SearchTasks(query)
}

// ListSubtodos returns the subtodo checklist for a task.
func (s *TaskService) ListSubtodos(taskID string) ([]sqlstore.Subtodo, error) {
	return s.store.GetSubtodos(taskID)
}

// AddSubtodo appends a checklist item. The caller may supply a meaningful
// slug id (e.g. "check-auth-flow") to keep it deterministic across repeated
// emits; when id is omitted, the server generates one (see newSubtodoID).
// Duplicate ids — caller-supplied or generated — are rejected so downstream
// mark-done calls stay unambiguous.
func (s *TaskService) AddSubtodo(taskID string, item sqlstore.Subtodo) ([]sqlstore.Subtodo, error) {
	if item.Text == "" {
		return nil, &ValidationError{Field: "text", Message: "text is required"}
	}
	existing, err := s.store.GetSubtodos(taskID)
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		item.ID = newSubtodoID()
		// newSubtodoID is a 64-bit random value under a dedicated prefix, so
		// a collision with an existing (generated or caller-supplied) id is
		// vanishingly unlikely; regenerate defensively rather than fail.
		for subtodoIDTaken(existing, item.ID) {
			item.ID = newSubtodoID()
		}
	} else if subtodoIDTaken(existing, item.ID) {
		return nil, &ValidationError{Field: "id", Message: "duplicate subtodo id: " + item.ID}
	}
	existing = append(existing, item)
	if err := s.store.SetSubtodos(taskID, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func subtodoIDTaken(items []sqlstore.Subtodo, id string) bool {
	for _, e := range items {
		if e.ID == id {
			return true
		}
	}
	return false
}

// newSubtodoID returns a short random subtodo id under a dedicated "sub_"
// prefix so generated ids can never collide with a caller-supplied
// slug-style id (e.g. "check-auth-flow") — slugs are free to omit that
// prefix entirely. Mirrors the crypto/rand + hex approach used by
// internal/runtime/scheduler.newEventID: 8 random bytes (64 bits) is ample
// for uniqueness within a single task's checklist (typically low tens of
// items), and subtodos have no global/cross-task query surface that would
// call for a date-bucketed sequential scheme like the other entity IDs.
func newSubtodoID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never errors on a healthy OS RNG; fall back to a
		// time-based id so the field is never empty.
		return fmt.Sprintf("sub_%d", time.Now().UnixNano())
	}
	return "sub_" + hex.EncodeToString(b[:])
}

// MarkSubtodoDone ticks a single item and records its evidence.
func (s *TaskService) MarkSubtodoDone(taskID, itemID, evidence string) ([]sqlstore.Subtodo, error) {
	if err := s.store.SetSubtodoDone(taskID, itemID, evidence); err != nil {
		return nil, err
	}
	return s.store.GetSubtodos(taskID)
}

// UpdateSubtodo edits the text and/or required flag on an existing item.
// nil-valued fields leave their current value untouched. Returns the full
// updated checklist.
func (s *TaskService) UpdateSubtodo(taskID, itemID string, text *string, required *bool) ([]sqlstore.Subtodo, error) {
	items, err := s.store.GetSubtodos(taskID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID != itemID {
			continue
		}
		if text != nil {
			if *text == "" {
				return nil, &ValidationError{Field: "text", Message: "text cannot be empty"}
			}
			items[i].Text = *text
		}
		if required != nil {
			items[i].Required = *required
		}
		if err := s.store.SetSubtodos(taskID, items); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, &ValidationError{Field: "id", Message: "subtodo not found: " + itemID}
}

// DeleteSubtodo removes a checklist item by id. Returns the full updated
// checklist (which may be empty).
func (s *TaskService) DeleteSubtodo(taskID, itemID string) ([]sqlstore.Subtodo, error) {
	items, err := s.store.GetSubtodos(taskID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == itemID {
			items = append(items[:i], items[i+1:]...)
			if err := s.store.SetSubtodos(taskID, items); err != nil {
				return nil, err
			}
			return items, nil
		}
	}
	return nil, &ValidationError{Field: "id", Message: "subtodo not found: " + itemID}
}

// BulkTransition applies the same status transition to multiple tasks. It
// returns the slice of task IDs that successfully transitioned (in input
// order, with failures dropped) plus a slice of errors for the failures. The
// caller needs the actual successful IDs — not just a count — to broadcast
// per-task SSE events or otherwise act per-item on the partial-success case.
func (s *TaskService) BulkTransition(ctx context.Context, ids []string, newStatus string) ([]string, []error) {
	// Wrapping span: per-item torque.task.transition spans nest under this so
	// an operator sees "BulkTransition of N tasks" as one unit, with each task
	// drilldown still available. Bulk operations don't expose a single err
	// (callers see partial-success: succeeded IDs + []error), so the wrapper
	// span doesn't RecordError; per-item spans capture individual faults.
	ctx, span := feotel.StartSpan(ctx, "torque.task.transition.bulk")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.Int("torque.task.bulk_count", len(ids)),
		attribute.String("torque.task.to_status", newStatus),
	)
	defer span.End()

	var errs []error
	succeeded := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := s.Transition(ctx, id, newStatus); err != nil {
			errs = append(errs, err)
		} else {
			succeeded = append(succeeded, id)
		}
	}
	span.SetAttributes(
		attribute.Int("torque.task.bulk_success", len(succeeded)),
		attribute.Int("torque.task.bulk_failed", len(errs)),
	)
	return succeeded, errs
}
