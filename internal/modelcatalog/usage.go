package modelcatalog

import (
	"strings"

	"github.com/hollis-labs/substrate/llm-core/modelsdev"
)

// UsageTokens are a run's token counts as its runtime reported them, for a
// cache-aware price (CW-20260912-0003).
type UsageTokens struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	// InputIncludesCacheRead says Input already counts the cache reads, as
	// OpenAI's (codex's) usage does. Anthropic's (claude-code) and
	// opencode's do not: their cache reads come on top of Input.
	InputIncludesCacheRead bool
}

// PriceUsage prices u in USD at p's per-million-token rates: uncached input
// at the input price, cache reads at the cache-read price, cache writes at
// the cache-write price, output at the output price. When Input includes the
// cache reads, only the remainder (never below zero) is priced as input.
// A missing cache price falls back to the input price, and cacheFallback
// says so, since that overstates a cache read; the catalog's cache-write
// price is the provider's default tier (Anthropic's 5-minute one).
// ok is false when the pricing has neither an input nor an output price.
func PriceUsage(p modelsdev.Pricing, u UsageTokens) (cost float64, cacheFallback bool, ok bool) {
	if p.Input == 0 && p.Output == 0 {
		return 0, false, false
	}
	uncached := u.Input
	if u.InputIncludesCacheRead {
		uncached = max(0, u.Input-u.CacheRead)
	}
	readPrice, writePrice := p.CacheRead, p.CacheWrite
	if readPrice == 0 && u.CacheRead > 0 {
		readPrice, cacheFallback = p.Input, true
	}
	if writePrice == 0 && u.CacheWrite > 0 {
		writePrice, cacheFallback = p.Input, true
	}
	cost = (float64(uncached)*p.Input +
		float64(u.CacheRead)*readPrice +
		float64(u.CacheWrite)*writePrice +
		float64(u.Output)*p.Output) / 1_000_000
	return cost, cacheFallback, true
}

// EstimateUsageCost prices u for (providerID, modelID) with PriceUsage. An
// opencode model id, "<provider>/<model>", is looked up as that provider's
// model (SplitModelID). ok is false when the model is unknown or unpriced.
func (c *Catalog) EstimateUsageCost(providerID, modelID string, u UsageTokens) (cost float64, cacheFallback bool, ok bool) {
	providerID, modelID = SplitModelID(providerID, modelID)
	m, found := c.client.Get(providerID, modelID)
	if !found {
		return 0, false, false
	}
	return PriceUsage(m.Cost, u)
}

// SplitModelID resolves an opencode model id, which opencode always spells
// "<provider>/<model>", into that catalog provider and model: models.dev
// keys opencode's own models as provider "opencode", so
// "opencode/claude-sonnet-4-5" is that entry, and "anthropic/claude-sonnet-4-5"
// Anthropic's. Any other provider's id is used as given: a slash there can
// be part of the model's own name.
func SplitModelID(providerID, modelID string) (string, string) {
	if providerID != "opencode" {
		return providerID, modelID
	}
	if prefix, rest, found := strings.Cut(modelID, "/"); found && prefix != "" && rest != "" {
		return prefix, rest
	}
	return providerID, modelID
}
