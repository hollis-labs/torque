package agent

import (
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
	"testing"
)

func createLaunchTestRun(t *testing.T, store *sqlstore.Store, taskID string) int64 {
	t.Helper()
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cli", Status: "running"})
	require.NoError(t, err)
	return id
}
