package sqlstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appendEvent is the seam T3 calls for progress.checkpoint and data.emitted:
// it must return the canonical id, keep the payload untouched, and take an
// optional run scope and idempotency key without any from/to state.
func TestAppendEvent_GenericPayloadReturnsCanonicalID(t *testing.T) {
	s := setupTestStore(t)
	w, err := s.BeginWriteTx(context.Background())
	require.NoError(t, err)
	defer w.Rollback()

	payload := json.RawMessage(`{"schema":"acme.thing@1","data":{"nested":{"a":[1,2,3]}}}`)
	id, err := sqlstore.AppendEvent(w, sqlstore.EventSpec{
		Kind: "data.emitted", Category: "data",
		EntityType: "task", EntityID: "CW-GEN-1",
		Payload:        payload,
		PayloadSchema:  "urn:hollis-labs:events:data_emitted@1",
		Trigger:        sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_data_emit"},
		Scope:          sqlstore.StateScope{TaskID: "CW-GEN-1", RunID: sql.NullInt64{Int64: 7, Valid: true}},
		IdempotencyKey: "k1",
	})
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	var envJSON, cat, trig string
	require.NoError(t, s.DB().QueryRow(
		`SELECT envelope, category, trigger_kind FROM event_outbox WHERE event_id = ?`, id).Scan(&envJSON, &cat, &trig))
	var ev runtimeevents.Event
	require.NoError(t, json.Unmarshal([]byte(envJSON), &ev))
	assert.Equal(t, id, ev.ID)
	assert.Equal(t, "data", cat)
	assert.Equal(t, "agent", trig)
	assert.JSONEq(t, string(payload), string(ev.Payload), "payload keeps its schema shape")
	assert.Equal(t, "7", ev.Scope.RunID)
	assert.Equal(t, "k1", ev.Corr.IdempotencyKey)
	assert.Equal(t, "urn:hollis-labs:events:data_emitted@1", ev.Class.PayloadSchema)
	assert.False(t, ev.Class.Sanitized)
}

func TestAppendEvent_RequiresKindAndEntity(t *testing.T) {
	s := setupTestStore(t)
	w, err := s.BeginWriteTx(context.Background())
	require.NoError(t, err)
	defer w.Rollback()
	_, err = sqlstore.AppendEvent(w, sqlstore.EventSpec{Payload: json.RawMessage(`{}`)})
	require.Error(t, err)
}
