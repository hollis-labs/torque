package scheduler_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/stretchr/testify/assert"
)

func TestEstimatePromptTokens(t *testing.T) {
	// chars/4 heuristic — quick sanity rather than a deep test.
	assert.Equal(t, 0, scheduler.EstimatePromptTokens())
	assert.Equal(t, 0, scheduler.EstimatePromptTokens("abc")) // 3/4 = 0
	assert.Equal(t, 1, scheduler.EstimatePromptTokens("abcd"))
	assert.Equal(t, 25, scheduler.EstimatePromptTokens(strings.Repeat("x", 100)))
	assert.Equal(t, 50, scheduler.EstimatePromptTokens(
		strings.Repeat("a", 100), strings.Repeat("b", 100),
	))
}

func TestPrecheck_ColdCacheNoOps(t *testing.T) {
	task := sqlstore.TaskRecord{Title: "t", Description: "d"}
	res := scheduler.Precheck(task, scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "",
		func(string, string) (modelsdev.Model, bool) { return modelsdev.Model{}, false },
		scheduler.PrecheckOptions{WindowEnabled: true, CapabilitiesEnabled: true})
	assert.Empty(t, res.BlockReason)
	assert.Empty(t, res.Warnings)
}

func TestPrecheck_NoOpWhenProfileMissingProviderModel(t *testing.T) {
	task := sqlstore.TaskRecord{Title: "t"}
	res := scheduler.Precheck(task, scheduler.NewProfileForPrecheck("", "", ""), "",
		func(string, string) (modelsdev.Model, bool) {
			return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 100}}, true
		},
		scheduler.PrecheckOptions{WindowEnabled: true})
	assert.Empty(t, res.BlockReason)
}

func TestPrecheck_WindowBlock(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 5000), // ~1250 estimated tokens
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 1000}}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{WindowEnabled: true, WindowThreshold: 0.8})
	assert.Contains(t, res.BlockReason, "exceeds")
	assert.Contains(t, res.BlockReason, "context window")
}

func TestPrecheck_WindowWarn(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 4000), // ~1000 tokens, above 80% of 1024
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 1024}}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{WindowEnabled: true, WindowThreshold: 0.8})
	assert.Empty(t, res.BlockReason, "should warn but not block")
	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "above 80%")
}

func TestPrecheck_WindowDisabledNoCheck(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 1_000_000),
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 100}}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{}) // both disabled
	assert.Empty(t, res.BlockReason)
}

func TestPrecheck_CapabilityBlock(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title: "t",
		// Tools field signals the task needs tool-call support.
		Tools: sql.NullString{String: `["bash","edit"]`, Valid: true},
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{
			Limit:        modelsdev.Limits{ContextWindow: 100000},
			Capabilities: modelsdev.Capabilities{ToolCall: false},
		}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("openai", "no-tools-model", ""), "", lookup,
		scheduler.PrecheckOptions{CapabilitiesEnabled: true})
	assert.Contains(t, res.BlockReason, "tool-call")
}

func TestPrecheck_CapabilityPassesWhenSupported(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title: "t",
		Tools: sql.NullString{String: `["bash"]`, Valid: true},
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{
			Limit:        modelsdev.Limits{ContextWindow: 100000},
			Capabilities: modelsdev.Capabilities{ToolCall: true},
		}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{CapabilitiesEnabled: true})
	assert.Empty(t, res.BlockReason)
}

func TestPrecheck_CapabilitySkipsWhenTaskHasNoTools(t *testing.T) {
	task := sqlstore.TaskRecord{Title: "t"} // Tools field is invalid (NULL)
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{
			Limit:        modelsdev.Limits{ContextWindow: 100000},
			Capabilities: modelsdev.Capabilities{ToolCall: false},
		}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("openai", "no-tools-model", ""), "", lookup,
		scheduler.PrecheckOptions{CapabilitiesEnabled: true})
	assert.Empty(t, res.BlockReason, "no tools requested = no capability check needed")
}
