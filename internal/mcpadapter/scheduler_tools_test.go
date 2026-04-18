package mcpadapter_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

// setupAdapterWithScheduler builds a scheduler instance and wires it into a
// fresh adapter. The scheduler is NOT Run()-ed — tests only exercise
// SetEnabled/Status which are safe to call on an un-started scheduler.
func setupAdapterWithScheduler(t *testing.T, initialEnabled bool) (*mcpadapter.Adapter, *scheduler.Scheduler) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { q.Close() })

	registry := executor.NewRegistry()
	registry.Register(executor.NewMockExecutor())
	predicates := waitpoll.NewRegistry()

	cfg := &config.SchedulerConfig{
		Workers:         2,
		IntervalSeconds: 1,
		RetryBudget:     3,
		StaleSeconds:    300,
		Enabled:         initialEnabled,
	}
	sched := scheduler.New(store, q, registry, predicates, cfg)

	svc := service.New(store)
	return mcpadapter.New(svc, sched), sched
}

// TestSchedulerToggle_RoundTrip exercises the happy path: start enabled,
// toggle to disabled via the MCP tool, confirm status reflects it, toggle
// back to enabled, confirm again. Status is checked both via the adapter
// (clockwork_scheduler_status) and directly on the scheduler so we know the
// MCP path actually drove the real state, not just a reflected arg.
func TestSchedulerToggle_RoundTrip(t *testing.T) {
	a, sched := setupAdapterWithScheduler(t, true)

	// Starting state: enabled per cfg.
	require.True(t, sched.Status().Enabled, "precondition: scheduler starts enabled")

	// Toggle off via MCP. Schema declares enabled as a string (Phase A pattern:
	// every arg is a string, server-side coerce via reqBool). Callers must send
	// "true"/"false"; mcp-go's stdio schema validator rejects JSON booleans.
	text, isErr := callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": "false",
	})
	require.False(t, isErr, "toggle off should succeed: %s", text)
	var status map[string]interface{}
	parseData(t, text, &status)
	assert.Equal(t, false, status["enabled"], "response should echo new state")

	// Direct service check — no reliance on the returned JSON.
	assert.False(t, sched.Status().Enabled, "scheduler.Status() must agree with MCP response")

	// Status tool should also reflect the change.
	statusText, isErr := callTool(t, a, "clockwork_scheduler_status", map[string]interface{}{})
	require.False(t, isErr, statusText)
	parseData(t, statusText, &status)
	assert.Equal(t, false, status["enabled"])

	// Toggle back on.
	text, isErr = callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": "true",
	})
	require.False(t, isErr, text)
	parseData(t, text, &status)
	assert.Equal(t, true, status["enabled"])
	assert.True(t, sched.Status().Enabled)
}

// TestSchedulerToggle_Idempotent confirms that toggling to the current state
// is a silent no-op returning the same status — the contract required by the
// ticket so agents don't need to read-before-write.
func TestSchedulerToggle_Idempotent(t *testing.T) {
	a, sched := setupAdapterWithScheduler(t, true)

	// Toggle to enabled=true when already enabled. Schema requires string.
	text, isErr := callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": "true",
	})
	require.False(t, isErr, text)
	var status map[string]interface{}
	parseData(t, text, &status)
	assert.Equal(t, true, status["enabled"])
	assert.True(t, sched.Status().Enabled)

	// Toggle off, then toggle off again.
	_, _ = callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": "false",
	})
	text, isErr = callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": "false",
	})
	require.False(t, isErr, text)
	parseData(t, text, &status)
	assert.Equal(t, false, status["enabled"])
	assert.False(t, sched.Status().Enabled)
}

// TestSchedulerTools_NoScheduler verifies the nil-sched path — mirrors the
// HTTP 503 contract. The stdio mcp subcommand runs without a scheduler
// instance, so agents calling these tools there must see a clear error
// rather than a zero-value status or a nil panic.
func TestSchedulerTools_NoScheduler(t *testing.T) {
	// Use the regular setupAdapter which passes nil for sched.
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_scheduler_status", map[string]interface{}{})
	assert.True(t, isErr, "status without scheduler should error: %s", text)
	assert.Contains(t, text, "scheduler not running")

	text, isErr = callTool(t, a, "clockwork_scheduler_toggle", map[string]interface{}{
		"enabled": true,
	})
	assert.True(t, isErr, "toggle without scheduler should error: %s", text)
	assert.Contains(t, text, "scheduler not running")
}
