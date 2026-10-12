package service_test

import (
	"context"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestForumsFeatureGateAndDegradedState(t *testing.T) {
	svc := setupService(t)
	_, _, err := svc.Forums.Do(context.Background(), "GET", "/forums", nil)
	var disabled *service.FeatureDisabledError
	require.ErrorAs(t, err, &disabled)
	require.NoError(t, svc.Feature.Enable("forums"))
	_, _, err = svc.Forums.Do(context.Background(), "GET", "/forums", nil)
	var unavailable *tetherclient.ForumsError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, 503, unavailable.Status)
	require.NoError(t, svc.Feature.Disable("forums"))
	_, _, err = svc.Forums.Do(context.Background(), "GET", "/forums", nil)
	require.ErrorAs(t, err, &disabled)
}
