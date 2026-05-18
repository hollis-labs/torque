package bootstrap_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
)

func TestStuckWatcher_DisabledByConfig(t *testing.T) {
	closer, err := bootstrap.StuckWatcher(context.Background(), nil, nil, nil,
		config.StuckConfig{WatcherEnabled: false})
	require.NoError(t, err)
	require.NotNil(t, closer, "closer is always non-nil so the caller can defer it")
	closer() // must not panic
}

func TestStuckWatcher_NilDepsDegradesToNoop(t *testing.T) {
	// WatcherEnabled true but the session runtime / broker / dispatcher are
	// absent (a composition root that did not stand them up): the watcher
	// is not started rather than failing the daemon boot.
	closer, err := bootstrap.StuckWatcher(context.Background(), nil, nil, nil,
		config.StuckConfig{WatcherEnabled: true, IdleThresholdSeconds: 600, ScanIntervalSeconds: 60})
	require.NoError(t, err)
	require.NotNil(t, closer)
	closer() // must not panic
	assert.NotPanics(t, func() { closer() }, "closer is idempotent")
}
