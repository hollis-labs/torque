package bootstrap_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

// planMetadata builds a metadata blob with the plan namespace + an
// orchestrator_session_id, mirroring what planstart.Start stamps.
func planMetadataWithOrchestrator(sessionID string) string {
	b, _ := json.Marshal(map[string]any{
		"plan": map[string]any{
			"orchestrator_session_id": sessionID,
		},
	})
	return string(b)
}

// readCheckpointResponses extracts metadata.checkpoint_responses from a task.
func readCheckpointResponses(t *testing.T, store *sqlstore.Store, taskID string) map[string]any {
	t.Helper()
	rec, err := store.GetTask(taskID)
	require.NoError(t, err)
	if !rec.Metadata.Valid || rec.Metadata.String == "" {
		return nil
	}
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(rec.Metadata.String), &md))
	cr, _ := md["checkpoint_responses"].(map[string]any)
	return cr
}

// TestOrchestratorRedispatcher_NoPlanAncestor is the cheap no-op case: a
// checkpoint task with no kind=plan ancestor must not error and must not
// touch anything.
func TestOrchestratorRedispatcher_NoPlanAncestor(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)
	d := bootstrap.NewOrchestratorRedispatcher(store, deps.Sessions)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-STANDALONE", Title: "standalone", Status: "review", Kind: "agent",
	}))

	err := d.RedispatchForCheckpointResponse(context.Background(), service.OrchestratorRedispatch{
		TaskID:        "CW-STANDALONE",
		CorrelationID: "01HK_CORR_A",
		ResponseJSON:  `{"decision":"approve"}`,
	})
	require.NoError(t, err, "a task with no plan ancestor is a clean no-op")

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		Types: []string{"checkpoint.orchestrator_redispatched"},
	})
	require.NoError(t, err)
	assert.Empty(t, events, "no plan ancestor → no redispatch breadcrumb")
}

// TestOrchestratorRedispatcher_PlanWithoutOrchestratorSession: the plan
// exists above the child but was never walked by an orchestrator session
// (no metadata.plan.orchestrator_session_id). No-op, no error.
func TestOrchestratorRedispatcher_PlanWithoutOrchestratorSession(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)
	d := bootstrap.NewOrchestratorRedispatcher(store, deps.Sessions)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PLAN-NOSESS", Title: "plan", Status: "doing", Kind: "plan",
		WorkingDir: t.TempDir(),
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CHILD-NOSESS", Title: "child", Status: "review", Kind: "agent",
		ParentID: sql.NullString{String: "CW-PLAN-NOSESS", Valid: true},
	}))

	err := d.RedispatchForCheckpointResponse(context.Background(), service.OrchestratorRedispatch{
		TaskID:        "CW-CHILD-NOSESS",
		CorrelationID: "01HK_CORR_B",
		ResponseJSON:  `{"decision":"approve"}`,
	})
	require.NoError(t, err, "plan with no orchestrator session is a clean no-op")
}

// TestOrchestratorRedispatcher_ChildCheckpoint_PropagatesAndRedispatches is
// the core regression for the live bug (CW-20260518): a pr_review checkpoint
// emitted on a CHILD task is responded; the redispatcher must
//
//  1. copy the response onto the PARENT PLAN's metadata.checkpoint_responses
//     (where the orchestrator's redispatch-preflight looks), and
//  2. attempt to re-boot the exited orchestrator session.
//
// The Boot fails here (the test composition has no executor runtime) — but
// the propagation must already have happened (it runs before the boot) and
// a breadcrumb must land recording the redispatch attempt + error. Before
// the fix, NOTHING propagated the response to the plan and NOTHING tried to
// redispatch the orchestrator — the plan stalled forever.
func TestOrchestratorRedispatcher_ChildCheckpoint_PropagatesAndRedispatches(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)
	d := bootstrap.NewOrchestratorRedispatcher(store, deps.Sessions)

	// An orchestrator session that has already exited (state=done) — the
	// "[system/orchestrator/session-complete]" exit the bug describes.
	const orchSessID = "SES-ORCH-EXITED"
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           orchSessID,
		AgentProfile: "orchestrator",
		Provider:     "claude",
		RuntimeID:    "torque-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      t.TempDir(),
		State:        "done",
		TaskID:       sql.NullString{String: "CW-PLAN-0038", Valid: true},
	}))

	// Plan task — mid-walk (status=doing), orchestrator session id stamped.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PLAN-0038", Title: "plan", Status: "doing", Kind: "plan",
		WorkingDir: t.TempDir(),
		Metadata:   sql.NullString{String: planMetadataWithOrchestrator(orchSessID), Valid: true},
	}))
	// Child task at review — the reviewer end-agent emitted a pr_review
	// checkpoint here. Note it is NOT "parked" on the correlation (Emit's
	// ParkTaskOnCheckpoint only fires on status=doing); that is exactly why
	// the parked-gated dispatch path could not redispatch the orchestrator.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CHILD-0040", Title: "child", Status: "review", Kind: "agent",
		ParentID: sql.NullString{String: "CW-PLAN-0038", Valid: true},
	}))

	// RedispatchForCheckpointResponse propagates the response onto the plan
	// and re-boots the orchestrator. Whether the inner Boot completes
	// depends on the test runtime's executor availability — the contract
	// this test pins is the bug-relevant pair: (1) the response reaches the
	// PLAN's metadata, and (2) a redispatch was attempted (breadcrumb).
	_ = d.RedispatchForCheckpointResponse(context.Background(), service.OrchestratorRedispatch{
		TaskID:        "CW-CHILD-0040",
		CorrelationID: "01HK_CORR_15",
		ResponseJSON:  `{"decision":"approve"}`,
	})

	// (1) The response was propagated onto the PLAN task's metadata — the
	// orchestrator's redispatch-preflight reads exactly this key. In the
	// live bug this was null because the response only ever landed on the
	// CHILD task.
	planResponses := readCheckpointResponses(t, store, "CW-PLAN-0038")
	require.NotNil(t, planResponses, "plan.metadata.checkpoint_responses must be populated (was null in the bug)")
	resp, ok := planResponses["01HK_CORR_15"].(map[string]any)
	require.True(t, ok, "response must be stored structurally under the correlation_id")
	assert.Equal(t, "approve", resp["decision"])

	// (2) A redispatch breadcrumb landed on the plan recording the attempt.
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		TaskID: "CW-PLAN-0038",
		Types:  []string{"checkpoint.orchestrator_redispatched"},
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one redispatch breadcrumb per response")

	var payload struct {
		CorrelationID    string `json:"correlation_id"`
		CheckpointTaskID string `json:"checkpoint_task_id"`
		PlanID           string `json:"plan_id"`
		PriorSessionID   string `json:"prior_session_id"`
		NewSessionID     string `json:"new_session_id"`
		AlreadyLive      bool   `json:"already_live"`
		Error            string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &payload))
	assert.Equal(t, "01HK_CORR_15", payload.CorrelationID)
	assert.Equal(t, "CW-CHILD-0040", payload.CheckpointTaskID)
	assert.Equal(t, "CW-PLAN-0038", payload.PlanID)
	assert.Equal(t, orchSessID, payload.PriorSessionID)
	assert.False(t, payload.AlreadyLive, "the prior orchestrator session was exited, not live")
	// Either the re-boot produced a new session id, or it failed and the
	// error is recorded — both are valid; what matters is a redispatch was
	// attempted (before the fix, nothing was).
	assert.True(t, payload.NewSessionID != "" || payload.Error != "",
		"breadcrumb records either a new session id or the boot error")

	// When the re-boot succeeded, the plan must now point at the NEW
	// orchestrator session, not the dead one.
	if payload.NewSessionID != "" {
		plan, err := store.GetTask("CW-PLAN-0038")
		require.NoError(t, err)
		var md map[string]any
		require.NoError(t, json.Unmarshal([]byte(plan.Metadata.String), &md))
		planNS, _ := md["plan"].(map[string]any)
		assert.Equal(t, payload.NewSessionID, planNS["orchestrator_session_id"],
			"plan must be re-stamped with the redispatched orchestrator session id")
	}
}

// TestOrchestratorRedispatcher_LiveOrchestrator_IsIdempotentNoOp: when the
// orchestrator session is still live, the redispatcher must not boot a
// second walker — it just propagates the response (the live orchestrator
// picks it up on its own preflight poll) and records an already_live
// breadcrumb.
func TestOrchestratorRedispatcher_LiveOrchestrator_IsIdempotentNoOp(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)
	d := bootstrap.NewOrchestratorRedispatcher(store, deps.Sessions)

	const orchSessID = "SES-ORCH-LIVE"
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           orchSessID,
		AgentProfile: "orchestrator",
		Provider:     "claude",
		RuntimeID:    "torque-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      t.TempDir(),
		State:        "running", // still live
		TaskID:       sql.NullString{String: "CW-PLAN-LIVE", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PLAN-LIVE", Title: "plan", Status: "doing", Kind: "plan",
		WorkingDir: t.TempDir(),
		Metadata:   sql.NullString{String: planMetadataWithOrchestrator(orchSessID), Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CHILD-LIVE", Title: "child", Status: "review", Kind: "agent",
		ParentID: sql.NullString{String: "CW-PLAN-LIVE", Valid: true},
	}))

	err := d.RedispatchForCheckpointResponse(context.Background(), service.OrchestratorRedispatch{
		TaskID:        "CW-CHILD-LIVE",
		CorrelationID: "01HK_CORR_LIVE",
		ResponseJSON:  `{"decision":"approve"}`,
	})
	require.NoError(t, err, "a live orchestrator needs no re-boot — idempotent no-op")

	// Response still propagated so the live orchestrator's preflight sees it.
	planResponses := readCheckpointResponses(t, store, "CW-PLAN-LIVE")
	require.NotNil(t, planResponses)
	assert.Contains(t, planResponses, "01HK_CORR_LIVE")

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		TaskID: "CW-PLAN-LIVE",
		Types:  []string{"checkpoint.orchestrator_redispatched"},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	var payload struct {
		AlreadyLive bool `json:"already_live"`
	}
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &payload))
	assert.True(t, payload.AlreadyLive, "live orchestrator → already_live breadcrumb, no second boot")
}
