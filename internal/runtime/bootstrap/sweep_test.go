package bootstrap_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
)

// Only the daemon that owns sessions sweeps them (CW-20261001-0141).
// AgentDeps, which `torque mcp` builds too, leaves a session whose process
// is gone as it is: a `torque mcp` that mux ran inside an agent's sandbox
// would see every live session as dead. SweepOrphanSessions, which `torque
// serve` runs at startup, marks it crashed.
func TestAgentDepsDoesNotSweep(t *testing.T) {
	store := bootstrapStore(t)
	exited := exec.Command("true")
	require.NoError(t, exited.Run())
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "SES-GONE", State: "running", PID: exited.Process.Pid}))

	deps, closeDeps, err := bootstrap.AgentDeps(store, nil, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(closeDeps)
	sess, err := store.GetSession("SES-GONE")
	require.NoError(t, err)
	assert.Equal(t, "running", sess.State, "building agent deps sweeps nothing")

	require.NoError(t, bootstrap.SweepOrphanSessions(deps, nil))
	sess, err = store.GetSession("SES-GONE")
	require.NoError(t, err)
	assert.Equal(t, "crashed", sess.State)
}
