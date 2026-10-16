package agent

import (
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLaunchProgressDetailOptions(t *testing.T) {
	for _, level := range []string{"", "off", "milestone", "step", "verbose"} {
		opts := optsFromJob(&executor.ExecutionJob{ProgressDetail: level, RunID: 17, AgentProfile: "mock"}, "/tmp")
		require.Equal(t, level, opts.ProgressDetail)
		require.Equal(t, int64(17), opts.RunID)
		require.NoError(t, opts.Validate())
	}
	opts := Options{AgentProfile: "mock", Workdir: "/tmp", ProgressDetail: "typo"}
	require.ErrorContains(t, opts.Validate(), "progress_detail")
}
