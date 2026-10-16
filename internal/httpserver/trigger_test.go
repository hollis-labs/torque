package httpserver_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func triggerServer(t *testing.T) (*httptest.Server, *sqlstore.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ts := httptest.NewServer(httpserver.New(service.New(store), nil))
	t.Cleanup(ts.Close)
	return ts, store
}

func lastTrigger(t *testing.T, s *sqlstore.Store, task string) (kind, ref string) {
	t.Helper()
	require.NoError(t, s.DB().QueryRow(
		`SELECT trigger_kind, json_extract(json_extract(envelope, '$.payload'), '$.trigger.ref')
		 FROM event_outbox WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, task).Scan(&kind, &ref))
	return
}

func post(t *testing.T, url, body string, headers map[string]string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300)
}

// The kind comes from the surface: the GUI marks its requests (X-Client:
// browser) and only a marked request is human. An unmarked API call is agent;
// there is no unknown kind.
func TestHTTPTrigger_GUIMarkerMakesAStateChangeHuman(t *testing.T) {
	ts, store := triggerServer(t)
	for _, id := range []string{"CW-HT-1", "CW-HT-2", "CW-HT-3", "CW-HT-4"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: id, Priority: 2}))
	}

	post(t, ts.URL+"/api/v1/tasks/CW-HT-1/transition", `{"status":"doing"}`, nil)
	kind, ref := lastTrigger(t, store, "CW-HT-1")
	assert.Equal(t, "agent", kind, "an unmarked API call is an agent call")
	assert.Equal(t, "http:POST /tasks/{id}/transition", ref)

	post(t, ts.URL+"/api/v1/tasks/CW-HT-2/transition", `{"status":"doing"}`, map[string]string{"X-Client": "browser"})
	kind, _ = lastTrigger(t, store, "CW-HT-2")
	assert.Equal(t, "human", kind, "the GUI marker makes it human")

	// A different X-Client value is not the GUI.
	post(t, ts.URL+"/api/v1/tasks/CW-HT-3/transition", `{"status":"doing"}`, map[string]string{"X-Client": "curl"})
	kind, _ = lastTrigger(t, store, "CW-HT-3")
	assert.Equal(t, "agent", kind)

	post(t, ts.URL+"/api/v1/tasks/bulk-transition", `{"ids":["CW-HT-4"],"status":"doing"}`, map[string]string{"X-Client": "browser"})
	kind, ref = lastTrigger(t, store, "CW-HT-4")
	assert.Equal(t, "human", kind)
	assert.Equal(t, "http:POST /tasks/bulk-transition", ref)
}
