package sqlstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/tetherexec"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

func TestBindRunTether_FillsAndKeepsColumns(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20261030-0210")
	require.NoError(t, store.CreateTask(task))
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "tether"})
	require.NoError(t, err)

	b := tetherexec.StoreBinder{Writer: writeq.NewDirect(store)}
	ctx := context.Background()
	require.NoError(t, b.BindRun(ctx, id, "sess-1", "general-worker", ""))
	r, err := store.GetRun(id)
	require.NoError(t, err)
	require.Equal(t, "sess-1", r.TetherSessionID.String)
	require.Equal(t, "general-worker", r.LaunchID.String)
	require.False(t, r.WorkstreamID.Valid)

	// Workstream arrives after launch; empty fields keep stored values.
	require.NoError(t, b.BindRun(ctx, id, "sess-1", "", "ws-9"))
	r, err = store.GetRun(id)
	require.NoError(t, err)
	require.Equal(t, "general-worker", r.LaunchID.String)
	require.Equal(t, "ws-9", r.WorkstreamID.String)

	require.Error(t, b.BindRun(ctx, 999999, "s", "l", "w"), "unknown run")
}
