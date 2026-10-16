package agent_boot

import (
	"errors"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

// Session launch now persists a real run FK; fixtures must model dispatch
// instead of supplying an arbitrary integer used only for the stderr path.
func createBootRun(t *testing.T, store *sqlstore.Store, taskID string) int64 {
	t.Helper()
	_, err := store.GetTask(taskID)
	if errors.Is(err, sqlstore.ErrTaskNotFound) {
		err = store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: taskID, Executor: "cli", Status: "doing"})
	}
	require.NoError(t, err)
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cli", Status: "running"})
	require.NoError(t, err)
	return id
}
