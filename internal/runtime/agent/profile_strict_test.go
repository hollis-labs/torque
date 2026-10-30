package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// CW-20261030-0207: an unknown profile is a permanent, named failure, not a
// zero profile that dies later as "profile has empty provider".
func TestExecutorValidate_UnknownProfileIsPermanentAndNamed(t *testing.T) {
	deps := &Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t), Profiles: config.ProfileMap{
		"implementer": {Executor: "cli", Provider: "claude-code", Command: "claude"},
	}}
	e := NewExecutor(deps)

	err := e.Validate(&executor.ExecutionJob{TaskID: "CW-1", AgentProfile: "tangent-worker-codex"})
	require.Error(t, err)
	var perm *executor.PermanentError
	assert.True(t, errors.As(err, &perm), "got %v", err)
	assert.Contains(t, err.Error(), "tangent-worker-codex")
	assert.NotContains(t, err.Error(), "empty provider")
}

func TestExecutorValidate_NoProfileResolvesToDefault(t *testing.T) {
	deps := &Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t), Profiles: config.ProfileMap{}}
	e := NewExecutor(deps)
	for _, job := range []*executor.ExecutionJob{
		{TaskID: "CW-2"},
		{TaskID: "CW-3", LaunchProfile: "worker.implementer"},
	} {
		err := e.Validate(job)
		if err != nil {
			assert.NotContains(t, err.Error(), "empty provider")
			assert.NotContains(t, err.Error(), "does not resolve")
		}
	}
}
