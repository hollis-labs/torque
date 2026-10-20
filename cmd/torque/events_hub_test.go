package main

import (
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

// With the relay switched on but no HOLLIS_EVENTS_URL there is no hub: nothing
// starts, and the daemon runs as it did.
func TestStartHubRelayDoesNothingWithoutAURL(t *testing.T) {
	t.Setenv(eventbus.EnvURL, "")
	var wg sync.WaitGroup
	startHubRelay(t.Context(), &wg, sqlitetest.OpenStore(t), t.TempDir())
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a goroutine was started although there is no hub URL")
	}
}
