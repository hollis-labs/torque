package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/torque/internal/aar"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// aarSubmitInput mirrors the agent-visible payload for torque_aar_submit. The
// shape is intentionally close to aar.Reflection — the handler validates +
// promotes it directly without an intermediate aar.Reflection conversion in
// the agent's mental model. Outcome is parsed via aar.ValidateOutcome; empty
// defaults to OutcomeSuccess at write time.
type aarSubmitInput struct {
	Summary            string   `json:"summary"`
	Outcome            string   `json:"outcome"`
	Clunky             string   `json:"clunky"`
	Automatable        string   `json:"automatable"`
	ManualShouldBeAuto string   `json:"manual_should_be_auto"`
	SharpEdges         string   `json:"sharp_edges"`
	Suggestions        string   `json:"suggestions"`
	Errors             []string `json:"errors,omitempty"`
}

// registerLoopbackAARTool wires torque_aar_submit onto the loopback adapter.
// Called from registerLoopbackTools so every worker session that gets a
// loopback also gets the AAR submission primitive. The handler:
//
//  1. Validates the reflection payload (reflection_json is a JSON object,
//     parses into aarSubmitInput, outcome is in the canonical set).
//  2. Resolves run-identity fields from the bound task + its most recent
//     run record (the worker's active run when the AAR is submitted).
//  3. Renders the AAR markdown via aar.Render.
//  4. Persists the document as an ArtifactRecord{Type:aar.ArtifactType,
//     TaskID, RunID, Content:<markdown>, Metadata:<structured JSON>}.
//  5. Returns the created artifact record so the agent has a stable
//     pointer (artifact id) it can reference in its closing comment.
//
// The metadata blob stores the structured reflection AND the parsed
// outcome so CLI aggregation tools can filter / group without re-parsing
// the markdown body.
func (a *Adapter) registerLoopbackAARTool() {
	a.addTool(mcp.NewTool("torque_aar_submit",
		mcp.WithDescription(`Submit the run's After-Action Report — the structured reflection log every Torque agent run is expected to file before signaling completion (CW-20260519-0088).

The AAR captures process / DX feedback the operator cannot infer from commits or error logs. Run-identity fields (task_id, run_id, agent_profile, started_at, ended_at, project_id, sprint_id, title) are auto-populated from the task and the active run; you only fill in the reflection sections.

Call exactly once per run, AFTER your verification comment and BEFORE torque_task_review / torque_task_blocked. Empty reflection strings are acceptable for sections you have nothing to say about — a "nothing to flag" AAR is still a queryable signal — but every key MUST be present in reflection_json so the document shape stays uniform across runs.

reflection_json fields (all string unless noted):
  summary                  — one-paragraph prose of what was attempted + accomplished
  outcome                  — "success" | "partial" | "blocked" | "failed" (defaults to "success" when empty)
  clunky                   — what was clunky, confusing, or surprising
  automatable              — what could be automated / become a deterministic harness step
  manual_should_be_auto    — what manual step should the system have done for you
  sharp_edges              — sharp edges hit and how they were worked around
  suggestions              — concrete suggestions for the next run (system / DX / process)
  errors (array of string) — optional: concrete errors / failed steps observed in this run

Response shape: data = {<ArtifactRecord fields>} — the created AAR artifact (Type="aar").
Example: {"reflection_json":"{\"summary\":\"Built X.\",\"outcome\":\"success\",\"clunky\":\"...\",\"automatable\":\"\",\"manual_should_be_auto\":\"\",\"sharp_edges\":\"\",\"suggestions\":\"\"}"}`),
		mcp.WithString("reflection_json", mcp.Required(), mcp.Description("JSON object of reflection fields (see description for the canonical keys)")),
	), a.handleLoopbackAARSubmit)
}

// handleLoopbackAARSubmit is the concrete handler for torque_aar_submit. It
// parses + validates the payload, resolves identity fields from the task and
// the most recent run, renders the markdown body via the aar package, and
// stores the result as a typed artifact attached to the bound task.
//
// RunID resolution: at AAR submission time the worker is still in `doing`
// state (the AAR is submitted BEFORE the lifecycle signal), so the latest
// row in the runs table for this task is the worker's own run. Listing in
// "newest first" order and taking the first row is the canonical lookup.
// When no run exists (e.g. tests using NewLoopback without scheduler), the
// artifact is still written with RunID unset so the AAR remains a record
// of the agent's reflection.
func (a *Adapter) handleLoopbackAARSubmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := reqStr(req, "reflection_json")
	if raw == "" {
		return errResult(ErrCodeArgInvalid, "reflection_json is required", "reflection_json")
	}

	var in aarSubmitInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid reflection_json: %v", err), "reflection_json")
	}

	outcome := aar.Outcome(in.Outcome)
	if err := aar.ValidateOutcome(outcome); err != nil {
		return errResult(ErrCodeArgInvalid, err.Error(), "outcome")
	}
	if outcome == "" {
		outcome = aar.OutcomeSuccess
	}

	task, err := a.svc.Task.Get(a.loopbackTaskID)
	if err != nil {
		return errFromService(err)
	}

	id := aar.Identity{
		TaskID:       task.ID,
		AgentProfile: task.AgentProfile,
		Title:        task.Title,
	}
	if task.ProjectID.Valid {
		id.ProjectID = task.ProjectID.String
	}
	if task.SprintID.Valid {
		id.SprintID = task.SprintID.String
	}
	if task.EpicID.Valid {
		id.EpicID = task.EpicID.String
	}

	// Resolve the active (latest) run for this task. The runs list is
	// ordered newest-first by started_at, so the head is the worker's own
	// active run at AAR submission time.
	var (
		runID            int64
		runStartedAt     time.Time
		runEndedAt       time.Time
		runRecordExisted bool
	)
	runs, runsErr := a.svc.Run.List(a.loopbackTaskID)
	if runsErr == nil && len(runs) > 0 {
		latest := runs[0]
		runID = latest.ID
		runStartedAt = latest.StartedAt
		if latest.EndedAt.Valid {
			runEndedAt = latest.EndedAt.Time
		}
		runRecordExisted = true
	}
	id.RunID = runID
	id.StartedAt = runStartedAt
	// EndedAt is typically unset at submission time (the run hasn't been
	// completed yet) — stamp now() in that case so the AAR has a useful
	// "filed at" timestamp even before CompleteRun lands.
	if runEndedAt.IsZero() {
		id.EndedAt = time.Now().UTC()
	} else {
		id.EndedAt = runEndedAt
	}

	reflection := aar.Reflection{
		Summary:            in.Summary,
		Outcome:            outcome,
		Clunky:             in.Clunky,
		Automatable:        in.Automatable,
		ManualShouldBeAuto: in.ManualShouldBeAuto,
		SharpEdges:         in.SharpEdges,
		Suggestions:        in.Suggestions,
		Errors:             in.Errors,
	}
	body := aar.Render(id, reflection)

	// Build the metadata blob. Anything queryable downstream lives here so
	// the CLI aggregator can filter without parsing the markdown body.
	meta := map[string]any{
		"schema":               aar.Schema,
		"outcome":              string(outcome),
		"agent_profile":        id.AgentProfile,
		"project_id":           id.ProjectID,
		"sprint_id":            id.SprintID,
		"epic_id":              id.EpicID,
		"started_at":           id.StartedAt.UTC().Format(time.RFC3339),
		"ended_at":             id.EndedAt.UTC().Format(time.RFC3339),
		"reflection_populated": reflectionPopulated(reflection),
		"errors_count":         len(reflection.Errors),
	}
	if !runRecordExisted {
		meta["run_lookup"] = "missing_or_unavailable"
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return errResult(ErrCodeInternal, "metadata serialization failed", "")
	}

	rec := &sqlstore.ArtifactRecord{
		TaskID:   a.loopbackTaskID,
		Type:     aar.ArtifactType,
		Content:  body,
		Metadata: sql.NullString{String: string(metaJSON), Valid: true},
	}
	if runID != 0 {
		rec.RunID = sql.NullInt64{Int64: runID, Valid: true}
	}
	if err := a.svc.Artifact.Create(rec); err != nil {
		return errFromService(err)
	}

	return okResult(rec)
}

// reflectionPopulated counts how many reflection sections the agent filled
// with non-empty content. Stored in the artifact metadata as a quick "is
// this a substantive AAR or a no-op submission" signal for CLI aggregation.
func reflectionPopulated(r aar.Reflection) int {
	n := 0
	for _, s := range []string{
		r.Summary,
		r.Clunky,
		r.Automatable,
		r.ManualShouldBeAuto,
		r.SharpEdges,
		r.Suggestions,
	} {
		if s != "" {
			n++
		}
	}
	if len(r.Errors) > 0 {
		n++
	}
	return n
}
