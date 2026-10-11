package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enablePlannerFeature(t *testing.T, baseURL string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/v1/settings/features.planner", bytes.NewBufferString(`{"value":"true"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// plannerCall sends a JSON request under /api/v1/planner and decodes the JSON
// reply (object or array) into out, which may be nil.
func plannerCall(t *testing.T, method, base, path string, body any, headers map[string]string) (int, any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+"/api/v1/planner"+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotEqual(t, byte('<'), firstByte(raw), "got HTML (SPA fallback?) for %s %s: %.120s", method, path, raw)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out), "body: %s", raw)
	return resp.StatusCode, out
}

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func TestHTTP_Planner_DisabledIs404(t *testing.T) {
	ts := setupTestServer(t)
	status, body := plannerCall(t, http.MethodGet, ts.URL, "/databases", nil, nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "feature_disabled", obj(body)["code"])
}

func TestHTTP_Planner_CRUDAndRevConflict(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)

	status, body := plannerCall(t, http.MethodPost, ts.URL, "/risks", map[string]any{"title": "R", "kind": "gap", "severity": "high"}, map[string]string{"X-Client": "browser"})
	require.Equal(t, http.StatusCreated, status, "%v", body)
	risk := obj(body)
	assert.Equal(t, "RK-001", risk["id"])
	assert.Equal(t, "chrispian", risk["author"], "the browser client is the owner")

	status, body = plannerCall(t, http.MethodPost, ts.URL, "/risks", map[string]any{"title": "S", "kind": "gap", "severity": "low"}, nil)
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, "portfolio-manager", obj(body)["author"])

	status, body = plannerCall(t, http.MethodGet, ts.URL, "/risks?severity=high", nil, nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, body, 1)
	status, body = plannerCall(t, http.MethodGet, ts.URL, "/risks?sort=-title", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "S", body.([]any)[0].(map[string]any)["title"])

	status, body = plannerCall(t, http.MethodPatch, ts.URL, "/risks/RK-001", map[string]any{"patch": map[string]any{"status": "mitigating"}, "rev": 1}, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.EqualValues(t, 2, obj(body)["rev"])

	status, body = plannerCall(t, http.MethodPatch, ts.URL, "/risks/RK-001", map[string]any{"patch": map[string]any{"status": "closed"}, "rev": 1}, nil)
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "conflict", obj(body)["code"])
	assert.Equal(t, "mitigating", obj(obj(obj(body)["details"])["current"])["status"], "a conflict carries the current item")

	status, body = plannerCall(t, http.MethodPatch, ts.URL, "/risks/RK-001", map[string]any{"patch": map[string]any{"severity": "apocalyptic"}}, nil)
	require.Equal(t, http.StatusBadRequest, status)
	errs := obj(obj(body)["details"])["errors"].([]any)
	assert.Equal(t, "severity", errs[0].(map[string]any)["path"])

	status, _ = plannerCall(t, http.MethodPatch, ts.URL, "/risks/RK-001", map[string]any{"rev": 2}, nil)
	assert.Equal(t, http.StatusBadRequest, status, "patch is required")
	status, _ = plannerCall(t, http.MethodGet, ts.URL, "/risks/RK-404", nil, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = plannerCall(t, http.MethodGet, ts.URL, "/nope", nil, nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestHTTP_Planner_StaticRoutesAreNotShadowed(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)

	status, body := plannerCall(t, http.MethodGet, ts.URL, "/databases", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, body, 7)

	status, body = plannerCall(t, http.MethodGet, ts.URL, "/schema/roadmap", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, obj(obj(body)["enums"])["horizon"], "someday")

	plannerCall(t, http.MethodPost, ts.URL, "/inbox", map[string]any{"title": "findable thing"}, nil)
	status, body = plannerCall(t, http.MethodGet, ts.URL, "/search?q=findable", nil, nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, body, 1)
	status, _ = plannerCall(t, http.MethodGet, ts.URL, "/search?q=", nil, nil)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestHTTP_Planner_CommentsLinksAndRelations(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)
	plannerCall(t, http.MethodPost, ts.URL, "/ideas", map[string]any{"title": "idea", "kind": "idea"}, nil)
	plannerCall(t, http.MethodPost, ts.URL, "/workstreams", map[string]any{"title": "ws"}, nil)

	status, body := plannerCall(t, http.MethodPost, ts.URL, "/ideas/ID-idea/comments", map[string]any{"text": "hello"}, map[string]string{"X-Client": "browser"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	c := obj(body)["comments"].([]any)[0].(map[string]any)
	assert.Equal(t, "c-1", c["id"])
	assert.Equal(t, "chrispian", c["author"])

	status, body = plannerCall(t, http.MethodPost, ts.URL, "/links", map[string]any{
		"from": map[string]any{"db": "ideas", "id": "ID-idea"}, "to": map[string]any{"db": "workstreams", "id": "WS-ws"}, "field": "workstream_ids"}, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []any{"WS-ws"}, obj(body)["workstream_ids"])
	status, body = plannerCall(t, http.MethodDelete, ts.URL, "/links", map[string]any{
		"from": map[string]any{"db": "ideas", "id": "ID-idea"}, "to": map[string]any{"db": "workstreams", "id": "WS-ws"}, "field": "workstream_ids"}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, obj(body), "workstream_ids")

	status, body = plannerCall(t, http.MethodPost, ts.URL, "/ideas/ID-idea/links", map[string]any{"link": map[string]any{"kind": "torque", "ref": "CW-20261011-0050"}}, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	status, _ = plannerCall(t, http.MethodPost, ts.URL, "/ideas/ID-idea/links", map[string]any{"link": map[string]any{"kind": "torque", "ref": "CW-20261011-0050"}}, nil)
	assert.Equal(t, http.StatusConflict, status, "duplicate pointer")
	status, _ = plannerCall(t, http.MethodPost, ts.URL, "/ideas/ID-idea/links", map[string]any{"link": map[string]any{"kind": "url", "ref": "javascript:alert(1)"}}, nil)
	assert.Equal(t, http.StatusBadRequest, status, "only http(s) urls")
	status, body = plannerCall(t, http.MethodDelete, ts.URL, "/ideas/ID-idea/links", map[string]any{"kind": "torque", "ref": "CW-20261011-0050"}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, obj(body), "links")
	status, _ = plannerCall(t, http.MethodDelete, ts.URL, "/ideas/ID-idea/links", map[string]any{"kind": "torque", "ref": "CW-20261011-0050"}, nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestHTTP_Planner_ReorderDecisionsAndInbox(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)
	for _, title := range []string{"a", "b", "c"} {
		plannerCall(t, http.MethodPost, ts.URL, "/inbox", map[string]any{"title": title}, nil)
	}
	status, body := plannerCall(t, http.MethodPost, ts.URL, "/inbox/reorder", map[string]any{"ids": []string{"IN-003", "IN-001"}}, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "IN-003", body.([]any)[0].(map[string]any)["id"])
	status, _ = plannerCall(t, http.MethodPost, ts.URL, "/inbox/reorder", map[string]any{"ids": []any{1}}, nil)
	assert.Equal(t, http.StatusBadRequest, status)

	plannerCall(t, http.MethodPost, ts.URL, "/decisions", map[string]any{"title": "pick", "options": []any{map[string]any{"id": "a", "label": "Alpha"}}}, nil)
	status, body = plannerCall(t, http.MethodPost, ts.URL, "/decisions/DEC-001/defer", nil, nil) // empty body
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "deferred", obj(body)["status"])
	status, _ = plannerCall(t, http.MethodPost, ts.URL, "/decisions/DEC-001/decide", map[string]any{"option": "zzz"}, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	status, body = plannerCall(t, http.MethodPost, ts.URL, "/decisions/DEC-001/decide", map[string]any{"option": "a", "comment": "yes"}, map[string]string{"X-Client": "browser"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "chrispian", obj(body)["decided_by"])
	status, body = plannerCall(t, http.MethodPost, ts.URL, "/decisions/DEC-001/reopen", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "needs-decision", obj(body)["status"])

	status, body = plannerCall(t, http.MethodPost, ts.URL, "/inbox/IN-002/promote", map[string]any{"to_db": "risks", "fields": map[string]any{"severity": "high"}}, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "promoted", obj(obj(body)["inbox"])["status"])
	assert.Equal(t, "high", obj(obj(body)["item"])["severity"])
	status, _ = plannerCall(t, http.MethodPost, ts.URL, "/inbox/IN-002/promote", map[string]any{"to_db": "risks"}, nil)
	assert.Equal(t, http.StatusConflict, status)
	status, body = plannerCall(t, http.MethodPost, ts.URL, "/inbox/IN-001/dismiss", map[string]any{"note": "no"}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "dismissed", obj(body)["status"])
}

func TestHTTP_Planner_Board(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)
	plannerCall(t, http.MethodPost, ts.URL, "/inbox", map[string]any{"title": "triage me"}, nil)
	plannerCall(t, http.MethodPost, ts.URL, "/risks", map[string]any{"title": "big", "kind": "risk", "severity": "high"}, nil)

	status, body := plannerCall(t, http.MethodGet, ts.URL, "/board", nil, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	b := obj(body)
	assert.Equal(t, []any{"RK-001", "IN-001"}, []any{
		obj(obj(b["sections"])["holds"].([]any)[0])["id"],
		obj(obj(b["sections"])["holds"].([]any)[1])["id"],
	}, "/board is not shadowed by /{db}")
	assert.EqualValues(t, 72, b["recent_hours"])
	assert.EqualValues(t, 30, b["limit"])
	assert.Equal(t, []any{}, b["notices"])

	status, body = plannerCall(t, http.MethodGet, ts.URL, "/board?limit=1&recent_hours=6&active_hours=1", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.EqualValues(t, 1, obj(body)["limit"])
	assert.EqualValues(t, 6, obj(body)["recent_hours"])
	assert.EqualValues(t, 1, obj(body)["active_hours"])

	for _, q := range []string{"?limit=abc", "?limit=0x", "?recent_hours=soon", "?limit=500", "?recent_hours=-3"} {
		status, body = plannerCall(t, http.MethodGet, ts.URL, "/board"+q, nil, nil)
		assert.Equal(t, http.StatusBadRequest, status, q)
		assert.Equal(t, "invalid", obj(body)["code"], q)
	}
}

func TestHTTP_Planner_TorqueReferences(t *testing.T) {
	ts := setupTestServer(t)
	enablePlannerFeature(t, ts.URL)

	taskResp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(`{"title":"Referenced task","description":"x","executor":"cli","tags":["ws-demo"]}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, taskResp.StatusCode)
	var task map[string]any
	require.NoError(t, json.NewDecoder(taskResp.Body).Decode(&task))
	taskResp.Body.Close()
	id := task["id"].(string)

	status, body := plannerCall(t, http.MethodGet, ts.URL, "/torque-titles?ids="+id+",CW-20200101-9999", nil, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	titles := obj(body)
	assert.Equal(t, "Referenced task", obj(titles[id])["title"])
	assert.Nil(t, titles["CW-20200101-9999"])
	assert.Contains(t, titles, "CW-20200101-9999")
	status, body = plannerCall(t, http.MethodGet, ts.URL, "/torque-titles?ids=nope", nil, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "invalid", obj(body)["code"])
	status, body = plannerCall(t, http.MethodGet, ts.URL, "/torque-titles", nil, nil)
	require.Equal(t, http.StatusOK, status, "no ids is an empty answer, not a database called torque-titles")
	assert.Empty(t, obj(body))

	plannerCall(t, http.MethodPost, ts.URL, "/workstreams", map[string]any{"title": "Demo", "torque_tag": "ws-demo"}, nil)
	status, body = plannerCall(t, http.MethodGet, ts.URL, "/workstreams/WS-demo/tasks", nil, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	ws := obj(body)
	assert.Equal(t, "ws-demo", ws["torque_tag"])
	assert.EqualValues(t, 1, ws["total"])
	open := ws["open"].([]any)
	require.Len(t, open, 1)
	assert.Equal(t, id, obj(open[0])["id"])
	status, _ = plannerCall(t, http.MethodGet, ts.URL, "/workstreams/WS-nope/tasks", nil, nil)
	assert.Equal(t, http.StatusNotFound, status)
}
