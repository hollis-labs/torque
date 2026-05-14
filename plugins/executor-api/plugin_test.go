package executorapi

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// fakeVendorClient is the test seam for plugin-level tests. Per-vendor SDK
// behavior (anthropic.go / openai.go) is not exercised here — those need
// HTTP-transport mocking and are best covered by manual e2e against real keys.
type fakeVendorClient struct {
	usage   *executor.TokenUsage
	err     error
	logEcho string
}

func (f *fakeVendorClient) RunTurn(ctx context.Context, profile config.AgentProfile, prompt, systemPrompt string, cb executor.EventCallback) (*executor.TokenUsage, error) {
	if cb != nil && f.logEcho != "" {
		cb(executor.LogEvent(f.logEcho))
	}
	return f.usage, f.err
}

func testProfiles() config.ProfileMap {
	return config.ProfileMap{
		"default": {
			Executor: "api",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
	}
}

func testJob() *executor.ExecutionJob {
	return &executor.ExecutionJob{
		TaskID:       "task-1",
		Description:  "do something useful",
		AgentProfile: "default",
	}
}

func newWithFake(profiles config.ProfileMap, fake *fakeVendorClient) *APIExecutor {
	return New(profiles, nil, WithClientFactory(func(p config.AgentProfile) (vendorClient, error) {
		return fake, nil
	}))
}

func TestName(t *testing.T) {
	assert.Equal(t, "api", New(testProfiles(), nil).Name())
}

func TestCapabilities(t *testing.T) {
	caps := New(testProfiles(), nil).Capabilities()
	assert.True(t, caps.SupportsStreaming)
	assert.True(t, caps.SupportsTools, "Plan 4 (CW-20260503-0015) wired the tool-broker")
	assert.False(t, caps.SupportsSandbox, "Phase B/D inheritance; restored with CW-20260427-0059")
	assert.True(t, caps.SupportsPermissions)
	assert.False(t, caps.SupportsResume, "CW-20260512-0059: API executor has no native conversation-resume; reactor falls back to fresh-boot")
}

func TestValidate_Valid(t *testing.T) {
	e := newWithFake(testProfiles(), &fakeVendorClient{})
	require.NoError(t, e.Validate(testJob()))
}

func TestValidate_MissingTaskID(t *testing.T) {
	e := newWithFake(testProfiles(), &fakeVendorClient{})
	job := testJob()
	job.TaskID = ""
	err := e.Validate(job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskID")
}

func TestValidate_NoProvider(t *testing.T) {
	profiles := config.ProfileMap{"default": {Executor: "api", Model: "claude-sonnet-4-6"}}
	e := New(profiles, nil)
	err := e.Validate(testJob())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe), "missing provider must be permanent")
}

func TestValidate_NoModel(t *testing.T) {
	profiles := config.ProfileMap{"default": {Executor: "api", Provider: "anthropic"}}
	e := New(profiles, nil)
	err := e.Validate(testJob())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe), "missing model must be permanent")
}

func TestRun_SuccessCapturesTokens(t *testing.T) {
	fake := &fakeVendorClient{
		usage:   &executor.TokenUsage{PromptTokens: 100, CompletionTokens: 50},
		logEcho: "hello world",
	}
	e := newWithFake(testProfiles(), fake)

	var events []executor.ExecutionEvent
	cb := func(ev executor.ExecutionEvent) { events = append(events, ev) }

	result, err := e.Run(context.Background(), testJob(), cb)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, 100, result.Tokens.PromptTokens)
	assert.Equal(t, 50, result.Tokens.CompletionTokens)

	var sawLog, sawTokens bool
	for _, ev := range events {
		if ev.Type == executor.EventLog {
			sawLog = true
		}
		if ev.Type == executor.EventTokenUsage {
			sawTokens = true
		}
	}
	assert.True(t, sawLog, "delta event should reach callback")
	assert.True(t, sawTokens, "token-usage event should reach callback")
}

func TestRun_PermanentErrorPropagates(t *testing.T) {
	fake := &fakeVendorClient{err: executor.NewPermanentError(errors.New("invalid api key"))}
	e := newWithFake(testProfiles(), fake)

	result, err := e.Run(context.Background(), testJob(), nil)
	require.Error(t, err, "permanent errors must surface so the scheduler stops retrying")
	assert.Equal(t, "failed", result.Status)
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestRun_TransientErrorDoesNotPropagate(t *testing.T) {
	fake := &fakeVendorClient{err: errors.New("temporary network blip")}
	e := newWithFake(testProfiles(), fake)

	result, err := e.Run(context.Background(), testJob(), nil)
	require.NoError(t, err, "transient errors should land in result.Reason; the scheduler retry policy decides")
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "temporary network blip")
}

func TestRun_RedactsAPIKeyFromReason(t *testing.T) {
	fake := &fakeVendorClient{err: errors.New("auth failed: key sk-ant-abc123XYZ-toolongstring is invalid")}
	e := newWithFake(testProfiles(), fake)

	result, _ := e.Run(context.Background(), testJob(), nil)
	assert.NotContains(t, result.Reason, "abc123XYZ")
	assert.Contains(t, result.Reason, "REDACTED")
}

func TestClientFor_KnownProviders(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")

	for _, prov := range []string{"anthropic", "openai", "ANTHROPIC", " openai "} {
		t.Run(prov, func(t *testing.T) {
			c, err := clientFor(config.AgentProfile{Provider: prov, Model: "x"})
			require.NoError(t, err)
			assert.NotNil(t, c)
		})
	}
}

func TestClientFor_GeminiDeferred(t *testing.T) {
	_, err := clientFor(config.AgentProfile{Provider: "gemini", Model: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deferred")
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestClientFor_UnknownProvider(t *testing.T) {
	_, err := clientFor(config.AgentProfile{Provider: "bogus", Model: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestAPIKeyFor_ProfileWinsOverEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	key, err := apiKeyFor(config.AgentProfile{Provider: "anthropic", APIKey: "from-profile"}, "ANTHROPIC_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "from-profile", key)
}

func TestAPIKeyFor_EnvFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	key, err := apiKeyFor(config.AgentProfile{Provider: "anthropic"}, "ANTHROPIC_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "from-env", key)
}

func TestAPIKeyFor_NeitherSet(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	_, err := apiKeyFor(config.AgentProfile{Provider: "anthropic"}, "ANTHROPIC_API_KEY")
	require.Error(t, err)
	var pe *executor.PermanentError
	assert.True(t, errors.As(err, &pe), "missing key must be permanent — retry won't help")
}

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"anthropic key", "auth: sk-ant-abc123xyz failed", "auth: sk-ant-REDACTED failed"},
		{"openai project key", "bearer sk-proj-deadbeef-cafe denied", "bearer sk-proj-REDACTED denied"},
		{"openai user key", "key sk-livedeadbeef rejected", "key sk-REDACTED rejected"},
		{"no secret", "connection refused", "connection refused"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, redactSecrets(tc.in))
		})
	}
}
