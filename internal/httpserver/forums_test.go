package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForumsHTTPContractAndIdentityBoundary(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer operator-secret", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-Author"))
		require.Empty(t, r.Header.Get("X-Session-ID"))
		require.Empty(t, r.URL.Query().Get("session_id"))
		if r.Method == "POST" {
			data, _ := io.ReadAll(r.Body)
			require.NotContains(t, string(data), "author")
			w.WriteHeader(201)
			w.Write([]byte(`{"post":{"id":"p1"},"next":"expand"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/search") {
			require.Equal(t, "literal words", r.URL.Query().Get("query"))
			require.Equal(t, "agent", r.URL.Query().Get("author_kind"))
			require.Equal(t, "100", r.URL.Query().Get("since_seq"))
			require.Equal(t, "cursor-2", r.URL.Query().Get("cursor"))
		}
		w.Write([]byte(`{"items":[{"id":"p1"}],"has_more":true,"next_cursor":"next","last_seq":101}`))
	}))
	defer upstream.Close()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	defer store.Close()
	svc := service.New(store)
	svc.Forums.Client = tetherclient.NewForums(upstream.URL, "operator-secret")
	server := httpserver.New(svc, nil)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost"
		req.Header.Set("Authorization", "Bearer browser-session")
		req.Header.Set("X-Author", "spoof")
		req.Header.Set("X-Session-ID", "spoof-session")
		out := httptest.NewRecorder()
		server.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, 404, call("GET", "/forums", "").Code)
	require.Zero(t, calls)
	require.NoError(t, svc.Feature.Enable("forums"))
	for _, path := range []string{"/forums", "/forums/portfolio", "/forums/portfolio/topics", "/forums/topics/t1", "/forums/topics/t1/posts", "/forums/posts/p1?detail=full", "/forums/portfolio/search?query=literal+words&author_kind=agent&since_seq=100&cursor=cursor-2&session_id=spoof"} {
		out := call("GET", path, "")
		require.Equal(t, 200, out.Code, out.Body.String())
		var page map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &page))
		require.Equal(t, "next", page["next_cursor"])
	}
	for _, input := range []struct{ path, body string }{{"/forums", `{"id":"pilot","namespace":"app/torque/forum/pilot","title":"Pilot"}`}, {"/forums/portfolio/topics", `{"title":"Topic"}`}, {"/forums/topics/t1/posts", `{"body_md":"Reply","parent_post_id":"p0"}`}} {
		require.Equal(t, 201, call("POST", input.path, input.body).Code)
	}
	before := calls
	for _, body := range []string{`{"body_md":"x","author":"spoof"}`, `{"body_md":"x","session_id":"spoof"}`, `{"body_md":"x","provenance":{}}`, `{"body_md":"x"} {}`, `null`, `[]`} {
		require.Equal(t, 400, call("POST", "/forums/topics/t1/posts", body).Code)
	}
	require.Equal(t, before, calls)
	svc.Forums.Client = nil
	degraded := call("GET", "/forums", "")
	require.Equal(t, 503, degraded.Code)
	require.Contains(t, degraded.Body.String(), "tether_unavailable")
}
