package cliexec

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

// newSvcWithSeededTask builds an in-memory service with one seeded task so
// loopback round-trips have a real target. Mirrors the loopback_test.go
// fixture pattern from internal/mcpadapter/.
//
// Returns a closeFn the caller must invoke before any goleak check —
// database/sql's connectionOpener goroutine runs until the DB is closed,
// and goleak's defer order would catch it otherwise.
func newSvcWithSeededTask(t *testing.T) (*service.Service, string, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)

	// Seed a task via the global adapter — same shape used by
	// mcpadapter/loopback_test.go.
	global := mcpadapter.New(svc, nil)
	createReq := mustJSON(t, map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0.1.0"},
		},
	})
	global.Server().HandleMessage(context.Background(), createReq)

	createMsg := mustJSON(t, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{
			"name": "clockwork_task_create",
			"arguments": map[string]interface{}{
				"title":       "loopback-cliexec-target",
				"description": "cliexec loopback test target",
			},
		},
	})
	resp := global.Server().HandleMessage(context.Background(), createMsg)
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(respBytes, &envelope))
	require.False(t, envelope.Result.IsError, "task create error: %s", string(respBytes))
	require.NotEmpty(t, envelope.Result.Content, "task create response empty: %s", string(respBytes))

	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			ID string `json:"ID"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload))
	require.True(t, payload.OK)
	require.NotEmpty(t, payload.Data.ID)

	return svc, payload.Data.ID, func() { _ = store.Close() }
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestSetupLoopback_NilSvcReturnsNil(t *testing.T) {
	h, err := setupLoopback(nil, "CW-X")
	require.NoError(t, err)
	assert.Nil(t, h, "nil svc should disable loopback wiring")
	// Shutdown on nil handle is safe.
	require.NoError(t, h.Shutdown(context.Background()))
}

func TestSetupLoopback_BindsRandomPort(t *testing.T) {
	// Defer order matters: goleak runs LAST (declared first → LIFO last).
	// closeSvc must run before goleak so the database/sql connectionOpener
	// goroutine has exited.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	svc, taskID, closeSvc := newSvcWithSeededTask(t)
	defer closeSvc()

	h, err := setupLoopback(svc, taskID)
	require.NoError(t, err)
	require.NotNil(t, h)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, h.Shutdown(shutdownCtx))
	}()

	assert.Greater(t, h.Port(), 0, "port should be a positive bound TCP port")
	assert.Contains(t, h.url, "http://127.0.0.1:")
	assert.Contains(t, h.url, "/mcp")
}

func TestLoopback_NoLeak_OnImmediateShutdown(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	svc, taskID, closeSvc := newSvcWithSeededTask(t)
	defer closeSvc()

	h, err := setupLoopback(svc, taskID)
	require.NoError(t, err)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(shutdownCtx))
}

func TestLoopback_RoundTrip_InitializeAccepted(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent(),
		// http.DefaultClient keeps an idle connection pool alive for ~90s
		// after the server-side close; we explicitly close the response
		// body but the read/write loops persist briefly. Ignore them.
		goleak.IgnoreAnyFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreAnyFunction("net/http.(*persistConn).writeLoop"))

	svc, taskID, closeSvc := newSvcWithSeededTask(t)
	defer closeSvc()

	h, err := setupLoopback(svc, taskID)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, h.Shutdown(shutdownCtx))
	}()

	body := mustJSON(t, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "loopback-test", "version": "0.1.0"},
		},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	// Streamable HTTP transport per MCP 2025-03-26 spec accepts both event-stream
	// and json content negotiation; setting both keeps mcp-go's content-type
	// dispatch happy across server versions.
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "non-200 from loopback: %d body=%s", resp.StatusCode, string(respBody))

	// SSE-encoded responses begin with "event:"; raw-JSON responses begin
	// with "{". Either is acceptable — what we verify is that the JSON-RPC
	// payload reports the loopback's serverInfo (name == "Clockwork Loopback").
	bodyStr := string(respBody)
	assert.Contains(t, bodyStr, "Clockwork Loopback",
		"initialize response should advertise loopback's serverInfo: %s", bodyStr)
}
