package mcpadapter_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

// CW-20260910-0087: over MCP, an executor the process has not registered is
// arg_invalid naming the registered set, on create and on update, the
// precedent CW-20260907-0060 set for unknown arguments.
func TestFullStack_TaskExecutorMustBeRegistered(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	svc.Task.SetRegisteredExecutors(func() []string { return []string{"api", "cli", "mock"} })
	a := mcpadapter.New(svc, nil)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "provider in the executor column", "description": "x", "executor": "opencode",
	})
	require.True(t, isErr, "an unregistered executor must be refused: %s", text)
	code, msg, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "executor", field)
	require.Contains(t, msg, "expected one of: api, cli, mock")

	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{"title": "ok", "description": "x", "executor": "cli"})
	require.False(t, isErr, "a registered executor is accepted: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": created["ID"], "executor": "codex"})
	require.True(t, isErr, "updating to an unregistered executor must be refused: %s", text)
	code, _, field = parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "executor", field)
}
