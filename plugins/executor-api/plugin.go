// Package executorapi implements clockwork's `api` executor by calling
// vendor-published Go SDKs directly (Anthropic, OpenAI, ...).
//
// Phase E (CW-20260427-0043) replaced the legacy HTTP-Provider stub + the
// CLOCKWORK_* stdout signal protocol with a `vendorClient` interface backed by
// real vendor SDK streaming. Per-task FSM transitions are driven by stream
// completion + tool-call MCP signaling (same as cliexec), not stdout parsing.
package executorapi

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// APIExecutor implements executor.Executor by dispatching to a per-provider
// vendorClient. The executor is the registry's `api` slot; per-vendor logic
// lives behind clientFor(profile).
type APIExecutor struct {
	profiles   config.ProfileMap
	toolRouter interface{}
	// clientFactory is overridable for tests; production path uses clientFor.
	clientFactory func(profile config.AgentProfile) (vendorClient, error)
}

// Option is a functional option for APIExecutor.
type Option func(*APIExecutor)

// WithClientFactory overrides the default per-vendor client constructor. Tests
// inject a fakeVendorClient via this seam; production code should not call it.
func WithClientFactory(f func(profile config.AgentProfile) (vendorClient, error)) Option {
	return func(e *APIExecutor) {
		e.clientFactory = f
	}
}

// New creates a new APIExecutor. toolRouter is reserved for the Plan 4 tool
// integration and currently unused.
func New(profiles config.ProfileMap, toolRouter interface{}, opts ...Option) *APIExecutor {
	e := &APIExecutor{
		profiles:      profiles,
		toolRouter:    toolRouter,
		clientFactory: clientFor,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Compile-time interface check.
var _ executor.Executor = (*APIExecutor)(nil)

// Name returns the executor's registered name.
func (e *APIExecutor) Name() string { return "api" }

// Capabilities reports what this executor supports. Streaming is exercised via
// the per-vendor SDK stream APIs. Sandbox is false (Phase B/D inheritance —
// restoration is bundled with CW-20260427-0059's loopback wiring).
func (e *APIExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       false, // Plan 4
		SupportsSandbox:     false,
		SupportsPermissions: true,
	}
}

// Validate runs at dispatch-time. Returns a PermanentError when the job points
// at a missing/misconfigured profile so the scheduler doesn't retry the row.
func (e *APIExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := config.GetProfileOrDefault(e.profiles, job.AgentProfile)
	if profile.Provider == "" {
		return executor.NewPermanentError(fmt.Errorf("agent profile %q has no provider set", job.AgentProfile))
	}
	if profile.Model == "" {
		return executor.NewPermanentError(fmt.Errorf("agent profile %q has no model set", job.AgentProfile))
	}
	if _, err := e.clientFactory(profile); err != nil {
		return err
	}
	return nil
}

// Run executes the job by dispatching to the per-vendor client.
func (e *APIExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	profile := config.GetProfileOrDefault(e.profiles, job.AgentProfile)

	client, err := e.clientFactory(profile)
	if err != nil {
		return &executor.ExecutionResult{
			Status: "failed",
			Reason: redactSecrets(err.Error()),
		}, nil
	}

	timeout := job.Limits.EffectiveTimeout()
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := job.Description
	systemPrompt := composeSystemPrompt(profile, job)

	usage, runErr := client.RunTurn(runCtx, profile, prompt, systemPrompt, cb)

	result := &executor.ExecutionResult{Status: "done"}
	if usage != nil {
		result.Tokens = *usage
		if cb != nil {
			cb(executor.TokenEvent(usage.PromptTokens, usage.CompletionTokens, usage.Cost))
		}
	}
	if runErr != nil {
		result.Status = "failed"
		result.Reason = redactSecrets(runErr.Error())
		// Surface PermanentError up so the scheduler stops retrying.
		var pe *executor.PermanentError
		if errorsAs(runErr, &pe) {
			return result, runErr
		}
	}
	return result, nil
}

// composeSystemPrompt prefers the per-task SystemPrompt, falling back to the
// profile-level default. Empty when neither is set.
func composeSystemPrompt(profile config.AgentProfile, job *executor.ExecutionJob) string {
	if job.SystemPrompt != "" {
		return job.SystemPrompt
	}
	return profile.SystemPrompt
}
