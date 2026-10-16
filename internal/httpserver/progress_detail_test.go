package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPProgressDetailTemplateAndTask(t *testing.T) {
	ts := setupTestServer(t)
	tpl := httpCreateTemplate(t, ts.URL, `{"id":"detail","name":"Detail","progress_detail":"step"}`)
	require.Equal(t, "step", tpl["progress_detail"])
	resp, err := http.Post(ts.URL+"/api/v1/templates/detail/instantiate", "application/json", bytes.NewBufferString(`{"title":"child"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var task map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&task))
	resp.Body.Close()
	require.Equal(t, "step", task["progress_detail"])
	require.Equal(t, "template", task["progress_detail_source"])
	id := task["id"].(string)
	for _, level := range []string{"step", ""} {
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"progress_detail":"`+level+`"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
		resp, err = http.Get(ts.URL + "/api/v1/tasks/" + id)
		require.NoError(t, err)
		task = map[string]any{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&task))
		resp.Body.Close()
		if level == "" {
			require.Nil(t, task["progress_detail"])
			require.Nil(t, task["progress_detail_source"])
		} else {
			require.Equal(t, level, task["progress_detail"])
			require.Equal(t, "task", task["progress_detail_source"])
		}
	}
}
