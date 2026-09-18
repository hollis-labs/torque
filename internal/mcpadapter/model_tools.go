package mcpadapter

import (
	"context"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
)

// briefModel drops the noisier fields (full modality + every cost variant) to
// keep the default list payload small. Verbose=true returns the full ModelRef.
type briefModel struct {
	ProviderID      string  `json:"provider_id"`
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Family          string  `json:"family,omitempty"`
	ContextWindow   int     `json:"context_window,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	InputCost       float64 `json:"input_cost,omitempty"`
	OutputCost      float64 `json:"output_cost,omitempty"`
	ToolCall        bool    `json:"tool_call,omitempty"`
	Reasoning       bool    `json:"reasoning,omitempty"`
}

func toBriefModel(m modelsdev.ModelRef) briefModel {
	return briefModel{
		ProviderID:      m.ProviderID,
		ID:              m.ID,
		Name:            m.Name,
		Family:          m.Family,
		ContextWindow:   m.Limit.ContextWindow,
		MaxOutputTokens: m.Limit.MaxOutputTokens,
		InputCost:       m.Cost.Input,
		OutputCost:      m.Cost.Output,
		ToolCall:        m.Capabilities.ToolCall,
		Reasoning:       m.Capabilities.Reasoning,
	}
}

func (a *Adapter) registerModelTools() {
	a.addTool(newTool("torque_models_list",
		withDescription(`List every (provider, model) pair in the models.dev catalog with pricing, context-window, and capability data.
Use to discover what's available before configuring a profile or estimating cost. Filter with provider="anthropic" to narrow. Cold cache returns an empty list — retry rather than treat absence as fatal. Brief shape drops cache pricing + modality; pass verbose="true" for the full record.
Response shape: data = {items: [<briefModel or ModelRef>...], meta: {truncated, returned, limit, hint?}}.
Example: {"provider":"anthropic","limit":"50"}`),
		withString("provider", desc("Filter to a single provider id (e.g. 'anthropic', 'openai')")),
		withString("verbose", desc("Return full ModelRef records instead of brief (string 'true'/'false', default false)")),
		withString("limit", desc("Max records to return (default 100, capped at 500)")),
	), a.handleModelsList)

	a.addTool(newTool("torque_models_get",
		withDescription(`Look up a single (provider, model) pair. Returns the full Model record.
Use when you need exact pricing or capability flags for a known model. Returns error.code=not_found on cold cache or unknown id.
Response shape: data = <Model> — singleton.
Example: {"provider":"anthropic","model":"claude-sonnet-4-6"}`),
		withString("provider", required(), desc("Provider id")),
		withString("model", required(), desc("Model id within the provider")),
	), a.handleModelsGet)
}

func (a *Adapter) handleModelsList(ctx context.Context, req map[string]any) (any, error) {
	if a.svc.Models == nil {
		return cappedJSONResult(nil, defaultGenericListLimit)
	}
	provider := reqStr(req, "provider")
	verbose := reqStrBool(req, "verbose")
	limit := clampLimit(reqInt(req, "limit"), defaultGenericListLimit, 500)

	all := a.svc.Models.List()
	out := make([]any, 0, len(all))
	for _, m := range all {
		if provider != "" && m.ProviderID != provider {
			continue
		}
		if verbose {
			out = append(out, m)
		} else {
			out = append(out, toBriefModel(m))
		}
	}
	return cappedJSONResult(out, limit)
}

func (a *Adapter) handleModelsGet(ctx context.Context, req map[string]any) (any, error) {
	if a.svc.Models == nil {
		return errResult(ErrCodeNotFound, "model catalog not initialized", "")
	}
	provider := reqStr(req, "provider")
	model := reqStr(req, "model")
	m, ok := a.svc.Models.Get(provider, model)
	if !ok {
		return errResult(ErrCodeNotFound, "model not found (cold cache or unknown id)", provider+"/"+model)
	}
	return okResult(m)
}
