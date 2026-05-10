//go:build sqlite_concurrency

package sqlstore_test

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/require"
)

// TestConcurrentWritePaths_NoSQLITEBusy is the Phase 1 acceptance gate for
// CW-20260510-0084/CW-20260510-0112. Phase 0 intentionally lands it behind a
// build tag so the repo stays green while the current mainline substrate still
// shares an unrestricted writer pool.
func TestConcurrentWritePaths_NoSQLITEBusy(t *testing.T) {
	store := sqlitetest.OpenStore(t, sqlitetest.WithBusyTimeout(10))

	var busyCount atomic.Int64
	var mu sync.Mutex
	var otherErrs []string
	recordErr := func(prefix string, err error) {
		if err == nil {
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") {
			busyCount.Add(1)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(otherErrs) < 5 {
			otherErrs = append(otherErrs, prefix+": "+msg)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
					TaskID:  fmt.Sprintf("CW-CONC-%02d", worker),
					Type:    "tool_use",
					Payload: `{"kind":"tool_use"}`,
				})
				recordErr("append run event", err)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				err := store.CreateSession(&sqlstore.SessionRecord{
					ID: fmt.Sprintf("SES-%02d-%03d", worker, i),
				})
				recordErr("create session", err)
			}
		}(g)
	}
	wg.Wait()

	require.Empty(t, otherErrs, "unexpected non-SQLITE_BUSY write errors: %v", otherErrs)
	require.Zero(t, busyCount.Load(),
		"concurrent run_event/session writes hit SQLITE_BUSY; Phase 1's single-writer split must drive this to zero")
}
