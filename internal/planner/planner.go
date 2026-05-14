// Package planner implements the V0 Plan Refinement agent
// (CW-20260503-0020, S2.4). The Orchestrator (S2.2) calls Build at
// plan-execute boot to spawn a kind=internal planner task, waits for
// it to reach `done`, then reads the refinement back via Read.
//
// V0 is advisory: the Orchestrator still walks phases as authored.
// The refinement lives in `metadata.plan.planner_refinement` JSON +
// a `[system/planner]` comment on the plan task. No new tables, no
// new substrate.
package planner

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

const (
	// Profile is the agent_profile name stamped on planner tasks.
	// Resolves through config.GetProfileOrDefault — operators can
	// override in profiles.yaml; the substrate ships a builtin
	// fallback in internal/config so fresh installs dispatch.
	Profile = "planner"

	// CommentAuthor is the comment-author prefix the planner uses on
	// every comment it posts to the plan task. Operator audits can
	// grep all planner activity by this token.
	CommentAuthor = "[system/planner]"

	// TemplateEnvVar is the user override for the planner template
	// directory. When unset the resolver looks in
	// $HOME/.torque/agent-templates/.
	TemplateEnvVar = "TORQUE_AGENT_TEMPLATE_DIR"

	// TemplateName is the file the resolver reads from the configured
	// dir. Operators replace its contents to customize the V0 prompt.
	TemplateName = "default-planner.md"

	// MetadataKey is the plan task's metadata.plan.planner_refinement
	// path (operates on the existing plan-task metadata blob).
	MetadataKey = "planner_refinement"

	// MetadataTimestampKey is the RFC3339 timestamp written alongside
	// the refinement on every successful refresh.
	MetadataTimestampKey = "planner_refined_at"

	// defaultBudget is the cost ceiling stamped on a freshly enqueued
	// planner task. Lower than the reviewer's budget because V0 only
	// reads the plan and emits a single response — no iterative work.
	defaultBudget = 0.30
)

//go:embed templates/default-planner.md
var embeddedTemplate string

// Refinement is the structured shape persisted under
// metadata.plan.planner_refinement. Mirrors the schema in the V0
// template prompt — every field is optional from the planner's side,
// but readers should treat empty slices/maps as "no observation"
// rather than "no opinion".
type Refinement struct {
	Version    string            `json:"version"`
	PerTask    []TaskHint        `json:"per_task,omitempty"`
	PlanLevel  *PlanLevelChanges `json:"plan_level,omitempty"`
	Confidence string            `json:"confidence,omitempty"`
	Notes      string            `json:"notes,omitempty"`
}

// TaskHint is the per-child advisory entry the planner emits. The
// Orchestrator can apply suggested_priority and est_token_budget when
// walking phases; context_to_inject and blockers_flagged are surfaced
// to the executor at dispatch.
type TaskHint struct {
	TaskID            string   `json:"task_id"`
	SuggestedPriority int      `json:"suggested_priority,omitempty"`
	EstTokenBudget    int      `json:"est_token_budget,omitempty"`
	ContextToInject   string   `json:"context_to_inject,omitempty"`
	BlockersFlagged   []string `json:"blockers_flagged,omitempty"`
}

// PlanLevelChanges captures observations that span the plan rather
// than a single child. Each slice is a list of free-form statements
// the planner authored.
type PlanLevelChanges struct {
	Ordering       []string `json:"ordering,omitempty"`
	PhaseChanges   []string `json:"phase_changes,omitempty"`
	AcceptanceGaps []string `json:"acceptance_gaps,omitempty"`
	Redundancies   []string `json:"redundancies,omitempty"`
}

// BuildOptions parametrizes BuildTask. Caller supplies the plan-task
// id (the target the planner audits); the rest defaults to V0
// canonical values, overridable for tests / future v1 callers.
type BuildOptions struct {
	// TargetPlanID is the kind=plan task the planner refines. Required.
	TargetPlanID string

	// IDFn lets tests pin task ids. Production wires the
	// store.NextTaskID-style allocator at the call site.
	IDFn func() (string, error)

	// Now lets tests pin timestamps. Production passes time.Now.
	Now func() time.Time

	// Inherit copies project_id / sprint_id / epic_id from the target
	// plan onto the planner task so listing endpoints filter both as
	// a unit. Pass the loaded plan record; nil leaves them blank.
	Inherit *sqlstore.TaskRecord

	// Template lets the caller pass a pre-resolved template string,
	// short-circuiting LoadTemplate. Tests use this to assert the
	// task's system_prompt without touching the user's ~/.torque.
	Template string
}

// BuildTask returns a kind=internal task record ready for store
// insertion. The Orchestrator inserts via store.CreateTask (NOT
// service.Task.Create — that path force-flips manual=true via the
// CW-20260417-0133 safety override; planner tasks must auto-dispatch),
// then waits on its `done` transition before reading the refinement.
//
// Caller MUST set MaxRetries=0 via UpdateTask after CreateTask if the
// no-retry V0 contract matters — applyDefaults can't distinguish
// "explicitly zero" from "use default 3" on int fields. Mirrors the
// pattern in scheduler/end_agent.go (see CW-20260503-0019).
func BuildTask(opts BuildOptions) (*sqlstore.TaskRecord, error) {
	if opts.TargetPlanID == "" {
		return nil, fmt.Errorf("planner: BuildTask requires TargetPlanID")
	}
	if opts.IDFn == nil {
		return nil, fmt.Errorf("planner: BuildTask requires IDFn")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	template := opts.Template
	if template == "" {
		template, _ = LoadTemplate()
	}

	id, err := opts.IDFn()
	if err != nil {
		return nil, fmt.Errorf("planner: allocate task id: %w", err)
	}

	meta, err := json.Marshal(map[string]any{
		"planner": map[string]any{
			"target_plan_id": opts.TargetPlanID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("planner: marshal metadata: %w", err)
	}

	rec := &sqlstore.TaskRecord{
		ID:                   id,
		Title:                "planner: " + opts.TargetPlanID,
		Description:          "Plan refinement (V0) for " + opts.TargetPlanID + " (CW-20260503-0020).",
		Status:               "todo",
		Priority:             2,
		Manual:               false,
		Executor:             "cli",
		AgentProfile:         Profile,
		SystemPrompt:         template,
		MaxRetries:           0,
		OnDone:               "close",
		OnFail:               "block",
		OnReview:             "pause",
		OnDoneMerge:          "none",
		Kind:                 "internal",
		SourceType:           "agent",
		Trust:                "normal",
		CheckpointMode:       "none",
		OnCheckpointResponse: "resume",
		Metadata:             sql.NullString{String: string(meta), Valid: true},
		CostBudget:           sql.NullFloat64{Float64: defaultBudget, Valid: true},
		ParentID:             sql.NullString{String: opts.TargetPlanID, Valid: true},
	}
	if opts.Inherit != nil {
		rec.WorkingDir = opts.Inherit.WorkingDir
		rec.ProjectID = opts.Inherit.ProjectID
		rec.SprintID = opts.Inherit.SprintID
		rec.EpicID = opts.Inherit.EpicID
	}
	return rec, nil
}

// LoadTemplate returns the planner template content + the resolved
// path. Lookup order: $TORQUE_AGENT_TEMPLATE_DIR/default-planner.md,
// then $HOME/.torque/agent-templates/default-planner.md, then the
// embedded fallback ("<embedded>" path token).
func LoadTemplate() (string, string) {
	dir := os.Getenv(TemplateEnvVar)
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".torque", "agent-templates")
		}
	}
	if dir != "" {
		path := filepath.Join(dir, TemplateName)
		if data, err := os.ReadFile(path); err == nil {
			return string(data), path
		}
	}
	return embeddedTemplate, "<embedded>"
}

// Write persists a refinement to the plan task's metadata. The plan
// task's existing metadata blob is preserved — Write only replaces the
// `plan.planner_refinement` and `plan.planner_refined_at` keys under
// the top-level `plan` namespace. Used by the planner agent (via MCP
// loopback through torque_task_update) and by tests.
func Write(store *sqlstore.Store, planID string, ref *Refinement, now time.Time) error {
	if planID == "" {
		return fmt.Errorf("planner: Write requires planID")
	}
	if ref == nil {
		return fmt.Errorf("planner: Write requires refinement")
	}
	if ref.Version == "" {
		ref.Version = "v0"
	}

	plan, err := store.GetTask(planID)
	if err != nil {
		return fmt.Errorf("planner: get plan %s: %w", planID, err)
	}

	root := map[string]any{}
	if plan.Metadata.Valid && plan.Metadata.String != "" {
		_ = json.Unmarshal([]byte(plan.Metadata.String), &root)
	}
	planNS, _ := root["plan"].(map[string]any)
	if planNS == nil {
		planNS = map[string]any{}
	}
	planNS[MetadataKey] = ref
	planNS[MetadataTimestampKey] = now.UTC().Format(time.RFC3339)
	root["plan"] = planNS

	updated, err := json.Marshal(root)
	if err != nil {
		return fmt.Errorf("planner: marshal metadata: %w", err)
	}

	metaNS := sql.NullString{String: string(updated), Valid: true}
	if err := store.UpdateTask(planID, sqlstore.TaskUpdate{
		Metadata: &metaNS,
	}); err != nil {
		return fmt.Errorf("planner: update plan metadata: %w", err)
	}
	return nil
}

// Read returns the refinement persisted under the plan task's
// metadata.plan.planner_refinement. Returns (nil, nil) when no
// refinement has been written yet — callers fall back to the
// as-authored plan in that case.
func Read(store *sqlstore.Store, planID string) (*Refinement, error) {
	if planID == "" {
		return nil, fmt.Errorf("planner: Read requires planID")
	}
	plan, err := store.GetTask(planID)
	if err != nil {
		return nil, fmt.Errorf("planner: get plan %s: %w", planID, err)
	}
	if !plan.Metadata.Valid || plan.Metadata.String == "" {
		return nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(plan.Metadata.String), &root); err != nil {
		return nil, fmt.Errorf("planner: unmarshal metadata: %w", err)
	}
	planNS, _ := root["plan"].(map[string]any)
	raw, ok := planNS[MetadataKey]
	if !ok {
		return nil, nil
	}
	// json.Unmarshal lands the inner object as map[string]any; round-
	// trip through Marshal/Unmarshal so the typed struct fills in.
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("planner: re-marshal refinement: %w", err)
	}
	var ref Refinement
	if err := json.Unmarshal(body, &ref); err != nil {
		return nil, fmt.Errorf("planner: unmarshal refinement: %w", err)
	}
	return &ref, nil
}
