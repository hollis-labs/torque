package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestHTTPSelfReport(t *testing.T) {
	ts := setupTestServer(t)
	post := func(path, body string, status int) map[string]any {
		t.Helper()
		resp, err := http.Post(ts.URL+"/api/v1"+path, "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		require.Equal(t, status, resp.StatusCode, out)
		return out
	}
	task := post("/tasks", `{"title":"reports","progress_detail":"step","checkpoint_mode":"blocking"}`, 201)
	path := "/tasks/" + task["id"].(string)
	first := post(path+"/progress", `{"label":"ready","idempotency_key":"p"}`, 200)
	require.Equal(t, true, first["emitted"])
	duplicate := post(path+"/progress", `{"label":"ready","idempotency_key":"p"}`, 200)
	require.Equal(t, "duplicate", duplicate["suppressed_reason"])
	require.Equal(t, first["event_id"], duplicate["event_id"])
	conflict := post(path+"/progress", `{"label":"different","idempotency_key":"p"}`, 409)
	require.Equal(t, "idempotency_conflict", conflict["code"])
	post(path+"/progress", `{"label":"ready","task_id":"another"}`, 400)
	data := post(path+"/data", `{"schema":"example@1","data":{"value":1},"idempotency_key":"d"}`, 200)
	require.NotEmpty(t, data["event_id"])
	repeat := post(path+"/data", `{"schema":"example@1","data":{"value":1},"idempotency_key":"d"}`, 200)
	require.Equal(t, true, repeat["duplicate"])
	post(path+"/data", `{"schema":"example@1","data":null}`, 400)
}

func TestHTTPSelfReportTriggerFromSurface(t *testing.T) {
	for _, route := range []struct{ path, body, kind string }{
		{"progress", `{"label":"ready","level":"milestone"}`, catalog.KindProgressCheckpoint},
		{"data", `{"schema":"example@1","data":{"value":1}}`, catalog.KindDataEmitted},
	} {
		for _, surface := range []struct{ name, marker, trigger string }{
			{"api", "", "agent"},
			{"gui", httpserver.GUIClientValue, "human"},
			{"other_client", "curl", "agent"},
		} {
			t.Run(route.path+"/"+surface.name, func(t *testing.T) {
				ts, store := triggerServer(t)
				const taskID = "CW-SELF-REPORT-TRIGGER"
				require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: "report trigger", Priority: 2}))
				post(t, ts.URL+"/api/v1/tasks/"+taskID+"/"+route.path, route.body, map[string]string{httpserver.GUIClientHeader: surface.marker})
				var trigger, kind string
				require.NoError(t, store.DB().QueryRow(`SELECT trigger_kind, kind FROM event_outbox WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID).Scan(&trigger, &kind))
				require.Equal(t, surface.trigger, trigger)
				require.Equal(t, route.kind, kind)
			})
		}
	}
}
