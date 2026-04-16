package waitpoll_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

func TestURLReachable_200_ReturnsTrue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	p := waitpoll.NewURLReachable(nil)
	assert.Equal(t, "url_reachable", p.Type())
	ok, err := p.Evaluate(context.Background(), map[string]any{"url": ts.URL})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestURLReachable_500_ReturnsFalse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	p := waitpoll.NewURLReachable(nil)
	ok, err := p.Evaluate(context.Background(), map[string]any{"url": ts.URL})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestURLReachable_404_ReturnsFalse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := waitpoll.NewURLReachable(nil)
	ok, err := p.Evaluate(context.Background(), map[string]any{"url": ts.URL})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestURLReachable_Unreachable_ReturnsFalseNoError(t *testing.T) {
	// Unreachable URL — connection refused; treat as "not yet" rather than a
	// predicate failure so a target coming online mid-poll transitions cleanly.
	p := waitpoll.NewURLReachable(nil)
	ok, err := p.Evaluate(context.Background(), map[string]any{"url": "http://127.0.0.1:1"})
	require.NoError(t, err, "unreachable host should not error — predicate is just not-yet")
	assert.False(t, ok)
}

func TestURLReachable_Validate(t *testing.T) {
	p := waitpoll.NewURLReachable(nil)
	require.Error(t, p.Validate(map[string]any{}))
	require.Error(t, p.Validate(map[string]any{"url": ""}))
	require.NoError(t, p.Validate(map[string]any{"url": "http://x"}))
}
