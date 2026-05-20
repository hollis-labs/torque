package mcpadapter_test

import (
	"database/sql"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func setupLoopbackWithReminder(t *testing.T) (*mcpadapter.Adapter, string, *steering.ReminderRegistry) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc := service.New(store)

	const taskID = "CW-TEST-DISMISS-0001"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "dismiss tool test task",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "implementer",
	}))

	reg := steering.NewReminderRegistry()
	return mcpadapter.NewLoopback(svc, taskID).WithReminderRegistry(reg), taskID, reg
}

func envForDismiss(id string) gomsg.Envelope {
	return gomsg.Envelope{
		ID:          id,
		Kind:        gomsg.MsgKindNotice,
		From:        gomsg.Address{Kind: gomsg.KindUser, Authority: "local", ID: "operator"},
		To:          gomsg.Address{Kind: gomsg.KindSession, Authority: "local", ID: "SES-DISMISS"},
		Payload:     []byte(`"hi"`),
		ContentType: "application/json",
	}
}

// TestLoopback_SteeringDismiss_AcksKnownEnvelopes covers the happy path:
// dismissed envelopes appear in `data.dismissed` and the registry's
// Snapshot reflects the dismissal so PendingForTurnBoundary no longer
// returns them.
func TestLoopback_SteeringDismiss_AcksKnownEnvelopes(t *testing.T) {
	a, taskID, reg := setupLoopbackWithReminder(t)
	reg.RecordDelivery(taskID, envForDismiss("ENV-A"))
	reg.RecordDelivery(taskID, envForDismiss("ENV-B"))

	text, isErr := callTool(t, a, "torque_steering_dismiss", map[string]interface{}{
		"envelope_ids": []interface{}{"ENV-A", "ENV-B"},
	})
	require.False(t, isErr, text)

	var out map[string]interface{}
	parseData(t, text, &out)
	dismissed, _ := out["dismissed"].([]interface{})
	unknown, _ := out["unknown"].([]interface{})
	assert.ElementsMatch(t, []interface{}{"ENV-A", "ENV-B"}, dismissed)
	assert.Empty(t, unknown)

	snap := reg.Snapshot(taskID)
	require.Len(t, snap, 2)
	for _, pe := range snap {
		assert.True(t, pe.Dismissed, "envelope %s should be dismissed", pe.EnvelopeID)
	}
}

// TestLoopback_SteeringDismiss_ReportsUnknownEnvelopes covers the
// "stale id" branch: ids the registry doesn't recognize are returned in
// `data.unknown` so the agent can tell ack succeeded from no-op.
func TestLoopback_SteeringDismiss_ReportsUnknownEnvelopes(t *testing.T) {
	a, taskID, reg := setupLoopbackWithReminder(t)
	reg.RecordDelivery(taskID, envForDismiss("ENV-KNOWN"))

	text, isErr := callTool(t, a, "torque_steering_dismiss", map[string]interface{}{
		"envelope_ids": []interface{}{"ENV-KNOWN", "ENV-GHOST"},
	})
	require.False(t, isErr, text)

	var out map[string]interface{}
	parseData(t, text, &out)
	dismissed, _ := out["dismissed"].([]interface{})
	unknown, _ := out["unknown"].([]interface{})
	assert.Equal(t, []interface{}{"ENV-KNOWN"}, dismissed)
	assert.Equal(t, []interface{}{"ENV-GHOST"}, unknown)
}

// TestLoopback_SteeringDismiss_RejectsEmptyList covers the validation
// boundary: callers must pass at least one envelope id.
func TestLoopback_SteeringDismiss_RejectsEmptyList(t *testing.T) {
	a, _, _ := setupLoopbackWithReminder(t)

	text, isErr := callTool(t, a, "torque_steering_dismiss", map[string]interface{}{
		"envelope_ids": []interface{}{},
	})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "envelope_ids")
}

// TestLoopback_SteeringDismiss_NoRegistryReturnsDomainError covers the
// degraded path: an adapter without a wired registry must report the
// dismiss as unavailable instead of panicking. This is the stdio MCP /
// test-fixture path.
func TestLoopback_SteeringDismiss_NoRegistryReturnsDomainError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc := service.New(store)

	const taskID = "CW-TEST-DISMISS-NO-REG"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "no registry",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "implementer",
	}))
	a := mcpadapter.NewLoopback(svc, taskID) // no WithReminderRegistry

	text, isErr := callTool(t, a, "torque_steering_dismiss", map[string]interface{}{
		"envelope_ids": []interface{}{"ENV-X"},
	})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "not configured")
}
