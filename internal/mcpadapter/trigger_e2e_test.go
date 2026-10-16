package mcpadapter

import (
	"context"
	"database/sql"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"

	_ "modernc.org/sqlite"
)

// callOverProtocol drives a tool through the real middleware chain (an
// in-memory client/server pair), which is where the trigger is stamped.
func callOverProtocol(t *testing.T, a *Adapter, name string, args map[string]any) {
	t.Helper()
	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "trigger-test", Version: "0"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	ss, err := a.Server().SDKServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Wait() })
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	if res.IsError {
		tc, _ := res.Content[0].(*mcpsdk.TextContent)
		t.Fatalf("tool error: %s", tc.Text)
	}
}

func lastOutboxTrigger(t *testing.T, s *sqlstore.Store, taskID string) (kind string, to string) {
	t.Helper()
	require.NoError(t, s.DB().QueryRow(
		`SELECT trigger_kind, json_extract(json_extract(envelope, '$.payload'), '$.to')
		 FROM event_outbox WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID).Scan(&kind, &to))
	return kind, to
}

func newTriggerStore(t *testing.T) (*sqlstore.Store, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, service.New(store)
}

// A transition made by an agent's own loopback adapter is an agent
// transition, even with no auth guard and no principal (off mode).
func TestTrigger_LoopbackTransitionIsRecordedAsAgent(t *testing.T) {
	store, svc := newTriggerStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-TRG-1", Title: "t", Priority: 2, Executor: "cli"}))

	callOverProtocol(t, NewLoopback(svc, "CW-TRG-1"), "torque_task_blocked", map[string]any{"reason": "needs input"})

	kind, to := lastOutboxTrigger(t, store, "CW-TRG-1")
	require.Equal(t, "blocked", to)
	require.Equal(t, sqlstore.TriggerAgent, kind)
}

// The remote surface, with no verified agent principal, stays human.
func TestTrigger_RemoteTransitionWithoutPrincipalIsRecordedAsHuman(t *testing.T) {
	store, svc := newTriggerStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-TRG-2", Title: "t", Priority: 2, Executor: "cli"}))

	callOverProtocol(t, New(svc, nil), "torque_task_transition", map[string]any{"id": "CW-TRG-2", "status": "doing"})

	kind, to := lastOutboxTrigger(t, store, "CW-TRG-2")
	require.Equal(t, "doing", to)
	require.Equal(t, sqlstore.TriggerHuman, kind)
}
