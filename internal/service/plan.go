package service

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// PlanPhase is a single phase within a plan's metadata.plan.phases[] array.
// Phase IDs are short slugs ("ph-1", "ph-2", ...) assigned at add time. Order
// is a monotonic int used for display and dispatch ordering. Acceptance is
// optional free-form prose attached to the phase.
type PlanPhase struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Order      int    `json:"order"`
	Acceptance string `json:"acceptance,omitempty"`
}

// PlanMetadata is the versioned container stored at task.metadata.plan for
// kind=plan tasks. Version lets v2 migrate the shape without a one-way break.
type PlanMetadata struct {
	Version int         `json:"version"`
	Phases  []PlanPhase `json:"phases"`
}

// PlanPhaseInput is a caller-supplied phase description before id/order are
// assigned. Empty Name entries are silently skipped so the GUI can send a
// padded repeatable-input array without having to filter client-side.
type PlanPhaseInput struct {
	Name       string `json:"name"`
	Acceptance string `json:"acceptance,omitempty"`
}

// PlanCreateInput is the user-facing input for creating a plan task. Plans
// are coordination tasks (kind=plan, manual=true) — they never dispatch.
type PlanCreateInput struct {
	Title       string
	Description string
	Priority    int
	ProjectID   string
	SprintID    string
	EpicID      string
	Phases      []PlanPhaseInput
	Tags        []string
}

// PlanProgress is the roll-up returned alongside a plan for GUI display.
// ByPhase is keyed by phase_id (or "" for children with no phase_id set).
type PlanProgress struct {
	TotalChildren int                    `json:"total_children"`
	Done          int                    `json:"done"`
	Blocked       int                    `json:"blocked"`
	ByPhase       map[string]PhaseRollup `json:"by_phase"`
}

// PhaseRollup counts the children assigned to a single phase.
type PhaseRollup struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Blocked int `json:"blocked"`
}

// PlanDetail bundles the plan task, decoded plan metadata, and progress
// roll-up for a single read.
type PlanDetail struct {
	Task     *sqlstore.TaskRecord `json:"task"`
	Plan     PlanMetadata         `json:"plan"`
	Progress PlanProgress         `json:"progress"`
}

// PlanService wraps TaskService with plan-specific conveniences. Plans are
// stored as kind=plan tasks; phase metadata lives in task.metadata.plan.
type PlanService struct {
	store *sqlstore.Store
	tasks *TaskService
}

// Create builds a kind=plan task with phases[] prefilled into metadata.plan.
func (s *PlanService) Create(input PlanCreateInput) (*sqlstore.TaskRecord, error) {
	if input.Title == "" {
		return nil, &ValidationError{Field: "title", Message: "title is required"}
	}

	meta := PlanMetadata{Version: 1}
	for i, ph := range input.Phases {
		if ph.Name == "" {
			continue
		}
		meta.Phases = append(meta.Phases, PlanPhase{
			ID:         fmt.Sprintf("ph-%d", len(meta.Phases)+1),
			Name:       ph.Name,
			Order:      i + 1,
			Acceptance: ph.Acceptance,
		})
	}

	metadataMap := map[string]any{"plan": meta}

	return s.tasks.Create(TaskCreateInput{
		Title:       input.Title,
		Description: input.Description,
		Priority:    input.Priority,
		Tags:        input.Tags,
		ProjectID:   input.ProjectID,
		SprintID:    input.SprintID,
		EpicID:      input.EpicID,
		Kind:        "plan",
		Manual:      true,
		Metadata:    metadataMap,
	})
}

// Get returns the plan task, decoded metadata, and progress roll-up.
func (s *PlanService) Get(planID string) (*PlanDetail, error) {
	task, err := s.tasks.Get(planID)
	if err != nil {
		return nil, err
	}
	if task.Kind != "plan" {
		return nil, &ValidationError{Field: "kind", Message: "task " + planID + " is not a plan"}
	}
	plan, err := decodePlan(task)
	if err != nil {
		return nil, err
	}
	progress, err := s.progress(planID, plan)
	if err != nil {
		return nil, err
	}
	return &PlanDetail{Task: task, Plan: plan, Progress: progress}, nil
}

// AddPhase appends a phase to metadata.plan.phases with an auto-generated id
// and a monotonic order. Returns the new phase id.
func (s *PlanService) AddPhase(planID, name, acceptance string) (string, error) {
	if name == "" {
		return "", &ValidationError{Field: "name", Message: "phase name is required"}
	}

	task, err := s.tasks.Get(planID)
	if err != nil {
		return "", err
	}
	if task.Kind != "plan" {
		return "", &ValidationError{Field: "kind", Message: "task " + planID + " is not a plan"}
	}

	plan, err := decodePlan(task)
	if err != nil {
		return "", err
	}

	phaseID := nextPhaseID(plan.Phases)
	plan.Phases = append(plan.Phases, PlanPhase{
		ID:         phaseID,
		Name:       name,
		Order:      nextPhaseOrder(plan.Phases),
		Acceptance: acceptance,
	})

	if err := s.writePlanMetadata(task, plan); err != nil {
		return "", err
	}
	return phaseID, nil
}

// RemovePhase removes a phase from the plan. Fails when any child task still
// references the phase via metadata.phase_id — child tasks must be reassigned
// or deleted first.
func (s *PlanService) RemovePhase(planID, phaseID string) error {
	if phaseID == "" {
		return &ValidationError{Field: "phase_id", Message: "phase_id is required"}
	}

	task, err := s.tasks.Get(planID)
	if err != nil {
		return err
	}
	if task.Kind != "plan" {
		return &ValidationError{Field: "kind", Message: "task " + planID + " is not a plan"}
	}

	children, err := s.ListChildren(planID, phaseID)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return &ValidationError{
			Field:   "phase_id",
			Message: fmt.Sprintf("cannot remove phase %s: %d child task(s) still reference it", phaseID, len(children)),
		}
	}

	plan, err := decodePlan(task)
	if err != nil {
		return err
	}
	filtered := plan.Phases[:0]
	found := false
	for _, ph := range plan.Phases {
		if ph.ID == phaseID {
			found = true
			continue
		}
		filtered = append(filtered, ph)
	}
	if !found {
		return &ValidationError{Field: "phase_id", Message: "phase not found: " + phaseID}
	}
	plan.Phases = filtered
	return s.writePlanMetadata(task, plan)
}

// ListChildren returns tasks whose parent_id = planID, optionally narrowed to
// a specific phase_id. Pass empty phaseID to return all children.
func (s *PlanService) ListChildren(planID, phaseID string) ([]sqlstore.TaskRecord, error) {
	children, err := s.tasks.List(sqlstore.TaskFilter{ParentID: planID})
	if err != nil {
		return nil, err
	}
	if phaseID == "" {
		return children, nil
	}
	out := make([]sqlstore.TaskRecord, 0, len(children))
	for _, c := range children {
		if childPhaseID(c) == phaseID {
			out = append(out, c)
		}
	}
	return out, nil
}

// Progress returns the roll-up without the full plan payload. Callers that
// already have the plan should use Get.
func (s *PlanService) Progress(planID string) (PlanProgress, error) {
	task, err := s.tasks.Get(planID)
	if err != nil {
		return PlanProgress{}, err
	}
	if task.Kind != "plan" {
		return PlanProgress{}, &ValidationError{Field: "kind", Message: "task " + planID + " is not a plan"}
	}
	plan, err := decodePlan(task)
	if err != nil {
		return PlanProgress{}, err
	}
	return s.progress(planID, plan)
}

func (s *PlanService) progress(planID string, plan PlanMetadata) (PlanProgress, error) {
	children, err := s.tasks.List(sqlstore.TaskFilter{ParentID: planID})
	if err != nil {
		return PlanProgress{}, err
	}
	by := make(map[string]PhaseRollup, len(plan.Phases)+1)
	for _, ph := range plan.Phases {
		by[ph.ID] = PhaseRollup{}
	}
	out := PlanProgress{TotalChildren: len(children), ByPhase: by}
	for _, c := range children {
		if c.Status == "done" {
			out.Done++
		}
		if c.Status == "blocked" {
			out.Blocked++
		}
		pid := childPhaseID(c)
		roll := by[pid]
		roll.Total++
		if c.Status == "done" {
			roll.Done++
		}
		if c.Status == "blocked" {
			roll.Blocked++
		}
		by[pid] = roll
	}
	out.ByPhase = by
	return out, nil
}

// writePlanMetadata re-serializes the metadata map with the updated plan
// block and pushes it through TaskService.Update so validation still runs.
func (s *PlanService) writePlanMetadata(task *sqlstore.TaskRecord, plan PlanMetadata) error {
	metadata := map[string]any{}
	if task.Metadata.Valid && task.Metadata.String != "" {
		_ = json.Unmarshal([]byte(task.Metadata.String), &metadata)
	}
	metadata["plan"] = plan
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	update := sqlstore.TaskUpdate{
		Metadata: &sql.NullString{String: string(raw), Valid: true},
	}
	return s.tasks.Update(task.ID, TaskUpdateInput{TaskUpdate: update})
}

// decodePlan extracts metadata.plan from a task record, returning an empty
// plan (version=1, no phases) when the task has no plan block.
func decodePlan(task *sqlstore.TaskRecord) (PlanMetadata, error) {
	plan := PlanMetadata{Version: 1}
	if !task.Metadata.Valid || task.Metadata.String == "" {
		return plan, nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(task.Metadata.String), &metadata); err != nil {
		return plan, fmt.Errorf("decode task metadata: %w", err)
	}
	raw, ok := metadata["plan"]
	if !ok {
		return plan, nil
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return plan, fmt.Errorf("decode plan metadata: %w", err)
	}
	if plan.Version == 0 {
		plan.Version = 1
	}
	return plan, nil
}

// childPhaseID reads metadata.phase_id from a child task, returning "" when
// the child is not phase-tagged.
func childPhaseID(task sqlstore.TaskRecord) string {
	if !task.Metadata.Valid || task.Metadata.String == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(task.Metadata.String), &m); err != nil {
		return ""
	}
	v, _ := m["phase_id"].(string)
	return v
}

// nextPhaseID generates a short slug id for a new phase. Uses the next free
// integer suffix to stay human-readable; skips any already-taken ids to keep
// ids unique across the plan's lifetime.
func nextPhaseID(phases []PlanPhase) string {
	taken := map[string]bool{}
	for _, ph := range phases {
		taken[ph.ID] = true
	}
	for i := 1; i < 1000; i++ {
		id := fmt.Sprintf("ph-%d", i)
		if !taken[id] {
			return id
		}
	}
	return fmt.Sprintf("ph-%d", len(phases)+1)
}

// nextPhaseOrder returns one past the current max order, so new phases land
// at the end of the display order.
func nextPhaseOrder(phases []PlanPhase) int {
	max := 0
	for _, ph := range phases {
		if ph.Order > max {
			max = ph.Order
		}
	}
	return max + 1
}
