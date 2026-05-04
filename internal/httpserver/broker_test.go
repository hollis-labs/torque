package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	clockmsg "github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// setupBrokerServer wires the broker through SetBroker so all routes serve
// real envelopes. File-backed SQLite (WAL) avoids the per-connection
// :memory: divergence the broker package tests already document.
func setupBrokerServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "broker.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	t.Cleanup(func() { db.Close() })
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	handler.SetBroker(broker.New(clockmsg.NewStore(db), handler.SSEHub()))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// noBrokerServer constructs a server WITHOUT calling SetBroker — all
// /broker routes must 503.
func noBrokerServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "broker.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	t.Cleanup(func() { db.Close() })
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP_Broker_SendHappyPath(t *testing.T) {
	ts := setupBrokerServer(t)
	body := `{
        "kind": "notice",
        "from": "msg://agent/test/alice",
        "to":   "msg://agent/test/bob",
        "payload": {"hello":"world"}
    }`
	resp, err := http.Post(ts.URL+"/api/v1/broker/send", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var env gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	resp.Body.Close()
	assert.Equal(t, gomsg.MsgKindNotice, env.Kind)
	assert.NotEmpty(t, env.ID)
}

func TestHTTP_Broker_SendValidation422(t *testing.T) {
	ts := setupBrokerServer(t)
	// Missing from + bogus kind → 422.
	body := `{"kind":"bogus","to":"msg://agent/test/bob"}`
	resp, err := http.Post(ts.URL+"/api/v1/broker/send", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestHTTP_Broker_RequestTimeout504(t *testing.T) {
	ts := setupBrokerServer(t)
	// No responder is subscribed, so the request will exhaust timeout_seconds
	// (clamped to MinRequestTimeout=1) and return 504.
	body := `{
        "from": "msg://agent/test/asker",
        "to":   "msg://agent/test/silent",
        "payload": {"q":"ping"},
        "timeout_seconds": 1
    }`
	resp, err := http.Post(ts.URL+"/api/v1/broker/request", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
}

func TestHTTP_Broker_RequestTimeoutOutOfRange422(t *testing.T) {
	ts := setupBrokerServer(t)
	body := `{
        "from": "msg://agent/test/asker",
        "to":   "msg://agent/test/silent",
        "timeout_seconds": 99999
    }`
	resp, err := http.Post(ts.URL+"/api/v1/broker/request", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestHTTP_Broker_InboxDrains(t *testing.T) {
	ts := setupBrokerServer(t)
	// Send three notices to the same recipient.
	body := `{
        "kind": "notice",
        "from": "msg://agent/test/alice",
        "to":   "msg://agent/test/bob",
        "payload": {}
    }`
	for i := 0; i < 3; i++ {
		resp, err := http.Post(ts.URL+"/api/v1/broker/send", "application/json", strings.NewReader(body))
		require.NoError(t, err)
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/api/v1/broker/inbox?to=" + urlEscape("msg://agent/test/bob"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var page struct {
		Envelopes []gomsg.Envelope `json:"envelopes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	assert.Len(t, page.Envelopes, 3)

	// Drained — second call returns zero.
	resp2, err := http.Get(ts.URL + "/api/v1/broker/inbox?to=" + urlEscape("msg://agent/test/bob"))
	require.NoError(t, err)
	defer resp2.Body.Close()
	var page2 struct {
		Envelopes []gomsg.Envelope `json:"envelopes"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&page2))
	assert.Empty(t, page2.Envelopes)
}

func TestHTTP_Broker_AllRoutes503WhenUnwired(t *testing.T) {
	ts := noBrokerServer(t)
	// /send POST
	resp, err := http.Post(ts.URL+"/api/v1/broker/send", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// /request POST
	resp, err = http.Post(ts.URL+"/api/v1/broker/request", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// /inbox GET
	resp, err = http.Get(ts.URL + "/api/v1/broker/inbox?to=" + urlEscape("msg://agent/test/bob"))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// urlEscape is a tiny inline helper rather than dragging net/url into
// every test for one call site.
func urlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ':' || c == '/' {
			out = append(out, '%', hexNibble(c>>4), hexNibble(c&0xF))
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func hexNibble(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'A' + (b - 10)
}
