package tetherclient

import (
	"context"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestForumsNativeJSONAndOperatorCredential(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer operator-secret", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-Author"))
		require.Equal(t, "/forums/portfolio/topics", r.URL.Path)
		require.Equal(t, "next-page", r.URL.Query().Get("cursor"))
		w.WriteHeader(201)
		w.Write([]byte(`{"topic":{"id":"topic-1"},"next":"hint"}`))
	}))
	defer upstream.Close()
	out, status, err := NewForums(upstream.URL, "operator-secret").Do(context.Background(), "POST", "/forums/portfolio/topics?cursor=next-page", []byte(`{"title":"Hello"}`))
	require.NoError(t, err)
	require.Equal(t, 201, status)
	require.JSONEq(t, `{"topic":{"id":"topic-1"},"next":"hint"}`, string(out))
}
func TestForumsErrorsAndMissingConfiguration(t *testing.T) {
	for _, status := range []int{400, 403, 404, 409, 500, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
				w.Write([]byte(`{"error":{"code":"failure","message":"operator-secret failed"}}`))
			}))
			defer upstream.Close()
			_, _, err := NewForums(upstream.URL, "operator-secret").Do(context.Background(), "GET", "/forums", nil)
			var fe *ForumsError
			require.ErrorAs(t, err, &fe)
			require.NotContains(t, err.Error(), "operator-secret")
			if status >= 500 || status < 400 {
				require.Equal(t, 503, fe.Status)
			} else {
				require.Equal(t, status, fe.Status)
			}
		})
	}
	for _, client := range []*ForumsClient{nil, NewForums("", "token"), NewForums("http://localhost:1", "")} {
		_, _, err := client.Do(context.Background(), "GET", "/forums", nil)
		var fe *ForumsError
		require.ErrorAs(t, err, &fe)
		require.Equal(t, 503, fe.Status)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>missing endpoint</html>`)) }))
	client := NewForums(upstream.URL, "secret")
	_, _, err := client.Do(context.Background(), "GET", "/forums", nil)
	require.Error(t, err)
	upstream.Close()
	_, _, err = client.Do(context.Background(), "GET", "/forums", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), upstream.URL)
}
func TestForumsUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "torque-forums-")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "t.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer operator", r.Header.Get("Authorization"))
		w.Write([]byte(`{"items":[],"has_more":false}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	_, status, err := NewForums("unix://"+sock, "operator").Do(context.Background(), "GET", "/forums", nil)
	require.NoError(t, err)
	require.Equal(t, 200, status)
}
