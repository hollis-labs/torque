package mcpadapter_test

import (
	"context"
	"testing"
	"encoding/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateMCP_CapabilityProfileRoundTrip(t *testing.T) {
	adapter := setupAdapter(t)
	ctx := context.Background()

	// 1. Create template
	createArgs := map[string]any{
		"id": "tpl-cap-test",
		"name": "Cap Test",
		"description": "Test",
		"kind": "agent",
		"role": "specialist",
		"tier": "advanced",
		"capability_profile": `{"mcp":["server1"]}`,
	}
	createRes, err := adapter.Server().CallTool(ctx, "torque_template_create", createArgs)
	require.NoError(t, err)

	b, _ := json.Marshal(createRes)
	assert.Contains(t, string(b), `"Role":{"String":"specialist","Valid":true}`)
	assert.Contains(t, string(b), `"Tier":{"String":"advanced","Valid":true}`)
	assert.Contains(t, string(b), `server1`)

	// 2. Update template
	updateArgs := map[string]any{
		"id": "tpl-cap-test",
		"tier": "basic",
		"capability_profile": `{"mcp":["server2"]}`,
	}
	updateRes, err := adapter.Server().CallTool(ctx, "torque_template_update", updateArgs)
	require.NoError(t, err)

	b2, _ := json.Marshal(updateRes)
	assert.Contains(t, string(b2), `"Role":{"String":"specialist","Valid":true}`)
	assert.Contains(t, string(b2), `"Tier":{"String":"basic","Valid":true}`)
	assert.Contains(t, string(b2), `server2`)
}
