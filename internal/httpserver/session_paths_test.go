package httpserver

import (
	"bytes"
	"encoding/json"
	"github.com/hollis-labs/torque/internal/testutil/sessionpaths"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionLaunchHTTP_PathPolicy(t *testing.T) {
	root, cases := sessionpaths.Fixture(t)
	for _, tc := range cases {
		for _, field := range []string{"workdir", "repo_root"} {
			t.Run(tc.Name+"/"+field, func(t *testing.T) {
				srv, _, rt := launchSessionFixture(t, root)
				body, err := json.Marshal(map[string]any{"agent_profile": "default", "workdir": root, "repo_root": root, field: tc.Path, "env": []string{"TORQUE_SESSION_ALLOWED_ROOTS=" + tc.Path}})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/sessions/launch", bytes.NewReader(body))
				req.RemoteAddr = "127.0.0.1:1234"
				response := httptest.NewRecorder()
				srv.ServeHTTP(response, req)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "TORQUE_SESSION_ALLOWED_ROOTS")
				require.Contains(t, response.Body.String(), tc.Rule)
				require.Nil(t, rt.lastStart, "a refused launch reached the runtime")
			})
		}
	}
}
