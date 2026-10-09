package bootstrap_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// Sprint α.4 (CW-20260512-0062): when no session row is bound to the
// parked task, the dispatcher returns ErrNoLiveSessionForTask so
// CheckpointService.Respond falls back to the legacy fresh-boot path
// (review → todo + scheduler tick). This is the expected branch for
// one-shot executor tasks whose worker never registered with the
// long-lived session manager (or tasks whose session row never landed —
// e.g. crashed during launch before persistence).
func TestCheckpointResponseDispatcher_NoSessionRow_ReturnsSentinel(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t), Store: store}
	deps.Sessions = agent.NewManager(deps)

	d := bootstrap.NewCheckpointResponseDispatcher(store, deps.Sessions)

	err := d.DispatchResponse(context.Background(), service.CheckpointResponseDispatch{
		TaskID:        "CW-NOPE",
		CorrelationID: "01HK000000000000000000000",
		ResponseJSON:  `{"answer":"y"}`,
	})
	assert.ErrorIs(t, err, service.ErrNoLiveSessionForTask,
		"task with no session row returns the sentinel so Respond falls back to legacy todo")
}

// Nil store + nil sessions degrade to the sentinel too — defensive
// fallback so a partially-wired daemon can't strand a task in review.
func TestCheckpointResponseDispatcher_NilDeps_ReturnsSentinel(t *testing.T) {
	d := bootstrap.NewCheckpointResponseDispatcher(nil, nil)
	err := d.DispatchResponse(context.Background(), service.CheckpointResponseDispatch{
		TaskID: "CW-X", CorrelationID: "01HK", ResponseJSON: "{}",
	})
	assert.ErrorIs(t, err, service.ErrNoLiveSessionForTask)
}

// When a session row exists for the task, DispatchResponse must call
// agent.Manager.ResumeSession (proven by the resulting error path — the
// real Manager will try to Boot and fail because the test composition
// has no executor wiring) and emit a breadcrumb. We assert:
//   - the returned error wraps the resume failure ("α.4 HITL resume:")
//   - a run_events row of type=checkpoint.response_dispatched landed for
//     the task with the expected payload shape (original_session_id, path,
//     correlation_id, used_resume reflecting the provider capability).
//
// This is the wired-but-unhappy path — the dispatch decision fired
// correctly, but the downstream Boot couldn't complete because the test
// runtime has no real adapter. That's exactly what we want to validate:
// the dispatcher's contract holds even when the inner ResumeSession
// fails, so postmortem queries can still see the chain.
func TestCheckpointResponseDispatcher_SessionPresent_EmitsBreadcrumbAndAttemptsResume(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t), Store: store}
	deps.Sessions = agent.NewManager(deps)
	d := bootstrap.NewCheckpointResponseDispatcher(store, deps.Sessions)

	// Plant a sessions row that DispatchResponse will pick up. The row
	// doesn't need to be live — ResumeSession reads AgentProfile +
	// Workdir + TaskID + ResumeHint off the persisted row regardless of
	// state. No profile is configured, so the session's "default" profile
	// boots no runtime: ResumeSession boots fresh (and fails), and the
	// breadcrumb's used_resume says so.
	const sessID = "SES-TEST-ALPHA4"
	rec := &sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: "default",
		Provider:     "claude",
		RuntimeID:    "torque-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      t.TempDir(),
		State:        "done", // terminal — but α.2's ResumeSession works on persisted state
		ResumeHint:   []byte("prov-sess-xyz"),
		TaskID:       sql.NullString{String: "CW-ALPHA4-T1", Valid: true},
	}
	require.NoError(t, store.CreateSession(rec))
	// CreateSession does not write resume_hint; the OnSessionID callback
	// does, through UpdateSessionResumeHint.
	require.NoError(t, store.UpdateSessionResumeHint(sessID, []byte("prov-sess-xyz")))

	err := d.DispatchResponse(context.Background(), service.CheckpointResponseDispatch{
		TaskID:        "CW-ALPHA4-T1",
		CorrelationID: "01HK_ALPHA4_CORR",
		ResponseJSON:  `{"answer":"go"}`,
	})
	// The resume attempt fails (no executor wiring) but the dispatcher
	// path was taken — that's the contract this test asserts.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "α.4 HITL resume",
		"resume failure must surface through the dispatcher's error wrap so the service-layer fallback path triggers")

	// Breadcrumb landed even on failure — observability is unconditional.
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		TaskID: "CW-ALPHA4-T1",
		Types:  []string{"checkpoint.response_dispatched"},
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one breadcrumb per dispatch attempt")

	var payload struct {
		Path                string `json:"path"`
		CorrelationID       string `json:"correlation_id"`
		OriginalSessionID   string `json:"original_session_id"`
		NewSessionID        string `json:"new_session_id"`
		Provider            string `json:"provider"`
		UsedResume          bool   `json:"used_resume"`
		OperatorResponseLen int    `json:"operator_response_len"`
		Error               string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &payload))
	assert.Equal(t, "alpha4", payload.Path,
		"breadcrumb path tag is the postmortem-grep handle for the α.4 rewire")
	assert.Equal(t, "01HK_ALPHA4_CORR", payload.CorrelationID)
	assert.Equal(t, sessID, payload.OriginalSessionID)
	assert.Equal(t, "claude", payload.Provider)
	assert.False(t, payload.UsedResume,
		"used_resume reflects Manager.ResumesSession: the profile boots no runtime, so no resume; a runtime probe is NOT used per D4")
	assert.Equal(t, len(`{"answer":"go"}`), payload.OperatorResponseLen)
	assert.NotEmpty(t, payload.Error, "failed resume must record the error on the breadcrumb")
}

// With a profile whose runtime Torque genuinely resumes (claude-code,
// streaming-stdio) and a stored provider session id, the breadcrumb records
// used_resume=true: the same decision ResumeSession made (CW-20261001-0174).
// The row's provider is the bare "claude" alias, the same runtime.
func TestCheckpointResponseDispatcher_GenuineResume_RecordsUsedResume(t *testing.T) {
	// A fake claude CLI that lives long enough for Boot to succeed: the agent
	// CLI shim exits at once, and whether Boot then returned a session or an
	// error was a race, which made used_resume (read off the new session)
	// flaky. Replaying a captured resumed stream keeps the session up.
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	fake.ExpectErrors() // the dispatcher's own turn is not part of the capture
	fake.Install()
	store := sqlitetest.OpenStore(t)
	deps := &agent.Dependencies{
		Store:          store,
		WorkspacesRoot: t.TempDir(),
		Profiles:       config.ProfileMap{"default": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}},
	}
	deps.Sessions = agent.NewManager(deps)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = deps.Sessions.Shutdown(ctx)
	})
	d := bootstrap.NewCheckpointResponseDispatcher(store, deps.Sessions)

	const sessID = "SES-TEST-ALPHA4-RESUME"
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID: sessID, AgentProfile: "default", Provider: "claude", RuntimeKind: "streaming-stdio",
		Workdir: t.TempDir(), State: "done",
		TaskID: sql.NullString{String: "CW-ALPHA4-T2", Valid: true},
	}))
	require.NoError(t, store.UpdateSessionResumeHint(sessID, []byte("prov-sess-xyz")))

	// Whether the dispatch's own turn succeeds does not matter here.
	_ = d.DispatchResponse(context.Background(), service.CheckpointResponseDispatch{
		TaskID: "CW-ALPHA4-T2", CorrelationID: "01HK_ALPHA4_RESUME", ResponseJSON: `{"answer":"go"}`,
	})

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-ALPHA4-T2", Types: []string{"checkpoint.response_dispatched"}})
	require.NoError(t, err)
	require.Len(t, events, 1)
	var payload struct {
		UsedResume bool `json:"used_resume"`
	}
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &payload))
	assert.True(t, payload.UsedResume, "claude-code resumes, and the row has a stored provider session id")
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 10*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "the resume reached the CLI: %v", fake.Call(0).Args)
	assert.Equal(t, "prov-sess-xyz", got)
}
