package mcpadapter_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"

	_ "modernc.org/sqlite"
)

// TestPlanStart_NoSessionsWired_ReturnsDomainError is the regression test for
// the legacy stdio-mcp behavior — without WithSessions wired, plan_start
// surfaces ErrSessionMgrMissing instead of attempting to boot.
func TestPlanStart_NoSessionsWired_ReturnsDomainError(t *testing.T) {
	a := setupAdapter(t) // mcpadapter.New(svc, nil) without .WithSessions

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		"plan_id": "CW-DOES-NOT-MATTER",
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
	assert.Contains(t, msg, "session manager not configured",
		"plan_start without WithSessions must surface ErrSessionMgrMissing")
}

// TestPlanStart_SessionsWired_PassesNilCheck is the CW-20260509-0013
// regression test — wiring agent.Manager into the stdio adapter via
// .WithSessions makes plan_start route through to planstart.Start, which
// then errors on the (intentionally bogus) plan_id with ErrPlanNotFound.
// The point is to prove we got PAST the ErrSessionMgrMissing nil check —
// the specific downstream error is incidental.
func TestPlanStart_SessionsWired_PassesNilCheck(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	svc := service.New(store)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)

	a := mcpadapter.New(svc, nil).WithSessions(deps.Sessions)

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		"plan_id": "CW-DOES-NOT-EXIST-IN-DB",
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, _ := parseError(t, text)
	// We expect arg_invalid (ErrPlanNotFound), NOT domain (ErrSessionMgrMissing).
	assert.Equal(t, "arg_invalid", code,
		"plan_start with sessions wired must NOT return ErrSessionMgrMissing — got code=%q msg=%q", code, msg)
	assert.NotContains(t, msg, "session manager not configured",
		"with sessions wired, the error must be plan-related, not session-manager-related")
}

// TestPlanStart_SessionsWired_MissingPlanID is the input-validation test —
// after sessions are wired, the explicit empty-plan_id arg check still
// surfaces the dedicated arg_invalid error (not ErrSessionMgrMissing).
func TestPlanStart_SessionsWired_MissingPlanID(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	svc := service.New(store)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)

	a := mcpadapter.New(svc, nil).WithSessions(deps.Sessions)

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		// no plan_id
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "plan_id", field)
	assert.Contains(t, msg, "plan_id is required")
}
