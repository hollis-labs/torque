package mcpadapter

import (
	"context"
	"github.com/hollis-labs/torque/internal/testutil/sessionpaths"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSessionLaunchMCP_PathPolicy(t *testing.T) {
	for _, tool := range []string{"torque_session_create", "torque_session_launch"} {
		t.Run(tool, func(t *testing.T) {
			root, cases := sessionpaths.Fixture(t)
			for _, tc := range cases {
				for _, field := range []string{"workdir", "repo_root"} {
					t.Run(tc.Name+"/"+field, func(t *testing.T) {
						a, _, rt := sessionCreateFixture(t, root)
						req := map[string]any{"agent_profile": "default", "workdir": root, "repo_root": root, field: tc.Path, "env": map[string]any{"TORQUE_SESSION_ALLOWED_ROOTS": tc.Path}}
						_, err := a.server.CallTool(context.Background(), tool, req)
						require.Error(t, err)
						text := mcpErrorJSON(t, err)
						require.Contains(t, text, "arg_invalid")
						require.Contains(t, text, "TORQUE_SESSION_ALLOWED_ROOTS")
						require.Contains(t, text, tc.Rule)
						require.Nil(t, rt.lastStart, "a refused launch reached the runtime")
					})
				}
			}
		})
	}
}
