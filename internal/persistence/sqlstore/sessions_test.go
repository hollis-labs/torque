package sqlstore_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessions_CreateAndGet(t *testing.T) {
	store := setupTestStore(t)
	rec := &sqlstore.SessionRecord{
		ID:           "S-1",
		AgentProfile: "default",
		Provider:     "claude",
		Workdir:      "/tmp",
	}
	require.NoError(t, store.CreateSession(rec))

	got, err := store.GetSession("S-1")
	require.NoError(t, err)
	assert.Equal(t, "S-1", got.ID)
	assert.Equal(t, "launching", got.State)
	assert.Equal(t, "{}", got.MetaJSON)
}

func TestSessions_UpdateState_Terminal(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-2"}))

	exit := 0
	require.NoError(t, store.UpdateSessionState("S-2", "done", 1234, &exit))
	got, err := store.GetSession("S-2")
	require.NoError(t, err)
	assert.Equal(t, "done", got.State)
	assert.Equal(t, 1234, got.PID)
	require.True(t, got.ExitCode.Valid)
	assert.Equal(t, int64(0), got.ExitCode.Int64)
	require.True(t, got.EndedAt.Valid, "ended_at should populate on terminal transitions")
}

func TestSessions_NotFoundReturnsSentinel(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.GetSession("missing")
	assert.ErrorIs(t, err, sqlstore.ErrSessionNotFound)

	err = store.UpdateSessionState("missing", "running", 0, nil)
	assert.ErrorIs(t, err, sqlstore.ErrSessionNotFound)
}

func TestSessions_Sweep_MarksLaunchingAndRunning(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-launch"}))
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-run"}))
	require.NoError(t, store.UpdateSessionState("S-run", "running", 0, nil))
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-done"}))
	exit := 0
	require.NoError(t, store.UpdateSessionState("S-done", "done", 0, &exit))

	n, err := store.SweepStaleSessions(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	got, _ := store.GetSession("S-launch")
	assert.Equal(t, "crashed", got.State)
	got, _ = store.GetSession("S-run")
	assert.Equal(t, "crashed", got.State)
	got, _ = store.GetSession("S-done")
	assert.Equal(t, "done", got.State)
}

func TestSessions_Sweep_SparesCallback(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-keep"}))
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-kill"}))

	n, err := store.SweepStaleSessions(func(rec *sqlstore.SessionRecord) bool {
		return rec.ID == "S-keep"
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	keep, _ := store.GetSession("S-keep")
	assert.Equal(t, "launching", keep.State)
	kill, _ := store.GetSession("S-kill")
	assert.Equal(t, "crashed", kill.State)
}

func TestSessionCheckpoints_CreateAndList(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "S-cp"}))

	require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{
		ID:        "C-1",
		SessionID: "S-cp",
		Payload:   `{"x":1}`,
		Note:      "first",
	}))
	require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{
		ID:        "C-2",
		SessionID: "S-cp",
		Payload:   `{"x":2}`,
	}))

	cps, err := store.ListSessionCheckpoints("S-cp", 0)
	require.NoError(t, err)
	assert.Len(t, cps, 2)

	latest, err := store.LatestSessionCheckpoint("S-cp")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "C-2", latest.ID)

	none, err := store.LatestSessionCheckpoint("missing")
	require.NoError(t, err)
	assert.Nil(t, none)
}
