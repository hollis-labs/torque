package executorapi

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// openaiClient wraps the official OpenAI Go SDK. Streaming dispatches text
// deltas as Log events; usage is captured from the final chunk when
// stream_options.include_usage = true.
type openaiClient struct {
	client    openai.Client
	maxTokens int64
}

// newOpenAIClient resolves the API key and constructs the SDK client.
func newOpenAIClient(profile config.AgentProfile) (vendorClient, error) {
	key, err := apiKeyFor(profile, "OPENAI_API_KEY")
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
	return &openaiClient{
		client:    openai.NewClient(opts...),
		maxTokens: maxTokens,
	}, nil
}

// RunTurn streams a chat completion and returns final token usage. Includes
// stream_options.include_usage so the final chunk carries CompletionUsage.
func (c *openaiClient) RunTurn(ctx context.Context, profile config.AgentProfile, prompt, systemPrompt string, cb executor.EventCallback) (*executor.TokenUsage, error) {
	messages := []openai.ChatCompletionMessageParamUnion{}
	if systemPrompt != "" {
		messages = append(messages, openai.SystemMessage(systemPrompt))
	}
	messages = append(messages, openai.UserMessage(prompt))

	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(profile.Model),
		Messages:            messages,
		MaxCompletionTokens: param.NewOpt(c.maxTokens),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if profile.Temperature != 0 {
		params.Temperature = param.NewOpt(profile.Temperature)
	}

	stream := c.client.Chat.Completions.NewStreaming(ctx, params)

	usage := &executor.TokenUsage{}

	for stream.Next() {
		chunk := stream.Current()
		// Per stream_options.include_usage, the final chunk has empty Choices
		// and a populated Usage field.
		if chunk.Usage.TotalTokens > 0 {
			usage.PromptTokens = int(chunk.Usage.PromptTokens)
			usage.CompletionTokens = int(chunk.Usage.CompletionTokens)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if cb != nil {
					cb(executor.LogEvent(choice.Delta.Content))
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return usage, classifyOpenAIError(err)
	}

	return usage, nil
}

// classifyOpenAIError maps SDK error shapes to PermanentError vs transient.
// Same policy as Anthropic: 4xx except 429 → permanent; 429/5xx/network → transient.
func classifyOpenAIError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.Error
	if !errorsAs(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != 429 {
		return executor.NewPermanentError(fmt.Errorf("openai %d: %s", apiErr.StatusCode, apiErr.Error()))
	}
	return err
}
