package tetherclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEmptyIsNil(t *testing.T) {
	require.Nil(t, New("", "tok"))
	require.Nil(t, New("  ", ""))
}

func TestCapacityAndRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/capacity":
			require.Empty(t, r.URL.RawQuery, "Tether ignores environment_id, so none is sent")
			w.Write([]byte(`{"environment_id":"local","max_sessions":4,"active_sessions":4,"admission":"open"}`))
		case "/teams/runs/run-1":
			w.Write([]byte(`{"status":"running"}`))
		case "/teams/runs/run-2":
			w.Write([]byte(`{"status":"dissolved","ended_at":"2026-10-11T07:00:00Z","reason":"dissolve"}`))
		case "/teams/runs/run-500":
			w.WriteHeader(500)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "tok")
	ctx := context.Background()

	cap, err := c.Capacity(ctx)
	require.NoError(t, err)
	require.Equal(t, "local", cap.EnvironmentID)
	require.Equal(t, 4, cap.MaxSessions)
	require.Equal(t, "open", cap.Admission)

	r, err := c.Run(ctx, "run-1")
	require.NoError(t, err)
	require.False(t, r.Ended())
	r, err = c.Run(ctx, "run-2")
	require.NoError(t, err)
	require.True(t, r.Ended())

	_, err = c.Run(ctx, "gone")
	require.ErrorIs(t, err, ErrRunNotFound)
	_, err = c.Run(ctx, "run-500")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRunNotFound, "a server error is not evidence the run is gone")

	require.False(t, RunState{Status: "weird"}.Ended(), "unknown status is not proof of death")
}
