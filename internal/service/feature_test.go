package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeatureDisabledByDefault(t *testing.T) {
	svc := setupService(t)

	assert.False(t, svc.Feature.IsEnabled("sprints"))
	assert.False(t, svc.Feature.IsEnabled("projects"))
	assert.False(t, svc.Feature.IsEnabled("epics"))
}

func TestFeatureEnableDisable(t *testing.T) {
	svc := setupService(t)

	err := svc.Feature.Enable("sprints")
	require.NoError(t, err)
	assert.True(t, svc.Feature.IsEnabled("sprints"))

	err = svc.Feature.Disable("sprints")
	require.NoError(t, err)
	assert.False(t, svc.Feature.IsEnabled("sprints"))
}

func TestFeatureEnableInvalidFeature(t *testing.T) {
	svc := setupService(t)

	err := svc.Feature.Enable("nonexistent")
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestFeatureListEnabled(t *testing.T) {
	svc := setupService(t)

	svc.Feature.Enable("sprints")
	svc.Feature.Enable("epics")

	enabled := svc.Feature.ListEnabled()
	assert.Contains(t, enabled, "sprints")
	assert.Contains(t, enabled, "epics")
	assert.NotContains(t, enabled, "projects")
}

func TestFeatureRequire(t *testing.T) {
	svc := setupService(t)

	err := svc.Feature.Require("sprints")
	assert.Error(t, err)
	assert.IsType(t, &service.FeatureDisabledError{}, err)

	svc.Feature.Enable("sprints")
	err = svc.Feature.Require("sprints")
	assert.NoError(t, err)
}
