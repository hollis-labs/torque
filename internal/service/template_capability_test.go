package service_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_CapabilityProfileRoundTrip(t *testing.T) {
	svc := setupService(t)

	// Create
	tpl, err := svc.Template.Create(service.TemplateCreateInput{
		ID:          "tpl-cap-test",
		Name:        "Test Template",
		Description: "A template",
		Kind:        "agent",
		Role:        "specialist",
		Tier:        "advanced",
		CapabilityProfile: map[string]any{
			"mcp": []any{"server1"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "specialist", tpl.Role.String)
	assert.Equal(t, "advanced", tpl.Tier.String)
	assert.Contains(t, tpl.CapabilityProfile.String, `"server1"`)

	// Instantiate
	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: tpl.ID,
		Title:      "Instantiated Task",
	})
	require.NoError(t, err)

	assert.Equal(t, "specialist", task.Role)
	assert.Equal(t, "advanced", task.Tier)
	assert.Contains(t, task.CapabilityProfile.String, `"server1"`)

	// Update (clear it)
	emptyMap := map[string]any{}
	tpl2, err := svc.Template.Update(tpl.ID, service.TemplateUpdateInput{
		CapabilityProfile: emptyMap,
	})
	require.NoError(t, err)
	assert.Equal(t, "{}", tpl2.CapabilityProfile.String)
}
