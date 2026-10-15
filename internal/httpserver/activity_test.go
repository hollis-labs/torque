package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityAgentsRelaysSnapshot(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, activitySnapshotPath, r.URL.Path)
		w.Write([]byte(`{"agents":[{"id":"claude:1:2"}]}`))
	}))
	defer src.Close()
	t.Setenv("TORQUE_ACTIVITY_URL", src.URL)

	rec := httptest.NewRecorder()
	(&Server{}).activityAgents(rec, httptest.NewRequest(http.MethodGet, "/api/v1/activity/agents", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"agents":[{"id":"claude:1:2"}]}`, rec.Body.String())
}

func TestActivityAgentsUnavailable(t *testing.T) {
	t.Setenv("TORQUE_ACTIVITY_URL", "http://127.0.0.1:1")
	rec := httptest.NewRecorder()
	(&Server{}).activityAgents(rec, httptest.NewRequest(http.MethodGet, "/api/v1/activity/agents", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
