// Package modelcatalog wraps go-modelsdev with provider+model-keyed lookups
// shaped for Clockwork's needs. It is the single point in the codebase that
// owns the modelsdev.Client lifecycle and the only sanctioned way to read
// pricing, context-window, and capability data.
//
// The wrapper returns zero values + ok=false when the catalog is cold or the
// (provider, model) pair is unknown so callers can degrade gracefully — no
// caller is expected to wait on the catalog before serving.
package modelcatalog

import (
	"context"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
)

// Catalog is a thin wrapper over modelsdev.Client.
type Catalog struct {
	client *modelsdev.Client
}

// New returns a Catalog with the default models.dev client. Pass modelsdev
// options (WithCacheDir, WithURL, WithCacheTTL, WithHTTPClient) to override
// defaults — primarily used by tests.
func New(opts ...modelsdev.Option) *Catalog {
	return &Catalog{client: modelsdev.New(opts...)}
}

// Start launches the background refresher. Returns immediately; cancel ctx
// to stop the refresh loop.
func (c *Catalog) Start(ctx context.Context) {
	c.client.StartRefresher(ctx)
}

// Refresh fetches the catalog synchronously. Mainly for cold-start tests.
func (c *Catalog) Refresh(ctx context.Context) error {
	return c.client.Refresh(ctx)
}

// Get returns the underlying Model for (providerID, modelID), or false if
// either provider or model is unknown.
func (c *Catalog) Get(providerID, modelID string) (modelsdev.Model, bool) {
	return c.client.Get(providerID, modelID)
}

// List returns every (provider, model) pair in the catalog, sorted by
// provider then model id. Returns an empty slice on a cold cache.
func (c *Catalog) List() []modelsdev.ModelRef {
	return c.client.List()
}

// ListProviders returns every provider in the catalog, sorted by id.
// Returns an empty slice on a cold cache.
func (c *Catalog) ListProviders() []modelsdev.Provider {
	return c.client.ListProviders()
}

// Pricing returns input and output USD-per-million-tokens. Returns
// 0, 0, false when the model is unknown OR both prices are zero.
func (c *Catalog) Pricing(providerID, modelID string) (input, output float64, ok bool) {
	m, found := c.client.Get(providerID, modelID)
	if !found {
		return 0, 0, false
	}
	if m.Cost.Input == 0 && m.Cost.Output == 0 {
		return 0, 0, false
	}
	return m.Cost.Input, m.Cost.Output, true
}

// ContextWindow returns the model's context window in tokens. Returns
// 0, false when the model is unknown or the window is not set.
func (c *Catalog) ContextWindow(providerID, modelID string) (int, bool) {
	m, found := c.client.Get(providerID, modelID)
	if !found || m.Limit.ContextWindow == 0 {
		return 0, false
	}
	return m.Limit.ContextWindow, true
}

// MaxOutput returns the model's max output tokens. Returns 0, false when
// the model is unknown or the limit is not set.
func (c *Catalog) MaxOutput(providerID, modelID string) (int, bool) {
	m, found := c.client.Get(providerID, modelID)
	if !found || m.Limit.MaxOutputTokens == 0 {
		return 0, false
	}
	return m.Limit.MaxOutputTokens, true
}

// Capabilities returns the model's capability flags. Returns the zero
// Capabilities + false when the model is unknown.
func (c *Catalog) Capabilities(providerID, modelID string) (modelsdev.Capabilities, bool) {
	m, found := c.client.Get(providerID, modelID)
	if !found {
		return modelsdev.Capabilities{}, false
	}
	return m.Capabilities, true
}

// EstimateCost computes the USD cost for the given prompt and completion
// token counts using the catalog's per-million-token pricing. Returns
// 0, false when the model is unknown or pricing is missing.
func (c *Catalog) EstimateCost(providerID, modelID string, promptTokens, completionTokens int) (float64, bool) {
	in, out, ok := c.Pricing(providerID, modelID)
	if !ok {
		return 0, false
	}
	cost := float64(promptTokens)*in/1_000_000 + float64(completionTokens)*out/1_000_000
	return cost, true
}
