package mcpadapter_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	clockmsg "github.com/hollis-labs/torque/internal/messaging"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/service"
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

// setupAdapterWithPolling wires a broker AND an opt-in poll registry, so
// the torque_inbox_poll tool exercises its full path. The registry is
// returned so tests can assert the opt-in state the bridge would observe.
func setupAdapterWithPolling(t *testing.T) (*mcpadapter.Adapter, *steering.PollRegistry) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "poll.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
		db.Close()
	})
	svc := service.New(store)
	reg := steering.NewPollRegistry(time.Hour)
	a := mcpadapter.New(svc, nil).
		WithBroker(broker.New(clockmsg.NewStore(db), nil)).
		WithPollRegistry(reg)
	return a, reg
}

// torque_inbox_poll records a polling opt-in and drains the inbox in one
// call (CW-20260518-0042).
func TestFullStack_InboxPoll_OptsInAndDrains(t *testing.T) {
	a, reg := setupAdapterWithPolling(t)

	for i := 0; i < 2; i++ {
		_, isErr := callTool(t, a, "torque_broker_send", map[string]interface{}{
			"kind": "notice", "from": "msg://agent/test/op", "to": "msg://agent/test/poller", "payload": `{}`,
		})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{
		"to": "msg://agent/test/poller",
	})
	require.False(t, isErr, "poll should not error: %s", text)

	var page struct {
		To             string           `json:"to"`
		Polling        bool             `json:"polling"`
		PollTTLSeconds int              `json:"poll_ttl_seconds"`
		Count          int              `json:"count"`
		Envelopes      []gomsg.Envelope `json:"envelopes"`
	}
	parseData(t, text, &page)
	assert.True(t, page.Polling, "a poll records the opt-in")
	assert.Equal(t, 2, page.Count)
	assert.Len(t, page.Envelopes, 2)
	assert.Greater(t, page.PollTTLSeconds, 0)

	// The opt-in the steering bridge would observe is live.
	assert.True(t, reg.IsPolling("msg://agent/test/poller"))

	// Inbox drained — a second poll returns nothing but keeps the opt-in.
	text, _ = callTool(t, a, "torque_inbox_poll", map[string]interface{}{"to": "msg://agent/test/poller"})
	parseData(t, text, &page)
	assert.Empty(t, page.Envelopes)
	assert.True(t, reg.IsPolling("msg://agent/test/poller"))
}

// release=true drops the opt-in so the bridge resumes inject-at-turn.
func TestFullStack_InboxPoll_Release(t *testing.T) {
	a, reg := setupAdapterWithPolling(t)

	_, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{"to": "msg://agent/test/poller"})
	require.False(t, isErr)
	require.True(t, reg.IsPolling("msg://agent/test/poller"))

	text, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{
		"to": "msg://agent/test/poller", "release": true,
	})
	require.False(t, isErr, "release poll should not error: %s", text)

	var page struct {
		Polling bool `json:"polling"`
	}
	parseData(t, text, &page)
	assert.False(t, page.Polling, "release reports polling off")
	assert.False(t, reg.IsPolling("msg://agent/test/poller"), "release drops the opt-in")
}

// A malformed recipient URN is rejected at the arg layer.
func TestFullStack_InboxPoll_BadURN(t *testing.T) {
	a, _ := setupAdapterWithPolling(t)
	text, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{"to": "not-a-urn"})
	require.True(t, isErr, "bad urn should fail: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "to", field)
}

// When no poll registry is wired, the tool surfaces a domain error
// (mirrors the no-broker contract).
func TestFullStack_InboxPoll_NoRegistryWired(t *testing.T) {
	a := setupAdapterWithBroker(t) // broker but no WithPollRegistry
	text, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{"to": "msg://agent/test/poller"})
	require.True(t, isErr, "poll without a registry should fail: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
}

// A registry with no broker still records the opt-in and returns a
// drain hint instead of envelopes.
func TestFullStack_InboxPoll_RegistryButNoBroker(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "nb.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(); db.Close() })

	reg := steering.NewPollRegistry(time.Hour)
	a := mcpadapter.New(service.New(store), nil).WithPollRegistry(reg) // no WithBroker

	text, isErr := callTool(t, a, "torque_inbox_poll", map[string]interface{}{"to": "msg://agent/test/poller"})
	require.False(t, isErr, "poll should still record the opt-in: %s", text)

	var page struct {
		Polling   bool   `json:"polling"`
		DrainHint string `json:"drain_hint"`
	}
	parseData(t, text, &page)
	assert.True(t, page.Polling)
	assert.NotEmpty(t, page.DrainHint, "no-broker host returns a drain hint")
	assert.True(t, reg.IsPolling("msg://agent/test/poller"))
}

// AC5: torque_broker_send round-trips a kind=notice envelope.
func TestFullStack_BrokerSend_Notice(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "torque_broker_send", map[string]interface{}{
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

	text, isErr := callTool(t, a, "torque_broker_send", map[string]interface{}{
		"kind": "bogus",
		"from": "msg://agent/test/alice",
		"to":   "msg://agent/test/bob",
	})
	require.True(t, isErr, "send with unknown kind should error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
}

// AC5: torque_broker_inbox drains and returns matched envelopes.
func TestFullStack_BrokerInbox_Drains(t *testing.T) {
	a := setupAdapterWithBroker(t)

	for i := 0; i < 3; i++ {
		text, isErr := callTool(t, a, "torque_broker_send", map[string]interface{}{
			"kind":    "notice",
			"from":    "msg://agent/test/alice",
			"to":      "msg://agent/test/bob",
			"payload": `{}`,
		})
		require.False(t, isErr, "send %d failed: %s", i, text)
	}

	text, isErr := callTool(t, a, "torque_broker_inbox", map[string]interface{}{
		"to": "msg://agent/test/bob",
	})
	require.False(t, isErr, "inbox should not error: %s", text)

	var page struct {
		Envelopes []gomsg.Envelope `json:"envelopes"`
	}
	parseData(t, text, &page)
	assert.Len(t, page.Envelopes, 3)

	// Drained.
	text, _ = callTool(t, a, "torque_broker_inbox", map[string]interface{}{
		"to": "msg://agent/test/bob",
	})
	parseData(t, text, &page)
	assert.Empty(t, page.Envelopes)
}

// AC5: torque_broker_request returns a domain error when the timeout
// expires with no matching response.
func TestFullStack_BrokerRequest_Timeout(t *testing.T) {
	a := setupAdapterWithBroker(t)

	text, isErr := callTool(t, a, "torque_broker_request", map[string]interface{}{
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

	text, isErr := callTool(t, a, "torque_broker_request", map[string]interface{}{
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

	text, isErr := callTool(t, a, "torque_broker_send", map[string]interface{}{
		"kind": "notice",
		"from": "msg://agent/test/a",
		"to":   "msg://agent/test/b",
	})
	require.True(t, isErr, "send without broker should fail: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
}
