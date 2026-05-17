package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSessionCreate_UnknownAgentProfile(t *testing.T) {
	a := &Adapter{
		sessions: agent.NewManager(&agent.Dependencies{
			Profiles: config.ProfileMap{
				"default": {},
				"fast":    {},
			},
		}),
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"agent_profile": "typo",
		"workdir":       t.TempDir(),
	}

	res, err := a.handleSessionCreate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, `"code": "arg_invalid"`)
	assert.Contains(t, text.Text, "unknown agent_profile 'typo' in profiles.yaml agent_profiles registry — known: [default fast]")
}
