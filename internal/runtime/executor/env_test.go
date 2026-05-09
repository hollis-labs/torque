package executor_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

func TestFilterEnvStripsSecrets(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"PATH=/usr/bin",
		"AWS_SECRET_ACCESS_KEY=supersecret123",
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"DATABASE_URL=postgres://localhost/db",
		"MY_APP_SECRET=topsecret",
		"CLOCKWORK_DB_PATH=clockwork.db",
	}

	filtered, stripped := executor.FilterEnv(env, executor.FilterEnvOpts{})
	assert.NotContains(t, filtered, "AWS_SECRET_ACCESS_KEY=supersecret123")
	assert.NotContains(t, filtered, "MY_APP_SECRET=topsecret")
	assert.Contains(t, filtered, "HOME=/home/user")
	assert.Contains(t, filtered, "PATH=/usr/bin")
	assert.Contains(t, filtered, "CLOCKWORK_DB_PATH=clockwork.db")

	assert.Contains(t, stripped, "AWS_SECRET_ACCESS_KEY")
	assert.Contains(t, stripped, "MY_APP_SECRET")

	// Provider auth env vars (ANTHROPIC_API_KEY, OPENAI_API_KEY, GITHUB_TOKEN,
	// etc.) are tested separately in TestFilterEnv_ProviderAuthVarsPassThrough
	// — they MUST survive FilterEnv per CW-20260509-0011 because the spawned
	// subprocess IS the LLM provider CLI and the variable is its documented
	// auth mechanism (esp. bare-mode claude).
}

// TestFilterEnv_ProviderAuthVarsPassThrough is the CW-20260509-0011
// regression test. Provider auth env vars must survive FilterEnv even
// though their names match the secret-pattern (API_KEY / TOKEN). Without
// this passthrough, bare-mode claude in the daemon-spawned subprocess
// fails with "Not logged in · Please run /login" because --bare's
// documented auth contract is `ANTHROPIC_API_KEY or apiKeyHelper via
// --settings (OAuth and keychain are never read)`.
func TestFilterEnv_ProviderAuthVarsPassThrough(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"ANTHROPIC_API_KEY=sk-ant-api03-test",
		"ANTHROPIC_AUTH_TOKEN=sk-ant-oat01-test",
		"OPENAI_API_KEY=sk-openai-test",
		"GEMINI_API_KEY=gem-test",
		"GOOGLE_API_KEY=goog-test",
		"GITHUB_TOKEN=ghp_test",
		"GH_TOKEN=ghp_alt_test",
		// Negative case: a similarly-named non-allowlisted secret stays stripped.
		"MY_CUSTOM_API_KEY=should-be-stripped",
	}

	filtered, stripped := executor.FilterEnv(env, executor.FilterEnvOpts{})

	for _, kv := range []string{
		"ANTHROPIC_API_KEY=sk-ant-api03-test",
		"ANTHROPIC_AUTH_TOKEN=sk-ant-oat01-test",
		"OPENAI_API_KEY=sk-openai-test",
		"GEMINI_API_KEY=gem-test",
		"GOOGLE_API_KEY=goog-test",
		"GITHUB_TOKEN=ghp_test",
		"GH_TOKEN=ghp_alt_test",
	} {
		assert.Contains(t, filtered, kv,
			"%s must pass through FilterEnv per CW-20260509-0011 provider-auth allowlist", kv)
	}

	for _, key := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GITHUB_TOKEN", "GH_TOKEN",
	} {
		assert.NotContains(t, stripped, key,
			"%s must NOT appear in the stripped list", key)
	}

	// Negative case: similar-shaped non-allowlisted secret IS still stripped.
	assert.NotContains(t, filtered, "MY_CUSTOM_API_KEY=should-be-stripped",
		"only the documented portfolio provider auth vars are allowlisted; ad-hoc API_KEY-named vars stay stripped")
	assert.Contains(t, stripped, "MY_CUSTOM_API_KEY")
}

func TestFilterEnvStripsPrefixes(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"CODEX_API_KEY=xxx",
		"CODEX_CONFIG=/tmp/cfg",
	}

	filtered, stripped := executor.FilterEnv(env, executor.FilterEnvOpts{
		StripPrefixes: []string{"CODEX_"},
	})
	assert.Contains(t, filtered, "HOME=/home/user")
	assert.NotContains(t, filtered, "CODEX_API_KEY=xxx")
	assert.NotContains(t, filtered, "CODEX_CONFIG=/tmp/cfg")
	assert.Contains(t, stripped, "CODEX_API_KEY")
	assert.Contains(t, stripped, "CODEX_CONFIG")
}

func TestFilterEnvAddsExtraVars(t *testing.T) {
	env := []string{"HOME=/home/user"}

	filtered, _ := executor.FilterEnv(env, executor.FilterEnvOpts{
		ExtraVars: []string{
			"CLOCKWORK_TASK_ID=CW-20260407-0001",
			"CLOCKWORK_AGENT_DEPTH=1",
		},
	})
	assert.Contains(t, filtered, "HOME=/home/user")
	assert.Contains(t, filtered, "CLOCKWORK_TASK_ID=CW-20260407-0001")
	assert.Contains(t, filtered, "CLOCKWORK_AGENT_DEPTH=1")
}

func TestFilterEnvAddsGUIPreventionVars(t *testing.T) {
	env := []string{"HOME=/home/user"}

	filtered, _ := executor.FilterEnv(env, executor.FilterEnvOpts{})
	assert.Contains(t, filtered, "BROWSER=")
	assert.Contains(t, filtered, "NO_GUI=1")
	assert.Contains(t, filtered, "HEADLESS=1")
}

func TestFilterEnvAllowListMode(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"PATH=/usr/bin",
		"RANDOM_VAR=something",
		"CLOCKWORK_DB_PATH=clockwork.db",
	}

	filtered, _ := executor.FilterEnv(env, executor.FilterEnvOpts{
		AllowList: []string{"HOME", "PATH"},
	})
	assert.Contains(t, filtered, "HOME=/home/user")
	assert.Contains(t, filtered, "PATH=/usr/bin")
	// AllowList mode only passes listed vars (plus extras and GUI prevention)
	foundRandom := false
	for _, v := range filtered {
		if v == "RANDOM_VAR=something" {
			foundRandom = true
		}
	}
	assert.False(t, foundRandom)
}

func TestSecretPatternDetection(t *testing.T) {
	// LooksLikeSecret is the "redact-worthy" predicate. Provider-auth env
	// vars ARE secrets and STILL return true here — log/UX redactors keep
	// hiding them from operator-visible output. Env passthrough is handled
	// by ShouldStripEnvVar (separate concern), tested below.

	// Secret patterns — match → true.
	assert.True(t, executor.LooksLikeSecret("AWS_SECRET_ACCESS_KEY"))
	assert.True(t, executor.LooksLikeSecret("MY_APP_SECRET"))
	assert.True(t, executor.LooksLikeSecret("DB_PASSWORD"))
	assert.True(t, executor.LooksLikeSecret("AUTH_CREDENTIAL"))
	assert.True(t, executor.LooksLikeSecret("PRIVATE_KEY"))
	assert.True(t, executor.LooksLikeSecret("MY_CUSTOM_API_KEY"))
	// Provider-auth vars are STILL secrets (redact-worthy). They are
	// allowed past the env-strip filter via ShouldStripEnvVar, but
	// LooksLikeSecret keeps treating them as redact-worthy so callers like
	// agent.summarizeToolInput continue hiding them from log/UX surfaces.
	assert.True(t, executor.LooksLikeSecret("ANTHROPIC_API_KEY"))
	assert.True(t, executor.LooksLikeSecret("ANTHROPIC_AUTH_TOKEN"))
	assert.True(t, executor.LooksLikeSecret("OPENAI_API_KEY"))
	assert.True(t, executor.LooksLikeSecret("GEMINI_API_KEY"))
	assert.True(t, executor.LooksLikeSecret("GOOGLE_API_KEY"))
	assert.True(t, executor.LooksLikeSecret("GITHUB_TOKEN"))
	assert.True(t, executor.LooksLikeSecret("GH_TOKEN"))

	// Plain non-secret names.
	assert.False(t, executor.LooksLikeSecret("HOME"))
	assert.False(t, executor.LooksLikeSecret("PATH"))
	assert.False(t, executor.LooksLikeSecret("CLOCKWORK_DB_PATH"))
	assert.False(t, executor.LooksLikeSecret("TERM"))
	assert.False(t, executor.LooksLikeSecret("SHELL"))
	// safeTokenVars false-positive path.
	assert.False(t, executor.LooksLikeSecret("COLORTERM"))
	assert.False(t, executor.LooksLikeSecret("TERM_PROGRAM"))
}

// TestShouldStripEnvVar verifies the env-strip predicate that wraps
// LooksLikeSecret with the providerAuthEnvVars allowlist
// (CW-20260509-0011). FilterEnv uses this; log redactors continue using
// LooksLikeSecret.
func TestShouldStripEnvVar(t *testing.T) {
	// Non-secret names: never stripped.
	assert.False(t, executor.ShouldStripEnvVar("HOME"))
	assert.False(t, executor.ShouldStripEnvVar("PATH"))
	assert.False(t, executor.ShouldStripEnvVar("CLOCKWORK_DB_PATH"))

	// Generic secrets: stripped.
	assert.True(t, executor.ShouldStripEnvVar("AWS_SECRET_ACCESS_KEY"))
	assert.True(t, executor.ShouldStripEnvVar("MY_APP_SECRET"))
	assert.True(t, executor.ShouldStripEnvVar("DB_PASSWORD"))
	assert.True(t, executor.ShouldStripEnvVar("AUTH_CREDENTIAL"))
	assert.True(t, executor.ShouldStripEnvVar("PRIVATE_KEY"))
	// Non-allowlisted API_KEY-named vars: stripped (only documented
	// portfolio provider auth vars are allowlisted).
	assert.True(t, executor.ShouldStripEnvVar("MY_CUSTOM_API_KEY"))

	// Provider-auth allowlist: NOT stripped (passthrough to subprocess).
	assert.False(t, executor.ShouldStripEnvVar("ANTHROPIC_API_KEY"),
		"bare-mode claude requires ANTHROPIC_API_KEY in env (--bare contract)")
	assert.False(t, executor.ShouldStripEnvVar("ANTHROPIC_AUTH_TOKEN"))
	assert.False(t, executor.ShouldStripEnvVar("OPENAI_API_KEY"))
	assert.False(t, executor.ShouldStripEnvVar("GEMINI_API_KEY"))
	assert.False(t, executor.ShouldStripEnvVar("GOOGLE_API_KEY"))
	assert.False(t, executor.ShouldStripEnvVar("GITHUB_TOKEN"))
	assert.False(t, executor.ShouldStripEnvVar("GH_TOKEN"))

	// Lowercase keys still match the allowlist (ToUpper inside).
	assert.False(t, executor.ShouldStripEnvVar("anthropic_api_key"))
}
