package modelcatalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func minimalCatalog() map[string]modelsdev.Provider {
	return map[string]modelsdev.Provider{
		"anthropic": {
			ID:   "anthropic",
			Name: "Anthropic",
			Env:  []string{"ANTHROPIC_API_KEY"},
			Models: map[string]modelsdev.Model{
				"claude-sonnet-4-6": {
					ID:     "claude-sonnet-4-6",
					Name:   "Claude Sonnet 4.6",
					Family: "claude",
					Cost:   modelsdev.Pricing{Input: 3.0, Output: 15.0},
					Limit:  modelsdev.Limits{ContextWindow: 200000, MaxOutputTokens: 8192},
					Capabilities: modelsdev.Capabilities{
						ToolCall:    true,
						Reasoning:   true,
						Attachment:  true,
						Temperature: true,
					},
				},
				"sparse-model": {
					ID:   "sparse-model",
					Name: "Sparse Model",
					// No pricing, no limits, no capabilities — used to assert
					// the wrapper returns ok=false when fields are missing.
				},
			},
		},
	}
}

func serveJSON(t *testing.T, payload any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func newTestCatalog(t *testing.T, srv *httptest.Server) *modelcatalog.Catalog {
	t.Helper()
	return modelcatalog.New(
		modelsdev.WithURL(srv.URL),
		modelsdev.WithHTTPClient(srv.Client()),
		modelsdev.WithCacheDir(t.TempDir()),
		modelsdev.WithCacheTTL(24*time.Hour),
	)
}

func TestGet(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	m, ok := c.Get("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	assert.Equal(t, "Claude Sonnet 4.6", m.Name)

	_, ok = c.Get("anthropic", "missing-model")
	assert.False(t, ok)

	_, ok = c.Get("missing-provider", "claude-sonnet-4-6")
	assert.False(t, ok)
}

func TestPricing(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	in, out, ok := c.Pricing("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	assert.InDelta(t, 3.0, in, 0.0001)
	assert.InDelta(t, 15.0, out, 0.0001)

	// Sparse model has zero pricing — ok must be false.
	_, _, ok = c.Pricing("anthropic", "sparse-model")
	assert.False(t, ok)
}

func TestContextWindow(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	w, ok := c.ContextWindow("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	assert.Equal(t, 200000, w)

	_, ok = c.ContextWindow("anthropic", "sparse-model")
	assert.False(t, ok)
}

func TestMaxOutput(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	mo, ok := c.MaxOutput("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	assert.Equal(t, 8192, mo)

	_, ok = c.MaxOutput("anthropic", "sparse-model")
	assert.False(t, ok)
}

func TestCapabilities(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	caps, ok := c.Capabilities("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	assert.True(t, caps.ToolCall)
	assert.True(t, caps.Reasoning)
	assert.True(t, caps.Attachment)
	assert.True(t, caps.Temperature)

	// Sparse model is in catalog but has zero capabilities — still ok=true.
	caps, ok = c.Capabilities("anthropic", "sparse-model")
	require.True(t, ok)
	assert.False(t, caps.ToolCall)

	_, ok = c.Capabilities("missing-provider", "x")
	assert.False(t, ok)
}

func TestEstimateCost(t *testing.T) {
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)
	require.NoError(t, c.Refresh(context.Background()))

	// 1M input tokens at $3 + 1M output tokens at $15 = $18.
	cost, ok := c.EstimateCost("anthropic", "claude-sonnet-4-6", 1_000_000, 1_000_000)
	require.True(t, ok)
	assert.InDelta(t, 18.0, cost, 0.0001)

	// 100k input + 50k output: 0.1 * 3 + 0.05 * 15 = 0.3 + 0.75 = 1.05.
	cost, ok = c.EstimateCost("anthropic", "claude-sonnet-4-6", 100_000, 50_000)
	require.True(t, ok)
	assert.InDelta(t, 1.05, cost, 0.0001)

	_, ok = c.EstimateCost("anthropic", "sparse-model", 1000, 1000)
	assert.False(t, ok)

	_, ok = c.EstimateCost("anthropic", "missing-model", 1000, 1000)
	assert.False(t, ok)
}

func TestColdLookupReturnsFalse(t *testing.T) {
	// No Refresh called and no cache on disk — every lookup degrades to ok=false.
	srv := serveJSON(t, minimalCatalog())
	defer srv.Close()

	c := newTestCatalog(t, srv)

	_, ok := c.Get("anthropic", "claude-sonnet-4-6")
	assert.False(t, ok)
	_, _, ok = c.Pricing("anthropic", "claude-sonnet-4-6")
	assert.False(t, ok)
	_, ok = c.ContextWindow("anthropic", "claude-sonnet-4-6")
	assert.False(t, ok)
	_, ok = c.MaxOutput("anthropic", "claude-sonnet-4-6")
	assert.False(t, ok)
	_, ok = c.Capabilities("anthropic", "claude-sonnet-4-6")
	assert.False(t, ok)
	_, ok = c.EstimateCost("anthropic", "claude-sonnet-4-6", 100, 100)
	assert.False(t, ok)
}
