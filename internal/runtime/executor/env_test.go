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
		"OPENAI_API_KEY=sk-abc123",
		"ANTHROPIC_API_KEY=sk-ant-abc123",
		"DATABASE_URL=postgres://localhost/db",
		"GITHUB_TOKEN=ghp_xxxx",
		"MY_APP_SECRET=topsecret",
		"CLOCKWORK_DB_PATH=clockwork.db",
	}

	filtered, stripped := executor.FilterEnv(env, executor.FilterEnvOpts{})
	assert.NotContains(t, filtered, "AWS_SECRET_ACCESS_KEY=supersecret123")
	assert.NotContains(t, filtered, "OPENAI_API_KEY=sk-abc123")
	assert.NotContains(t, filtered, "ANTHROPIC_API_KEY=sk-ant-abc123")
	assert.NotContains(t, filtered, "GITHUB_TOKEN=ghp_xxxx")
	assert.NotContains(t, filtered, "MY_APP_SECRET=topsecret")
	assert.Contains(t, filtered, "HOME=/home/user")
	assert.Contains(t, filtered, "PATH=/usr/bin")
	assert.Contains(t, filtered, "CLOCKWORK_DB_PATH=clockwork.db")

	assert.Contains(t, stripped, "AWS_SECRET_ACCESS_KEY")
	assert.Contains(t, stripped, "OPENAI_API_KEY")
	assert.Contains(t, stripped, "ANTHROPIC_API_KEY")
	assert.Contains(t, stripped, "GITHUB_TOKEN")
	assert.Contains(t, stripped, "MY_APP_SECRET")
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
	assert.True(t, executor.LooksLikeSecret("AWS_SECRET_ACCESS_KEY"))
	assert.True(t, executor.LooksLikeSecret("OPENAI_API_KEY"))
	assert.True(t, executor.LooksLikeSecret("MY_APP_SECRET"))
	assert.True(t, executor.LooksLikeSecret("DB_PASSWORD"))
	assert.True(t, executor.LooksLikeSecret("GITHUB_TOKEN"))
	assert.True(t, executor.LooksLikeSecret("AUTH_CREDENTIAL"))
	assert.True(t, executor.LooksLikeSecret("PRIVATE_KEY"))

	assert.False(t, executor.LooksLikeSecret("HOME"))
	assert.False(t, executor.LooksLikeSecret("PATH"))
	assert.False(t, executor.LooksLikeSecret("CLOCKWORK_DB_PATH"))
	assert.False(t, executor.LooksLikeSecret("TERM"))
	assert.False(t, executor.LooksLikeSecret("SHELL"))
}
