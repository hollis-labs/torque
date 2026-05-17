package agent_boot

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// plantSessionForResume inserts a sessions row with an already-captured
// provider session-id in the resume_hint column — the shape ResumeSession
// reads on input. Models the post-first-turn state: the original Boot ran,
// the adapter emitted its `init` event, the OnSessionID callback persisted
// the provider session-id, and now we're resuming after that session's
// process exited (or a daemon restart, etc).
func plantSessionForResume(t *testing.T, store *sqlstore.Store, sessID, provider, providerSessionID, workdir, agentProfile string) {
	t.Helper()
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: agentProfile,
		Provider:     provider,
		RuntimeID:    "torque-cli/" + provider,
		RuntimeKind:  "cli",
		Workdir:      workdir,
		State:        "done",
		MetaJSON:     `{"role":"orchestrator"}`,
	}))
	// resume_hint is NOT written by CreateSession (its INSERT statement
	// only stamps the launching-time columns). The OnSessionID callback
	// in production sets it via UpdateSessionResumeHint mid-turn; the
	// test plants it the same way so ResumeSession sees a populated
	// column on GetSession.
	require.NoError(t, store.UpdateSessionResumeHint(sessID, []byte(providerSessionID)))
}

// TestResumeSession_Claude_ThreadsProviderSessionID is the load-bearing test
// for sprint α.2: ResumeSession reads the persisted provider session-id off
// the sessions row and threads it into StartOptions.SessionIDPreset so the
// claude adapter prepends `--resume <id>` on the first turn.
func TestResumeSession_Claude_ThreadsProviderSessionID(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	const providerSessionID = "claude-session-aaaa-bbbb-cccc"
	plantSessionForResume(t, cd.Store, "SES-RESUME-CLAUDE", "claude", providerSessionID, t.TempDir(), "torque-backend")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-CLAUDE", agent.ResumeOptions{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotEqual(t, "SES-RESUME-CLAUDE", sess.ID,
		"ResumeSession must allocate a fresh session ID; original row is unchanged")
	assert.Equal(t, agent.ModeLongLived, sess.Mode,
		"ResumeSession boots in long-lived mode; state-based path is orthogonal to ModeResume")

	preset := cd.Runtime.sessionIDPreset.Load()
	require.NotNil(t, preset, "ResumeSession (SupportsResume=true) must populate StartOptions.SessionIDPreset")
	assert.Equal(t, providerSessionID, *preset,
		"ResumeSession must thread the persisted resume_hint bytes into StartOptions.SessionIDPreset")

	// AgentProfile + Workdir rehydrated from the original row; the new
	// session row's Provider comes from the resolved agent profile
	// (torque-backend → claude-code), not the planted row's value.
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "torque-backend", rec.AgentProfile)
	assert.Equal(t, "claude-code", rec.Provider)
}

// TestResumeSession_Codex_ThreadsProviderSessionID covers the second
// SupportsResume=true provider. The codex adapter in go-providers v0.16.1
// does not yet thread cliSessionID into argv (its BuildArgs ignores the
// parameter — "Resume is interactive-only in Codex" per the adapter
// comment), but the substrate's job is to feed SessionIDPreset based on
// the declared capability; per-adapter argv wiring is the go-providers
// codex adapter's concern. This test pins the substrate behavior so when
// go-providers ships codex resume support, no torque-side change is
// needed.
func TestResumeSession_Codex_ThreadsProviderSessionID(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "codex")

	const providerSessionID = "codex-thread-id-xyz-789"
	plantSessionForResume(t, cd.Store, "SES-RESUME-CODEX", "codex", providerSessionID, t.TempDir(), "torque-backend")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-CODEX", agent.ResumeOptions{})
	require.NoError(t, err)
	require.NotNil(t, sess)

	preset := cd.Runtime.sessionIDPreset.Load()
	require.NotNil(t, preset, "ResumeSession (codex, SupportsResume=true) must populate SessionIDPreset")
	assert.Equal(t, providerSessionID, *preset)
}

// TestResumeSession_Opencode_FreshBoot exercises the SupportsResume=false
// fallback: opencode has no native --resume primitive, so ResumeSession
// must boot fresh — same AgentProfile + Workdir + ProjectID + TaskID
// rehydration, but no SessionIDPreset on StartOptions.
func TestResumeSession_Opencode_FreshBoot(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "opencode")

	plantSessionForResume(t, cd.Store, "SES-RESUME-OPENCODE", "opencode", "ignored-session-id", t.TempDir(), "torque-backend")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-OPENCODE", agent.ResumeOptions{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, agent.ModeLongLived, sess.Mode)

	preset := cd.Runtime.sessionIDPreset.Load()
	assert.Nil(t, preset,
		"ResumeSession (opencode, SupportsResume=false) must NOT populate SessionIDPreset — fresh-boot fallback only")
}

// TestResumeSession_UnknownSessionID surfaces the typed not-found error so
// callers (reactor harness) can branch on errors.Is(ErrSessionNotFound).
func TestResumeSession_UnknownSessionID(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cd.Manager.ResumeSession(ctx, "SES-DOES-NOT-EXIST", agent.ResumeOptions{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, agent.ErrSessionNotFound),
		"unknown sessionID must surface ErrSessionNotFound via errors.Is")
}

// TestResumeSession_EmptySessionID rejects the bad input before touching
// the store.
func TestResumeSession_EmptySessionID(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	_, err := cd.Manager.ResumeSession(context.Background(), "", agent.ResumeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionID required")
}

// TestResumeSession_DiagnosticNote_FlowsIntoSystemPrompt covers the α.5
// stuck-task recovery shape: DiagnosticNote becomes the prepended framing
// in the resumed transcript's system prompt. The fakeRuntime records the
// composed BootPrompt verbatim on StartOptions; we assert the note shows
// up there. Without this contract, α.5's "the previous turn went silent"
// framing would never reach the LLM.
func TestResumeSession_DiagnosticNote_FlowsIntoSystemPrompt(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	plantSessionForResume(t, cd.Store, "SES-RESUME-DIAG", "claude", "claude-session-diag", t.TempDir(), "torque-backend")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const note = "Stuck-task probe fired; previous turn was silent for 90s."
	_, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-DIAG", agent.ResumeOptions{
		DiagnosticNote: note,
	})
	require.NoError(t, err)

	snap := cd.Runtime.lastStartOpts.Load()
	require.NotNil(t, snap, "fakeRuntime must have captured StartOptions on Start")
	assert.Contains(t, snap.BootPrompt, note,
		"DiagnosticNote must be prepended into the composed system prompt (StartOptions.BootPrompt)")
}

// TestResumeSession_FreshBoot_RehydratesFields covers the non-resume
// branch's rehydration contract: even when SupportsResume=false (fresh
// boot), the persisted AgentProfile + Workdir + ProjectID + TaskID + role
// must rehydrate so the new boot is the same shape as the original. The
// only difference vs the original boot is the missing SessionIDPreset.
func TestResumeSession_FreshBoot_RehydratesFields(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "opencode")

	workdir := t.TempDir()
	require.NoError(t, cd.Store.CreateSession(&sqlstore.SessionRecord{
		ID:           "SES-FRESH-REHY",
		AgentProfile: "torque-backend",
		Provider:     "opencode",
		RuntimeID:    "torque-cli/opencode",
		RuntimeKind:  "cli",
		Workdir:      workdir,
		ProjectID:    sql.NullString{String: "PROJ-7", Valid: true},
		TaskID:       sql.NullString{String: "CW-TASK-42", Valid: true},
		State:        "done",
		MetaJSON:     `{"role":"orchestrator","plan_id":"CW-PLAN-9"}`,
		// ResumeHint intentionally empty: opencode's adapter doesn't
		// expose a provider session-id, so the original boot never
		// stamped one. Fresh-boot is the only correct branch.
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.ResumeSession(ctx, "SES-FRESH-REHY", agent.ResumeOptions{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "torque-backend", sess.AgentProfile)
	assert.Equal(t, "opencode", sess.Provider)
	assert.Equal(t, workdir, sess.Workdir,
		"Workdir is the agent.Boot input; the recorded spawn cwd may differ (provider-specific plant) — Session.Workdir reflects the spawn cwd, not the input")
	assert.Equal(t, "PROJ-7", sess.ProjectID,
		"ProjectID must rehydrate from the original row")
	assert.Equal(t, "CW-TASK-42", sess.TaskID,
		"TaskID must rehydrate from the original row")

	preset := cd.Runtime.sessionIDPreset.Load()
	assert.Nil(t, preset, "opencode fresh-boot must NOT populate SessionIDPreset")
}

// TestResumeSession_StatePersistence_OnSessionID_FlowsToResumeHint covers
// the substrate-side state-capture contract that makes resume work
// end-to-end: when a claude adapter emits its `init` event with a
// session-id, the OnSessionID callback persists it into the sessions row's
// resume_hint column. ResumeSession then reads that column on the next
// invocation. Without this loop, ResumeSession would have nothing to
// resume from on the first resume of a session.
func TestResumeSession_StatePersistence_OnSessionID_FlowsToResumeHint(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First boot — captures the provider session-id via OnSessionID.
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-STATE-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Simulate the lib's OnSessionID callback firing once the adapter
	// emits its init event (claude's `{"type":"init","session_id":"<id>"}`).
	last := cd.Runtime.lastSession()
	require.NotNil(t, last, "fakeRuntime must have minted a fakeSession on Boot")
	const providerSessionID = "claude-session-end-to-end-xyz"
	last.triggerSessionID(providerSessionID)

	// The Manager's OnSessionID closure calls UpdateSessionResumeHint
	// synchronously; the row should reflect the new value immediately.
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte(providerSessionID), rec.ResumeHint,
		"OnSessionID must persist the provider session-id into the row's resume_hint column for future ResumeSession calls")

	// Resume the same session — ResumeSession reads the row and threads
	// the captured hint back to StartOptions.SessionIDPreset.
	resumed, err := cd.Manager.ResumeSession(ctx, sess.ID, agent.ResumeOptions{})
	require.NoError(t, err)
	require.NotNil(t, resumed)

	preset := cd.Runtime.sessionIDPreset.Load()
	require.NotNil(t, preset, "ResumeSession must thread the captured hint into SessionIDPreset")
	assert.Equal(t, providerSessionID, *preset,
		"end-to-end: OnSessionID → row.resume_hint → ResumeSession → SessionIDPreset")
}
