package agent

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconcileTerminalRow_NonTerminalRowGetsMarkedFailed pins the
// CW-20260519-0082 defensive write: when the lib's recordState dropped a
// terminal-state StateSink write on the floor (manager.go:272 silently
// swallows the error), the pid_poller's exit path must reconcile the
// row by forcing it to `failed`. Without this, the row stays `running`
// forever and the operator's /plans/start hits 409 against a phantom
// session.
func TestReconcileTerminalRow_NonTerminalRowGetsMarkedFailed(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &Dependencies{Store: store}
	deps.Sessions = NewManager(deps)

	const sessID = "SES-STALE-RECONCILE"
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: "orchestrator",
		Provider:     "claude",
		RuntimeID:    "torque-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      t.TempDir(),
		State:        string(StatusRunning),
	}))

	reconcileTerminalRow(deps.Sessions, sessID)

	rec, err := store.GetSession(sessID)
	require.NoError(t, err)
	assert.Equal(t, string(StatusFailed), rec.State,
		"non-terminal row must be reconciled to failed after lib-side unregistration")
	require.True(t, rec.ExitCode.Valid, "exit_code must be populated on the reconciled row")
	assert.Equal(t, int64(-1), rec.ExitCode.Int64,
		"reconciled rows use exit_code=-1 (no real exit code available)")
}

// TestReconcileTerminalRow_TerminalRowIsLeftAlone: idempotency. A row
// already in done/failed/crashed must not be flipped by reconcile.
// Defensive write is a fallback, not a normalizer.
func TestReconcileTerminalRow_TerminalRowIsLeftAlone(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &Dependencies{Store: store}
	deps.Sessions = NewManager(deps)

	terminalStates := []Status{StatusDone, StatusFailed, StatusCrashed}
	for _, want := range terminalStates {
		t.Run(string(want), func(t *testing.T) {
			sessID := "SES-TERMINAL-" + string(want)
			require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
				ID:           sessID,
				AgentProfile: "orchestrator",
				Provider:     "claude",
				RuntimeID:    "torque-cli/claude",
				RuntimeKind:  "cli",
				Workdir:      t.TempDir(),
				State:        string(want),
			}))
			reconcileTerminalRow(deps.Sessions, sessID)
			rec, err := store.GetSession(sessID)
			require.NoError(t, err)
			assert.Equal(t, string(want), rec.State,
				"terminal row must not be re-written by reconcile (idempotency)")
		})
	}
}

// TestReconcileTerminalRow_MissingSessionIsNoOp: when the row has been
// deleted (or never existed), reconcile must not panic or write a row.
func TestReconcileTerminalRow_MissingSessionIsNoOp(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	deps := &Dependencies{Store: store}
	deps.Sessions = NewManager(deps)

	// Should not panic and should not produce a session row out of thin air.
	reconcileTerminalRow(deps.Sessions, "SES-NEVER-EXISTED")

	_, err := store.GetSession("SES-NEVER-EXISTED")
	assert.ErrorIs(t, err, sqlstore.ErrSessionNotFound,
		"reconcile must not synthesize a row for an unknown session id")
}
