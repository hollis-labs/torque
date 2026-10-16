package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthzPublicJSON(t *testing.T) {
	for _, token := range []string{"", "test-api-token"} {
		t.Run("token="+token, func(t *testing.T) {
			// Liveness does not depend on the service/database or scheduler.
			handler := httpserver.New(nil, nil).
				WithSecurity(httpserver.Security{Token: token}).
				WithBuildInfo("1.2.3", "abc123")
			ts := httptest.NewServer(handler)
			defer ts.Close()

			resp := do(t, http.MethodGet, ts.URL+"/healthz", nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
			var body map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, map[string]string{"status": "ok", "version": "1.2.3", "commit": "abc123"}, body)

			if token != "" {
				api := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", nil)
				assert.Equal(t, http.StatusUnauthorized, api.StatusCode)
			}
			for _, path := range []string{"/", "/planner", "/tasks/example"} {
				spa := do(t, http.MethodGet, ts.URL+path, nil)
				assert.Equal(t, http.StatusOK, spa.StatusCode, path)
				assert.Contains(t, spa.Header.Get("Content-Type"), "text/html", path)
			}
		})
	}
}

func TestHealthzDefaultBuildInfo(t *testing.T) {
	handler := httpserver.New(nil, nil).WithBuildInfo("", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok","version":"dev","commit":"unknown"}`, w.Body.String())
}
