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
	var gotMethod, gotPath string
	var gotBody []byte
	var gotQuery map[string][]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod, gotPath = r.Method, r.URL.Path
		gotQuery = r.URL.Query()
		gotBody, _ = io.ReadAll(r.Body)
		require.Equal(t, "Bearer operator-secret", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-Author"))
		require.Empty(t, r.Header.Get("X-Session-ID"))
		require.Empty(t, r.URL.Query().Get("session_id"))
		if r.Method == "POST" {
			require.NotContains(t, string(gotBody), "author")
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

	// Management bodies remain native JSON, with per-operation field boundaries.
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/forums/categories", `{"id":"planning","namespace":"app/torque/forum/general","title":"Planning","description":"","sort_order":-10}`},
		{"POST", "/forums", `{"id":"work","namespace":"app/torque/forum/general","title":"Work","category_id":"planning","sort_order":0,"description":"Work boards"}`},
		{"PATCH", "/forums/categories/planning", `{"description":"","sort_order":0}`},
		{"PATCH", "/forums/work", `{"title":"Renamed","config_json":{},"metadata_json":{}}`},
		{"PATCH", "/forums/topics/t1", `{"status":"resolved"}`},
		{"PATCH", "/forums/posts/p1", `{"subject":"","attachments":[],"body_md":"Edited"}`},
		{"POST", "/forums/topics/t1/posts", `{"body_md":"Message","subject":"Subject","attachments":[{"name":"Design","uri":"https://example.com/design"}]}`},
		{"POST", "/forums/work/move", `{"destination_category_id":"","sort_order":0}`},
		{"POST", "/forums/work/clone", `{"id":"copy","title":"Copy","destination_category_id":"planning","sort_order":20}`},
		{"POST", "/forums/topics/t1/move", `{"destination_forum_id":"work"}`},
		{"POST", "/forums/topics/t1/clone", `{"destination_forum_id":"work","title":"Copy"}`},
		{"POST", "/forums/topics/t1/fork", `{"post_id":"p1","destination_forum_id":"work","title":"Fork"}`},
		{"POST", "/forums/posts/p1/move", `{"destination_topic_id":"t2","parent_post_id":"p2"}`},
		{"POST", "/forums/posts/p1/clone", `{"destination_topic_id":"t2"}`},
		{"POST", "/forums/work/participants", `{"principal_kind":"agent","principal_id":"urn:agent:manager","access":"post"}`},
		{"POST", "/forums/work/participants/revoke", `{"principal_kind":"agent","principal_id":"urn:agent:manager"}`},
		{"POST", "/forums/work/owner", `{}`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			out := call(tc.method, tc.path, tc.body)
			require.Less(t, out.Code, 300, out.Body.String())
			require.Equal(t, tc.method, gotMethod)
			require.Equal(t, tc.path, gotPath)
			require.JSONEq(t, tc.body, string(gotBody))
		})
	}
	for _, path := range []string{"/forums/categories/planning", "/forums/work", "/forums/topics/t1", "/forums/posts/p1"} {
		require.Equal(t, 200, call("DELETE", path, "").Code)
		require.Equal(t, "DELETE", gotMethod)
		require.Equal(t, path, gotPath)
		require.JSONEq(t, `{}`, string(gotBody))
		require.Equal(t, 200, call("DELETE", path, `{"cascade":true}`).Code)
		require.JSONEq(t, `{"cascade":true}`, string(gotBody))
		if !strings.Contains(path, "/posts/") {
			for _, action := range []string{"archive", "unarchive"} {
				require.Equal(t, 201, call("POST", path+"/"+action, `{}`).Code)
				require.Equal(t, path+"/"+action, gotPath)
			}
		}
	}
	for _, path := range []string{"/forums", "/forums/categories", "/forums/work/topics"} {
		require.Equal(t, 200, call("GET", path+"?namespace=app%2Ftorque%2Fforum%2Fgeneral&category_id=&archived=false&session_id=spoof", "").Code)
		require.Equal(t, []string{"app/torque/forum/general"}, gotQuery["namespace"])
		require.Equal(t, []string{""}, gotQuery["category_id"])
		require.Equal(t, []string{"false"}, gotQuery["archived"])
	}
	for _, path := range []string{"/forums/categories/planning", "/forums/work/participants"} {
		require.Equal(t, 200, call("GET", path, "").Code)
		require.Equal(t, path, gotPath)
	}
	before = calls
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/forums/posts/p1", `{"body_md":"x","provenance":{}}`},
		{"PATCH", "/forums/posts/p1", `{"body_md":"x","session_id":"spoof"}`},
		{"PATCH", "/forums/work", `{"owner_id":"spoof"}`},
		{"POST", "/forums/work/archive", `{"cascade":true}`},
		{"POST", "/forums/posts/p1/move", `{"destination_topic_id":"t2","author":"spoof"}`},
		{"DELETE", "/forums/work", `{"title":"x"}`},
		{"DELETE", "/forums/work", `null`},
		{"DELETE", "/forums/work", `{} {}`},
		{"PATCH", "/forums/work", ``},
	} {
		require.Equal(t, 400, call(tc.method, tc.path, tc.body).Code)
	}
	require.Equal(t, before, calls)
	svc.Forums.Client = nil
	degraded := call("GET", "/forums", "")
	require.Equal(t, 503, degraded.Code)
	require.Contains(t, degraded.Body.String(), "tether_unavailable")
}
