package executorapi

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// APIExecutor implements executor.Executor by calling LLM APIs via a Provider.
type APIExecutor struct {
	profiles   config.ProfileMap
	providers  map[string]Provider
	toolRouter interface{}
}

// Option is a functional option for APIExecutor.
type Option func(*APIExecutor)

// WithProvider registers a named provider with the executor.
func WithProvider(name string, p Provider) Option {
	return func(e *APIExecutor) {
		e.providers[name] = p
	}
}

// New creates a new APIExecutor with the given profiles and options.
func New(profiles config.ProfileMap, toolRouter interface{}, opts ...Option) *APIExecutor {
	e := &APIExecutor{
		profiles:   profiles,
		providers:  make(map[string]Provider),
		toolRouter: toolRouter,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Compile-time interface check.
var _ executor.Executor = (*APIExecutor)(nil)

// Name returns the executor's registered name.
func (e *APIExecutor) Name() string {
	return "api"
}

// Capabilities reports what this executor supports.
func (e *APIExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: true,
	}
}

// Validate checks that the job is valid for this executor.
func (e *APIExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := resolveProfile(e.profiles, job)
	if profile.Provider == "" {
		return fmt.Errorf("agent profile %q has no provider set", job.AgentProfile)
	}
	return nil
}

// Run executes the job by calling the configured LLM provider.
func (e *APIExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	profile := resolveProfile(e.profiles, job)

	provider, ok := e.providers[profile.Provider]
	if !ok {
		return &executor.ExecutionResult{
			Status: "failed",
			Reason: fmt.Sprintf("provider %q not registered", profile.Provider),
		}, nil
	}

	// Determine timeout: profile overrides job limits.
	timeout := job.Limits.EffectiveTimeout()
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxTokens := profile.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	messages := buildMessages(profile, job)

	req := CompletionRequest{
		Model:        profile.Model,
		Messages:     messages,
		SystemPrompt: profile.SystemPrompt,
		MaxTokens:    maxTokens,
		Temperature:  profile.Temperature,
	}

	return runConversation(runCtx, provider, req, cb)
}
