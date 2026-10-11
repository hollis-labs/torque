package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postJSON(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// With no scheduler the go switch is unreadable, so claim-next must defer as
// paused rather than hand out work.
func TestWorkstreamClaim_DefersPausedWithoutGo(t *testing.T) {
	ts := setupTestServer(t)

	resp, out := postJSON(t, ts.URL+"/api/v1/workstreams/claim", map[string]any{"team_run_id": "run-1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "defer", out["outcome"])
	assert.Equal(t, "paused", out["reason"])

	resp, out = postJSON(t, ts.URL+"/api/v1/workstreams/claim", map[string]any{})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "refuse", out["outcome"])
}

func TestWorkstreamRelease_UnknownClaimIs404(t *testing.T) {
	ts := setupTestServer(t)
	resp, _ := postJSON(t, ts.URL+"/api/v1/workstreams/claims/clm_nope/release", map[string]any{"outcome": "failed"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = postJSON(t, ts.URL+"/api/v1/workstreams/claims/clm_nope/heartbeat", map[string]any{})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestWorkstreamClaimsList(t *testing.T) {
	ts := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/workstreams/claims")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string][]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, []any{}, out["items"])

	bad, err := http.Get(ts.URL + "/api/v1/workstreams/claims?status=nope")
	require.NoError(t, err)
	defer bad.Body.Close()
	assert.Equal(t, http.StatusBadRequest, bad.StatusCode)
}
