package tetherclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnixSocketBaseURL(t *testing.T) {
	sock := filepath.Join(os.TempDir(), "tc-test-"+t.Name()+".sock")
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer os.Remove(sock)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"environment_id":"local","max_sessions":0,"active_sessions":3,"admission":"open"}`))
	})}
	go srv.Serve(l)
	defer srv.Close()

	for _, u := range []string{"unix:" + sock, "unix://" + sock} {
		c := New(u, "")
		cap, err := c.Capacity(context.Background(), "")
		require.NoError(t, err, u)
		require.Equal(t, 3, cap.ActiveSessions)
	}
}

func TestAPIErrorEnvelopeAndCapacityRefusal(t *testing.T) {
	code := "execution_paused"
	status := 409
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(`{"error":{"code":"` + code + `","message":"paused"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	_, err := c.LaunchSession(context.Background(), "s1")
	require.True(t, IsCapacityRefusal(err))
	code = "conflict"
	_, err = c.LaunchSession(context.Background(), "s1")
	require.False(t, IsCapacityRefusal(err), "wrong-state 409 is not a capacity refusal")
	status, code = 404, "not_found"
	require.True(t, IsNotFound(c.StopSession2(t)))
	require.NoError(t, c.StopSession(context.Background(), "s1"), "stopping an already-gone session is fine")
}

// StopSession2 returns the raw error of a stop (StopSession swallows 404).
func (c *Client) StopSession2(t *testing.T) error {
	_, err := c.do(context.Background(), c.HTTP, http.MethodPost, "/sessions/s1/stop", nil, nil)
	return err
}

func TestTurnOutputDecode(t *testing.T) {
	e := Event{Kind: EventTurnOutput, PayloadJSON: `{"turn_id":"t1","kind":"final","text":"hi"}`}
	to, ok := e.TurnOutput()
	require.True(t, ok)
	require.Equal(t, "final", to.Kind)
	_, ok = Event{Kind: EventStateChanged}.TurnOutput()
	require.False(t, ok)
}
