package mcpadapter

import (
	"context"

	"github.com/hollis-labs/substrate/llm-core/modelsdev"
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
Response shape: data = {items: [<briefModel or ModelRef>...], meta: {truncated, returned, limit, has_more, next_cursor, total?, offset?, next_offset?, hint?}}.
Example: {"provider":"anthropic","limit":"50"}`),
		withString("provider", desc("Filter to a single provider id (e.g. 'anthropic', 'openai')")),
		withString("verbose", desc("Return full ModelRef records instead of brief (string 'true'/'false', default false)")),
		withString("limit", desc("Max records to return (default 50, capped at 200)")),
		withResourcePageParams("models"),
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
	q, err := reqResourceQuery(req)
	if err != nil {
		return nil, err
	}
	provider, err := reqQueryString(req, "provider")
	if err != nil {
		return nil, err
	}
	search, err := reqQueryString(req, "search")
	if err != nil {
		return nil, err
	}
	page, err := a.svc.ListModelPage(provider, search, q)
	if err != nil {
		return errFromService(err)
	}
	more := len(page.Rows) > page.Query.Limit
	if more {
		page.Rows = page.Rows[:page.Query.Limit]
	}
	items := make([]any, 0, len(page.Rows))
	for _, row := range page.Rows {
		m := row.Record.(modelsdev.ModelRef)
		if reqStrBool(req, "verbose") {
			items = append(items, m)
		} else {
			items = append(items, toBriefModel(m))
		}
	}
	offset := []int{}
	if page.Offset != nil {
		offset = append(offset, *page.Offset)
	}
	return cappedCursorJSONResultWithTotal(items, page.Query.Limit, page.Total, page.Query.SortBy, page.Query.SortDir, more, func(i int) (string, string) { return page.Rows[i].SortValue, page.Rows[i].ID }, offset...)
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
