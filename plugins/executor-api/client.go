package executorapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// secretRE matches Anthropic / OpenAI key shapes. Alternation is leftmost-
// preference so the longest prefix wins (sk-ant-/sk-proj-/sk-) — single pass,
// no overlapping double-redaction.
var secretRE = regexp.MustCompile(`sk-(?:ant-|proj-)?[A-Za-z0-9_-]+`)

// vendorClient is the per-vendor adapter contract. RunTurn streams a single
// turn against the vendor's SDK, dispatching log/tool-use events to cb and
// returning final token usage. cb may be nil; implementations must handle that.
type vendorClient interface {
	RunTurn(ctx context.Context, profile config.AgentProfile, prompt, systemPrompt string, cb executor.EventCallback) (*executor.TokenUsage, error)
}

// clientFor selects the per-vendor adapter based on profile.Provider.
// Returns a PermanentError for unknown providers.
func clientFor(profile config.AgentProfile) (vendorClient, error) {
	switch strings.ToLower(strings.TrimSpace(profile.Provider)) {
	case "anthropic":
		return newAnthropicClient(profile)
	case "openai":
		return newOpenAIClient(profile)
	case "gemini":
		// Deferred — see CW-20260427-0043 follow-up. genai SDK pulls Azure +
		// AWS + cloud.google.com credential chains for an API-key-only use
		// case with no current consumer profile.
		return nil, executor.NewPermanentError(fmt.Errorf("executor-api: gemini support deferred until consumer profile lands; track via Phase E follow-up"))
	default:
		return nil, executor.NewPermanentError(fmt.Errorf("executor-api: unknown provider %q (supported: anthropic, openai)", profile.Provider))
	}
}

// apiKeyFor returns the API key for the given profile, preferring the
// profile-config value over the env-var fallback. Returns a PermanentError
// when neither path resolves so the scheduler doesn't retry endlessly.
func apiKeyFor(profile config.AgentProfile, envFallback string) (string, error) {
	if k := strings.TrimSpace(profile.APIKey); k != "" {
		return k, nil
	}
	if k := strings.TrimSpace(os.Getenv(envFallback)); k != "" {
		return k, nil
	}
	return "", executor.NewPermanentError(fmt.Errorf("executor-api: no API key for provider %q (set profile.api_key or env %s)", profile.Provider, envFallback))
}

// redactSecrets scrubs vendor-shaped API keys from a free-form error string.
// Vendor SDKs occasionally echo the key into error messages; this keeps it
// out of cost_ledger / scheduler logs / SSE feed. Not a substitute for not
// logging the key in the first place — defense in depth.
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	return secretRE.ReplaceAllStringFunc(s, func(m string) string {
		switch {
		case strings.HasPrefix(m, "sk-ant-"):
			return "sk-ant-REDACTED"
		case strings.HasPrefix(m, "sk-proj-"):
			return "sk-proj-REDACTED"
		default:
			return "sk-REDACTED"
		}
	})
}

// errorsAs is a thin wrapper around errors.As to keep the unwrap path local.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
