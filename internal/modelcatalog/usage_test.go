package modelcatalog_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// models.dev prices as the catalog carried them on 2026-10-01.
var (
	gpt55    = modelsdev.Pricing{Input: 5, Output: 30, CacheRead: 0.5}
	sonnet45 = modelsdev.Pricing{Input: 3, Output: 15, CacheWrite: 3.75, CacheRead: 0.3}
)

// Tonight's runs (CW-20260912-0003, comment 6685) as fixtures. Codex input
// counts its cached tokens; Claude's does not.
func TestPriceUsage(t *testing.T) {
	cases := []struct {
		name  string
		price modelsdev.Pricing
		u     modelcatalog.UsageTokens
		want  float64
	}{
		{
			// The task's own case: 21,843,564 input tokens, 21,239,936 of
			// them cached, recorded at $110.96 by pricing them all as input.
			name: "run 1079, codex gpt-5.5", price: gpt55,
			u:    modelcatalog.UsageTokens{Input: 21_843_564, CacheRead: 21_239_936, Output: 58_037, InputIncludesCacheRead: true},
			want: 15.379218,
		},
		{
			// Recorded $0.31596 token-only; three turns, 40,064 of 58,212
			// input tokens cached.
			name: "run 1140, codex gpt-5.5", price: gpt55,
			u:    modelcatalog.UsageTokens{Input: 58_212, CacheRead: 40_064, Output: 830, InputIncludesCacheRead: true},
			want: 0.135672,
		},
		{
			// Claude itself reported $0.1904: it bills these cache writes
			// at the 1-hour tier (2x input). The catalog's cache_write is
			// the 5-minute tier, so the estimate is the lower figure, and
			// provider cost comes first wherever Claude reports it.
			name: "run 1189, claude sonnet-4.5", price: sonnet45,
			u:    modelcatalog.UsageTokens{Input: 36, Output: 1_804, CacheWrite: 20_565, CacheRead: 132_711},
			want: 0.14410005,
		},
		{
			name: "input including more cache reads than it holds is clamped at zero", price: gpt55,
			u:    modelcatalog.UsageTokens{Input: 10, CacheRead: 100, InputIncludesCacheRead: true},
			want: 100 * 0.5 / 1_000_000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fallback, ok := modelcatalog.PriceUsage(tc.price, tc.u)
			require.True(t, ok)
			assert.False(t, fallback)
			assert.InDelta(t, tc.want, got, 1e-9)
		})
	}

	t.Run("a missing cache price falls back to the input price, flagged", func(t *testing.T) {
		got, fallback, ok := modelcatalog.PriceUsage(modelsdev.Pricing{Input: 30, Output: 180}, modelcatalog.UsageTokens{Input: 1_000_000, CacheRead: 1_000_000, InputIncludesCacheRead: true})
		require.True(t, ok)
		assert.True(t, fallback)
		assert.InDelta(t, 30.0, got, 1e-9, "the cached million is priced as input, the uncached remainder is 0")
	})
	t.Run("no prices is not a price", func(t *testing.T) {
		_, _, ok := modelcatalog.PriceUsage(modelsdev.Pricing{}, modelcatalog.UsageTokens{Input: 1})
		assert.False(t, ok)
	})
}

func TestSplitModelID(t *testing.T) {
	for _, tc := range []struct{ provider, model, wantProvider, wantModel string }{
		{"opencode", "opencode/claude-sonnet-4-5", "opencode", "claude-sonnet-4-5"},
		{"opencode", "anthropic/claude-sonnet-4-5", "anthropic", "claude-sonnet-4-5"},
		{"opencode", "claude-sonnet-4-5", "opencode", "claude-sonnet-4-5"},
		{"openrouter", "anthropic/claude-sonnet-4-5", "openrouter", "anthropic/claude-sonnet-4-5"},
		{"anthropic", "claude-sonnet-4-5", "anthropic", "claude-sonnet-4-5"},
	} {
		p, m := modelcatalog.SplitModelID(tc.provider, tc.model)
		assert.Equal(t, [2]string{tc.wantProvider, tc.wantModel}, [2]string{p, m}, "%s %s", tc.provider, tc.model)
	}
}

// An opencode profile's "opencode/<model>" resolves to models.dev's
// opencode provider; a plain id resolves where it is.
func TestEstimateUsageCost(t *testing.T) {
	srv := serveJSON(t, map[string]modelsdev.Provider{
		"opencode":  {ID: "opencode", Models: map[string]modelsdev.Model{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", Cost: sonnet45}}},
		"openai":    {ID: "openai", Models: map[string]modelsdev.Model{"gpt-5.5": {ID: "gpt-5.5", Cost: gpt55}}},
		"anthropic": {ID: "anthropic", Models: map[string]modelsdev.Model{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", Cost: sonnet45}}},
	})
	defer srv.Close()
	cat := newTestCatalog(t, srv)
	require.NoError(t, cat.Refresh(context.Background()))

	got, _, ok := cat.EstimateUsageCost("opencode", "opencode/claude-sonnet-4-5", modelcatalog.UsageTokens{Input: 1_000_000})
	require.True(t, ok)
	assert.InDelta(t, 3.0, got, 1e-9)

	got, _, ok = cat.EstimateUsageCost("openai", "gpt-5.5", modelcatalog.UsageTokens{Input: 58_212, CacheRead: 40_064, Output: 830, InputIncludesCacheRead: true})
	require.True(t, ok)
	assert.InDelta(t, 0.135672, got, 1e-9)

	_, _, ok = cat.EstimateUsageCost("opencode", "opencode/not-a-model", modelcatalog.UsageTokens{Input: 1})
	assert.False(t, ok)
}
