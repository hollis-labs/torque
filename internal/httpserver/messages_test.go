package httpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/substrate/mesh/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/httpserver"
	clockmsg "github.com/hollis-labs/torque/internal/messaging"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

func setupMessagingServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	handler.SetMessaging(clockmsg.NewStore(db))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// noMessagingServer constructs a server WITHOUT calling SetMessaging.
// All /messages routes must 503 in that configuration.
func noMessagingServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP_Messages_SendGetCancel(t *testing.T) {
	ts := setupMessagingServer(t)

	body := `{
        "kind": "notice",
        "from": "msg://agent/test/sender",
        "to":   "msg://agent/test/recipient",
        "thread_id": "T-1",
        "content_type": "application/json",
        "payload": {"hello":"world"},
        "metadata": {"trace": "abc"}
    }`
	resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var sent gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sent))
	resp.Body.Close()
	require.NotEmpty(t, sent.ID)
	assert.Equal(t, gomsg.MsgKindNotice, sent.Kind)
	assert.Equal(t, "T-1", sent.ThreadID)

	resp, err = http.Get(ts.URL + "/api/v1/messages/" + sent.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, sent.ID, got.ID)
	assert.Equal(t, "abc", got.Metadata["trace"])

	// Cancel: idempotent 204 on first + second call
	for range []int{0, 1} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/messages/"+sent.ID+"/cancel", nil)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		resp.Body.Close()
	}
}

func TestHTTP_Messages_GetUnknownIs404(t *testing.T) {
	ts := setupMessagingServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/messages/no-such-id")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHTTP_Messages_BadJSONIs400(t *testing.T) {
	ts := setupMessagingServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(`{not json`))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHTTP_Messages_MissingFieldsIs422(t *testing.T) {
	ts := setupMessagingServer(t)
	// missing kind / from / to → 422
	resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(`{"kind":""}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestHTTP_Messages_InboxDrains(t *testing.T) {
	ts := setupMessagingServer(t)
	to := "msg://agent/test/inbox-r1"

	for i := 0; i < 3; i++ {
		body := `{"kind":"notice","from":"msg://agent/test/src","to":"` + to + `"}`
		resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	}

	q := url.Values{"to": []string{to}}
	resp, err := http.Get(ts.URL + "/api/v1/messages/inbox?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first struct {
		Messages []gomsg.Envelope `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	resp.Body.Close()
	require.Len(t, first.Messages, 3)

	// Second drain returns nothing.
	resp, err = http.Get(ts.URL + "/api/v1/messages/inbox?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var second struct {
		Messages []gomsg.Envelope `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&second))
	resp.Body.Close()
	require.Len(t, second.Messages, 0)
}

func TestHTTP_Messages_InboxRequiresTo(t *testing.T) {
	ts := setupMessagingServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/messages/inbox")
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestHTTP_Messages_ThreadIsReadOnly(t *testing.T) {
	ts := setupMessagingServer(t)
	to := "msg://agent/test/thread-r1"
	for i := 0; i < 2; i++ {
		body := `{"kind":"notice","from":"msg://agent/test/src","to":"` + to + `","thread_id":"TT"}`
		resp, _ := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/api/v1/messages/thread/TT")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first struct {
		Messages []gomsg.Envelope `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	resp.Body.Close()
	require.Len(t, first.Messages, 2)

	// Inbox should still show both — Thread did not mutate delivery state.
	resp, err = http.Get(ts.URL + "/api/v1/messages/inbox?to=" + url.QueryEscape(to))
	require.NoError(t, err)
	var inbox struct {
		Messages []gomsg.Envelope `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&inbox))
	resp.Body.Close()
	require.Len(t, inbox.Messages, 2)
}

func TestHTTP_Messages_ConsumeRoundTrip(t *testing.T) {
	ts := setupMessagingServer(t)
	to := "msg://agent/test/consume-r1"
	body := `{"kind":"notice","from":"msg://agent/test/src","to":"` + to + `"}`
	resp, _ := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
	var sent gomsg.Envelope
	json.NewDecoder(resp.Body).Decode(&sent)
	resp.Body.Close()

	consumeBody := `{"recipient":"` + to + `"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/messages/"+sent.ID+"/consume",
		bytes.NewBufferString(consumeBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp2.StatusCode)
	resp2.Body.Close()

	// Get reflects consumption.
	resp3, _ := http.Get(ts.URL + "/api/v1/messages/" + sent.ID)
	var got gomsg.Envelope
	json.NewDecoder(resp3.Body).Decode(&got)
	resp3.Body.Close()
	require.NotNil(t, got.ConsumedAt, "consumed_at must be set after Consume")
}

// CW-20261001-0025: /messages takes the same address forms as /broker
// (CW-20261001-0014). from/to accept the URN string or the
// {kind, authority, id} object, and both decode to the same address.
func TestHTTP_Messages_SendObjectAddress(t *testing.T) {
	ts := setupMessagingServer(t)
	body := `{
        "kind": "notice",
        "from": {"kind":"agent","authority":"test","id":"alice"},
        "to":   {"kind":"agent","scope":"test","id":"bob","sub_id":"inbox"}
    }`
	resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var env gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	assert.Equal(t, "msg://agent/test/alice", env.From.URN())
	assert.Equal(t, "msg://agent/test/bob/inbox", env.To.URN())
}

func TestHTTP_Messages_ConsumeObjectRecipient(t *testing.T) {
	ts := setupMessagingServer(t)
	body := `{"kind":"notice","from":"msg://agent/test/src","to":"msg://agent/test/consume-obj"}`
	resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	var sent gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sent))
	resp.Body.Close()

	consumeBody := `{"recipient":{"kind":"agent","authority":"test","id":"consume-obj"}}`
	resp2, err := http.Post(ts.URL+"/api/v1/messages/"+sent.ID+"/consume", "application/json",
		strings.NewReader(consumeBody))
	require.NoError(t, err)
	resp2.Body.Close()
	require.Equal(t, http.StatusNoContent, resp2.StatusCode)

	resp3, err := http.Get(ts.URL + "/api/v1/messages/" + sent.ID)
	require.NoError(t, err)
	var got gomsg.Envelope
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&got))
	resp3.Body.Close()
	require.NotNil(t, got.ConsumedAt, "consumed_at must be set after Consume")
}

// A malformed address on any /messages body field is a 400 that names the
// field and both accepted shapes, rather than a decoder error about Go types.
func TestHTTP_Messages_MalformedAddress400(t *testing.T) {
	ts := setupMessagingServer(t)
	const shape = `must be a msg://<kind>/<authority>/<id> string or {"kind","authority","id"} object`
	const good = `"msg://agent/test/bob"`
	send := func(from, to string) string { return `{"kind":"notice","from":` + from + `,"to":` + to + `}` }
	consume := func(recipient string) string { return `{"recipient":` + recipient + `}` }
	// Consume parses the recipient before it looks the message up, so the
	// id need not exist.
	const sendPath, consumePath = "/api/v1/messages", "/api/v1/messages/any-id/consume"
	cases := []struct {
		name, path, body, field string
	}{
		{"send object missing id", sendPath, send(`{"kind":"agent","authority":"test"}`, good), "from"},
		{"send object unknown key", sendPath, send(`{"kind":"agent","authorty":"test","id":"alice"}`, good), "from"},
		{"send bad URN string", sendPath, send(`"agent/test/alice"`, good), "from"},
		{"send array", sendPath, send(`["msg://agent/test/alice"]`, good), "from"},
		{"send bad to names to", sendPath, send(good, `42`), "to"},
		{"consume object missing authority", consumePath, consume(`{"kind":"agent","id":"bob"}`), "recipient"},
		{"consume object unknown kind", consumePath, consume(`{"kind":"robot","authority":"test","id":"bob"}`), "recipient"},
		{"consume bad URN string", consumePath, consume(`"agent/test/bob"`), "recipient"},
		{"consume empty string", consumePath, consume(`""`), "recipient"},
		{"consume number", consumePath, consume(`7`), "recipient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			var out struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
			assert.Contains(t, out.Error, `"`+tc.field+`" `+shape)
		})
	}
}

// An absent or null address is missing (422), not a shape error.
func TestHTTP_Messages_NullOrAbsentAddressIsMissing(t *testing.T) {
	ts := setupMessagingServer(t)
	cases := []struct {
		name, path, body, want string
	}{
		{"send null from", "/api/v1/messages", `{"kind":"notice","from":null,"to":"msg://agent/test/bob"}`, "from is required"},
		{"send absent to", "/api/v1/messages", `{"kind":"notice","from":"msg://agent/test/alice"}`, "to is required"},
		{"consume absent recipient", "/api/v1/messages/any-id/consume", `{}`, "recipient is required"},
		{"consume null recipient", "/api/v1/messages/any-id/consume", `{"recipient":null}`, "recipient is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			var out struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
			assert.Equal(t, tc.want, out.Error)
		})
	}
}

func TestHTTP_Messages_RoutesReturn503WhenStoreNotWired(t *testing.T) {
	ts := noMessagingServer(t)

	endpoints := []struct {
		method, path string
		body         string
	}{
		{http.MethodPost, "/api/v1/messages", `{}`},
		{http.MethodGet, "/api/v1/messages/abc", ""},
		{http.MethodPost, "/api/v1/messages/abc/cancel", ""},
		{http.MethodPost, "/api/v1/messages/abc/consume", `{"recipient":"msg://agent/test/x"}`},
		{http.MethodGet, "/api/v1/messages/inbox?to=msg://agent/test/x", ""},
		{http.MethodGet, "/api/v1/messages/thread/TT", ""},
		{http.MethodGet, "/api/v1/messages/subscribe?to=msg://agent/test/x", ""},
	}
	for _, e := range endpoints {
		var req *http.Request
		var err error
		if e.body != "" {
			req, err = http.NewRequest(e.method, ts.URL+e.path, strings.NewReader(e.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req, err = http.NewRequest(e.method, ts.URL+e.path, nil)
		}
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
			"%s %s should 503", e.method, e.path)
		resp.Body.Close()
	}
}

// TestHTTP_Messages_SubscribeStreamsEnvelope verifies the SSE endpoint:
// after a subscriber connects, a fresh Send must surface as a `message`
// event on the stream.
func TestHTTP_Messages_SubscribeStreamsEnvelope(t *testing.T) {
	ts := setupMessagingServer(t)
	to := "msg://agent/test/sub-r1"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/v1/messages/subscribe?to="+url.QueryEscape(to), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	type result struct {
		env gomsg.Envelope
		err error
	}
	rc := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				rc <- result{err: err}
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			payload = strings.TrimSpace(payload)
			var env gomsg.Envelope
			if err := json.Unmarshal([]byte(payload), &env); err != nil {
				rc <- result{err: err}
				return
			}
			rc <- result{env: env}
			return
		}
	}()

	// Give the subscriber a moment to register before we Send.
	time.Sleep(50 * time.Millisecond)

	body := `{"kind":"notice","from":"msg://agent/test/src","to":"` + to + `"}`
	postResp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, postResp.StatusCode)
	postResp.Body.Close()

	select {
	case r := <-rc:
		require.NoError(t, r.err)
		assert.Equal(t, gomsg.MsgKindNotice, r.env.Kind)
		assert.Equal(t, to, r.env.To.URN())
	case <-ctx.Done():
		t.Fatal("subscriber did not receive envelope before deadline")
	}
}
