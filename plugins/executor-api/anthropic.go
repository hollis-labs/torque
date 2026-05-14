package executorapi

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// anthropicClient wraps the official Anthropic Go SDK. Streaming dispatches
// content-block deltas as Log events and accumulates usage from the final
// MessageDelta event's Usage field.
type anthropicClient struct {
	client    anthropic.Client
	maxTokens int64
}

// newAnthropicClient resolves the API key and constructs the SDK client.
// MaxTokens defaults to 4096 to match the legacy stub's default.
func newAnthropicClient(profile config.AgentProfile) (vendorClient, error) {
	key, err := apiKeyFor(profile, "ANTHROPIC_API_KEY")
	if err != nil {
		return nil, err
	}
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if profile.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(profile.BaseURL))
	}
	maxTokens := int64(profile.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &anthropicClient{
		client:    anthropic.NewClient(opts...),
		maxTokens: maxTokens,
	}, nil
}

// RunTurn streams a single user turn through the Messages API and returns
// final token usage. Cache-creation and cache-read input tokens are folded
// into PromptTokens so cost backfill (models.dev) sees the full prompt-side
// load — Anthropic separates these and dropping them would understate cost.
func (c *anthropicClient) RunTurn(ctx context.Context, profile config.AgentProfile, prompt, systemPrompt string, cb executor.EventCallback) (*executor.TokenUsage, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(profile.Model),
		MaxTokens: c.maxTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	}
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: systemPrompt}}
	}
	if profile.Temperature != 0 {
		params.Temperature = anthropic.Float(profile.Temperature)
	}

	stream := c.client.Messages.NewStreaming(ctx, params)

	usage := &executor.TokenUsage{}

	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "content_block_delta":
			delta := event.AsContentBlockDelta()
			if td := delta.Delta.AsTextDelta(); td.Text != "" {
				if cb != nil {
					cb(executor.LogEvent(td.Text))
				}
			}
		case "message_start":
			ms := event.AsMessageStart()
			usage.PromptTokens = int(ms.Message.Usage.InputTokens +
				ms.Message.Usage.CacheCreationInputTokens +
				ms.Message.Usage.CacheReadInputTokens)
		case "message_delta":
			md := event.AsMessageDelta()
			// MessageDeltaUsage carries running output token count.
			usage.CompletionTokens = int(md.Usage.OutputTokens)
		}
	}
	if err := stream.Err(); err != nil {
		return usage, classifyAnthropicError(err)
	}

	return usage, nil
}

// classifyAnthropicError maps SDK error shapes to PermanentError vs transient.
// 400 (invalid_request_error), 401 (authentication_error), 403 (permission_error),
// 404 (not_found_error) are permanent. 429 (rate_limit_error), 500+, network
// failures are transient (default — return as-is so the scheduler retries).
func classifyAnthropicError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if !errorsAs(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != 429 {
		return executor.NewPermanentError(fmt.Errorf("anthropic %d: %s", apiErr.StatusCode, apiErr.Error()))
	}
	return err
}
