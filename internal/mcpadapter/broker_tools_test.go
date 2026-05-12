package mcpadapter_test

import (
	"context"
	"path/filepath"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	clockmsg "github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// setupAdapterWithBroker is a sibling of setupAdapter that wires a real
// broker. File-backed SQLite (WAL) avoids the per-connection :memory: trap
// the broker tests already document.
func setupAdapterWithBroker(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "broker.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
		db.Close()
	})
	svc := service.New(store)
	a := mcpadapter.New(svc, nil)
	a.WithBroker(broker.New(clockmsg.NewStore(db), nil))
	return a
}

// AC5: clockwork_broker_send round-trips a kind=notice envelope.
func TestFullStack_BrokerSend_Notice(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "clockwork_broker_send", map[string]interface{}{
		"kind":    "notice",
		"from":    "msg://agent/test/alice",
		"to":      "msg://agent/test/bob",
		"payload": `{"hello":"world"}`,
	})
	require.False(t, isErr, "send should not error: %s", text)

	var env gomsg.Envelope
	parseData(t, text, &env)
	assert.Equal(t, gomsg.MsgKindNotice, env.Kind)
	assert.NotEmpty(t, env.ID)
}

// Validation flows back as arg_invalid (broker.ErrValidation maps to
// ErrCodeArgInvalid via brokerErrResult).
func TestFullStack_BrokerSend_BogusKindRejected(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "clockwork_broker_send", map[string]interface{}{
		"kind": "bogus",
		"from": "msg://agent/test/alice",
		"to":   "msg://agent/test/bob",
	})
	require.True(t, isErr, "send with unknown kind should error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
}

// AC5: clockwork_broker_inbox drains and returns matched envelopes.
func TestFullStack_BrokerInbox_Drains(t *testing.T) {
	a := setupAdapterWithBroker(t)

	for i := 0; i < 3; i++ {
		text, isErr := callTool(t, a, "clockwork_broker_send", map[string]interface{}{
			"kind":    "notice",
			"from":    "msg://agent/test/alice",
			"to":      "msg://agent/test/bob",
			"payload": `{}`,
		})
		require.False(t, isErr, "send %d failed: %s", i, text)
	}

	text, isErr := callTool(t, a, "clockwork_broker_inbox", map[string]interface{}{
		"to": "msg://agent/test/bob",
	})
	require.False(t, isErr, "inbox should not error: %s", text)

	var page struct {
		Envelopes []gomsg.Envelope `json:"envelopes"`
	}
	parseData(t, text, &page)
	assert.Len(t, page.Envelopes, 3)

	// Drained.
	text, _ = callTool(t, a, "clockwork_broker_inbox", map[string]interface{}{
		"to": "msg://agent/test/bob",
	})
	parseData(t, text, &page)
	assert.Empty(t, page.Envelopes)
}

// AC5: clockwork_broker_request returns a domain error when the timeout
// expires with no matching response.
func TestFullStack_BrokerRequest_Timeout(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "clockwork_broker_request", map[string]interface{}{
		"from":            "msg://agent/test/asker",
		"to":              "msg://agent/test/silent",
		"payload":         `{}`,
		"timeout_seconds": "1",
	})
	require.True(t, isErr, "request with no responder should time out: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
}

// timeout_seconds outside [1,600] is rejected at the arg layer.
func TestFullStack_BrokerRequest_TimeoutOutOfRange(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "clockwork_broker_request", map[string]interface{}{
		"from":            "msg://agent/test/asker",
		"to":              "msg://agent/test/silent",
		"timeout_seconds": "99999",
	})
	require.True(t, isErr, "out-of-range timeout should fail: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "timeout_seconds", field)
}

// When the adapter has no broker wired (mcp-only stdio path), tools
// reply with a domain error rather than panicking.
func TestFullStack_BrokerNoBrokerWired(t *testing.T) {
	// Use the standard setupAdapter (no WithBroker call).
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_broker_send", map[string]interface{}{
		"kind": "notice",
		"from": "msg://agent/test/a",
		"to":   "msg://agent/test/b",
	})
	require.True(t, isErr, "send without broker should fail: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
}
