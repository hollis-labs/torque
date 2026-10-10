package mcpadapter_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateMCP_CapabilityProfileRoundTrip(t *testing.T) {
	adapter := setupAdapter(t)
	ctx := context.Background()

	// 1. Create template
	createArgs := map[string]any{
		"id":                 "tpl-cap-test",
		"name":               "Cap Test",
		"description":        "Test",
		"kind":               "agent",
		"role":               "specialist",
		"tier":               "advanced",
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
		"id":                 "tpl-cap-test",
		"tier":               "basic",
		"capability_profile": `{"mcp":["server2"]}`,
	}
	updateRes, err := adapter.Server().CallTool(ctx, "torque_template_update", updateArgs)
	require.NoError(t, err)

	b2, _ := json.Marshal(updateRes)
	assert.Contains(t, string(b2), `"Role":{"String":"specialist","Valid":true}`)
	assert.Contains(t, string(b2), `"Tier":{"String":"basic","Valid":true}`)
	assert.Contains(t, string(b2), `server2`)
}

func TestTemplateMCP_CapabilityProfileObject(t *testing.T) {
	adapter := setupAdapter(t)
	ctx := context.Background()

	res, err := adapter.Server().CallTool(ctx, "torque_template_create", map[string]any{
		"id":   "tpl-cap-obj",
		"name": "Obj Test",
		"capability_profile": map[string]any{
			"mcp": []any{"server-obj"},
		},
	})
	assert.NoError(t, err)
	b, _ := json.Marshal(res)
	assert.Contains(t, string(b), `server-obj`)

	resUpdate, err := adapter.Server().CallTool(ctx, "torque_template_update", map[string]any{
		"id": "tpl-cap-obj",
		"capability_profile": map[string]any{
			"mcp": []any{"server-obj-2"},
		},
	})
	assert.NoError(t, err)
	b2, _ := json.Marshal(resUpdate)
	assert.Contains(t, string(b2), `server-obj-2`)
}

func TestTemplateMCP_CapabilityProfileInvalidJSON(t *testing.T) {
	adapter := setupAdapter(t)
	ctx := context.Background()

	_, err := adapter.Server().CallTool(ctx, "torque_template_create", map[string]any{
		"id":                 "tpl-cap-invalid",
		"name":               "Invalid Test",
		"capability_profile": "{invalid json}",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid capability_profile JSON string")

	_, err = adapter.Server().CallTool(ctx, "torque_template_update", map[string]any{
		"id":                 "tpl-cap-test", // Use the existing one from the roundtrip test
		"capability_profile": "{invalid json 2}",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid capability_profile JSON string")
}

func TestTemplateMCP_CapabilityProfileNumber(t *testing.T) {
	adapter := setupAdapter(t)
	ctx := context.Background()

	_, err := adapter.Server().CallTool(ctx, "torque_template_create", map[string]any{
		"id":                 "tpl-cap-num",
		"name":               "Num Test",
		"capability_profile": 123,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "capability_profile must be a JSON string or object")

	_, err = adapter.Server().CallTool(ctx, "torque_template_update", map[string]any{
		"id":                 "tpl-cap-test",
		"capability_profile": 456,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "capability_profile must be a JSON string or object")
}
