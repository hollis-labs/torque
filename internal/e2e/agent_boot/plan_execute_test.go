package agent_boot

// Plan-execute substrate composition tests — ported from the legacy
// internal/e2e/plan_execute/ build-tagged package after CW-20260508-0001
// deleted sessionmgr.Manager. The substrate now goes through agent.Manager;
// the first-turn-fire that previously required a manual SendInput post-
// Launch is now native via go-agent-sessions v0.6.0's AutoFireFirstTurn,
// asserted at the StartOptions field level rather than via SendInput
// counter inspection.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/orchestrator"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/planstart"
)

// createPlanTask inserts a minimal kind=plan record matching what the
// PlanService would create. We bypass the service to keep the test
// closed-loop and avoid pulling the validation rules through a path
// where they're already tested.
func createPlanTask(t *testing.T, store *sqlstore.Store, id, workdir string) *sqlstore.TaskRecord {
	t.Helper()
	// tasks.project_id is a real FK (FK-002, migration 028) — PRJ-SMOKE must
	// exist before a task can reference it.
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-SMOKE", Name: "Smoke Project"}))
	plan := &sqlstore.TaskRecord{
		ID:           id,
		Title:        "Smoke plan: " + id,
		Description:  "Composition test for plan-start → orchestrator boot",
		Status:       "todo",
		Kind:         "plan",
		Executor:     "cli",
		AgentProfile: orchestrator.Profile,
		WorkingDir:   workdir,
		ProjectID:    sql.NullString{String: "PRJ-SMOKE", Valid: true},
		Metadata: sql.NullString{
			String: `{"plan":{"version":1,"phases":[{"id":"ph-1","name":"write","order":1}]}}`,
			Valid:  true,
		},
	}
	require.NoError(t, store.CreateTask(plan))
	return plan
}

// TestPlanExecute_TriggerBootsOrchestratorSession is the unattended part
// of the S2 exit gate (CW-20260503-0021, S2.5). It validates the
// plan_start → orchestrator session boot path against a real agent.Manager
// + a fakeRuntime. AutoFireFirstTurn=true on the captured StartOptions is
// the kickoff-fire assertion (replaces the prior SendInput-fire tracking).
func TestPlanExecute_TriggerBootsOrchestratorSession(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan := createPlanTask(t, cd.Store, "CW-PLAN-SMOKE-001", t.TempDir())

	res, err := planstart.Start(ctx, cd.Store, cd.Manager, plan.ID, planstart.Options{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotEmpty(t, res.SessionID, "orchestrator session id should be returned")
	assert.Equal(t, plan.ID, res.PlanID)

	// AutoFireFirstTurn replaces the prior post-Launch SendInput drive.
	// fakeRuntime captures this on the recorded StartOptions.
	require.True(t, cd.Runtime.autoFireFirstTurn.Load(),
		"planstart.Start must trigger AutoFireFirstTurn=true on the orchestrator boot StartOptions")
	payload := cd.Runtime.firstTurnPayload.Load()
	require.NotNil(t, payload, "FirstTurnPayload must be non-empty")
	assert.NotEmpty(t, *payload)

	// Orchestrator session row exists in the manager + DB.
	sess, err := cd.Manager.Get(res.SessionID)
	require.NoError(t, err)
	assert.Equal(t, orchestrator.Profile, sess.AgentProfile)
	assert.Equal(t, plan.ID, sess.TaskID, "session.task_id binds to the plan")

	// Session metadata carries plan_id + role tag (S2.2 contract). Stamped
	// by planstart.Start into the SessionMeta map; recovered by Manager.Get
	// from the persisted MetaJSON.
	require.NotNil(t, sess.Meta)
	assert.Equal(t, plan.ID, sess.Meta[orchestrator.SessionMetaPlanID])
	assert.Equal(t, orchestrator.SessionMetaRoleValue, sess.Meta[orchestrator.SessionMetaRole])

	// Plan transitioned to doing; metadata.plan.orchestrator_session_id
	// stamped (substrate-side prerequisite for the orchestrator's walk).
	post, err := cd.Store.GetTask(plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "doing", post.Status, "plan must be in doing after start")
	require.True(t, post.Metadata.Valid)
	assert.Contains(t, post.Metadata.String, res.SessionID)
	assert.Contains(t, post.Metadata.String, "orchestrator_session_id")
	assert.Contains(t, post.Metadata.String, "orchestrator_started_at")

	// Pre-existing plan.phases survives the metadata write — the orchestrator
	// needs it to walk phases.
	assert.Contains(t, post.Metadata.String, `"phases"`)
}

// TestPlanExecute_IdempotentWhileLive validates AC5: calling plan_start
// while an orchestrator session is live returns ErrAlreadyOrchestrating
// wrapping a Result whose SessionID points at the existing session — the
// "Rerun" path the runbook documents.
func TestPlanExecute_IdempotentWhileLive(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan := createPlanTask(t, cd.Store, "CW-PLAN-SMOKE-002", t.TempDir())

	first, err := planstart.Start(ctx, cd.Store, cd.Manager, plan.ID, planstart.Options{})
	require.NoError(t, err)

	// Manually flip plan back to todo so we can re-attempt. In practice the
	// live-session check fires before the status check, but we want to
	// exercise that path explicitly.
	require.NoError(t, cd.Store.TransitionTask(plan.ID, "todo"))

	second, err := planstart.Start(ctx, cd.Store, cd.Manager, plan.ID, planstart.Options{})
	require.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, second)
	assert.Equal(t, first.SessionID, second.SessionID,
		"second start surfaces the live session id")
}

// TestPlanExecute_RejectsInvalidTargets confirms wrong-kind / missing /
// terminal-status plans are rejected with their dedicated sentinels —
// backstop for the runbook's failure-mode notes.
func TestPlanExecute_RejectsInvalidTargets(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")
	ctx := context.Background()

	// Wrong kind.
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-NOT-PLAN", Title: "x", Status: "todo", Kind: "agent",
		Executor: "cli", AgentProfile: "torque-backend",
	}))
	_, err := planstart.Start(ctx, cd.Store, cd.Manager, "CW-NOT-PLAN", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)

	// Missing.
	_, err = planstart.Start(ctx, cd.Store, cd.Manager, "CW-NOPE", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)

	// Wrong status (terminal).
	plan := createPlanTask(t, cd.Store, "CW-PLAN-DONE", t.TempDir())
	require.NoError(t, cd.Store.TransitionTask(plan.ID, "doing"))
	require.NoError(t, cd.Store.TransitionTask(plan.ID, "review"))
	require.NoError(t, cd.Store.TransitionTask(plan.ID, "done"))
	_, err = planstart.Start(ctx, cd.Store, cd.Manager, plan.ID, planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanWrongStatus)
}
