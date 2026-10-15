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

func TestActivityStreamRelaysEventsAndOnlyTheCursor(t *testing.T) {
	var gotQuery, gotAuth, gotLast string
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, activityStreamPath, r.URL.Path)
		gotQuery, gotAuth, gotLast = r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("id: 7\nevent: session.idle\ndata: {}\n\n"))
	}))
	defer src.Close()
	t.Setenv("TORQUE_ACTIVITY_URL", src.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/stream?since=5&kind=session&secret=x", nil)
	req.Header.Set("Authorization", "Bearer torque-token")
	req.Header.Set("Last-Event-ID", "6")
	rec := httptest.NewRecorder()
	(&Server{}).activityStream(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "event: session.idle")
	assert.Equal(t, "kind=session&since=5", gotQuery)
	assert.Empty(t, gotAuth, "the Torque token must not reach the activity host")
	assert.Equal(t, "6", gotLast)
}

func TestActivityStreamUnavailable(t *testing.T) {
	t.Setenv("TORQUE_ACTIVITY_URL", "http://127.0.0.1:1")
	rec := httptest.NewRecorder()
	(&Server{}).activityStream(rec, httptest.NewRequest(http.MethodGet, "/api/v1/activity/stream", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
