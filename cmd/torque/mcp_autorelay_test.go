package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoRelayEndpoint(t *testing.T) {
	var gotURL string
	probe := func(u, _ string) bool { gotURL = u; return true }
	assert.Equal(t, "http://127.0.0.1:8990/mcp", autoRelayEndpoint(8990, "", probe))
	assert.Equal(t, "http://127.0.0.1:8990/api/v1/scheduler/status", gotURL)

	assert.Equal(t, "", autoRelayEndpoint(8990, "", func(string, string) bool { return false }),
		"no daemon: serve from the local database")
}

func TestDaemonListening(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	assert.True(t, daemonListening(ok.URL, "tok"))
	assert.True(t, daemonListening(ok.URL, ""), "401 still means a daemon is there: relay and surface the auth error")
	url := ok.URL
	ok.Close()
	assert.False(t, daemonListening(url, "tok"), "closed port")
	assert.False(t, daemonListening(strings.Repeat("x", 3), ""), "unusable URL")
}
