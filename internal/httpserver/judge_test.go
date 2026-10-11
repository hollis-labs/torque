package httpserver_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgeServer(t *testing.T) (*httptest.Server, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	ts := httptest.NewServer(httpserver.New(svc, nil))
	t.Cleanup(ts.Close)
	return ts, svc
}

func judgeCall(t *testing.T, method, url string, body any) (int, map[string]any, []any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client", "browser")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var arr []any
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &arr)
	} else {
		_ = json.Unmarshal(raw, &obj)
	}
	return resp.StatusCode, obj, arr
}

func TestSuggestionsOverHTTP(t *testing.T) {
	ts, svc := judgeServer(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Fix the crash", Description: "broken"})
	require.NoError(t, err)
	d := judge.Scripted{Label: "scripted", Fn: func(judge.Request) (judge.Response, error) {
		return judge.Response{Answers: map[string]judge.Answer{"kind": {Type: judge.Choice, Choice: "bug", Probabilities: map[string]float64{"bug": 0.9}, Confidence: 0.9}}}, nil
	}}
	_, err = svc.Judge.EnrichTask(context.Background(), d, "task-kind", task.ID, true)
	require.NoError(t, err)

	code, _, list := judgeCall(t, "GET", ts.URL+"/api/v1/judge/suggestions?task_id="+task.ID, nil)
	require.Equal(t, 200, code)
	require.Len(t, list, 1)
	id := int64(list[0].(map[string]any)["id"].(float64))

	code, obj, _ := judgeCall(t, "GET", ts.URL+"/api/v1/judge/status", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, false, obj["active"])

	// A bulk call without a limit is refused.
	code, _, _ = judgeCall(t, "POST", ts.URL+"/api/v1/judge/suggestions/bulk", map[string]any{"action": "accept"})
	assert.Equal(t, 400, code)

	code, obj, _ = judgeCall(t, "POST", fmt.Sprintf("%s/api/v1/judge/suggestions/%d/accept", ts.URL, id), nil)
	require.Equal(t, 200, code)
	assert.Equal(t, []any{"bug"}, obj["added_tags"])

	// Accepting again is a conflict, not a second write.
	code, _, _ = judgeCall(t, "POST", fmt.Sprintf("%s/api/v1/judge/suggestions/%d/accept", ts.URL, id), nil)
	assert.Equal(t, 409, code)

	// No decider installed: enrich says so instead of spending.
	code, _, _ = judgeCall(t, "POST", ts.URL+"/api/v1/tasks/"+task.ID+"/enrich", nil)
	assert.Equal(t, 400, code)
}
