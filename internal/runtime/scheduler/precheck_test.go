package scheduler_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
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

func TestDefaultPrecheckOptions(t *testing.T) {
	opts := scheduler.DefaultPrecheckOptions()
	assert.Equal(t, scheduler.PrecheckWarn, opts.Window)
	assert.Equal(t, scheduler.PrecheckWarn, opts.Capabilities)
	assert.InDelta(t, 0.8, opts.WindowThreshold, 0.0001)
}

func TestPrecheck_ColdCacheNoOps(t *testing.T) {
	task := sqlstore.TaskRecord{Title: "t", Description: "d"}
	res := scheduler.Precheck(task, scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "",
		func(string, string) (modelsdev.Model, bool) { return modelsdev.Model{}, false },
		scheduler.PrecheckOptions{Window: scheduler.PrecheckBlock, Capabilities: scheduler.PrecheckBlock})
	assert.Empty(t, res.BlockReason)
	assert.Empty(t, res.Warnings)
}

func TestPrecheck_NoOpWhenProfileMissingProviderModel(t *testing.T) {
	task := sqlstore.TaskRecord{Title: "t"}
	res := scheduler.Precheck(task, scheduler.NewProfileForPrecheck("", "", ""), "",
		func(string, string) (modelsdev.Model, bool) {
			return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 100}}, true
		},
		scheduler.PrecheckOptions{Window: scheduler.PrecheckBlock})
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
		scheduler.PrecheckOptions{Window: scheduler.PrecheckBlock, WindowThreshold: 0.8})
	assert.Contains(t, res.BlockReason, "exceeds")
	assert.Contains(t, res.BlockReason, "context window")
}

func TestPrecheck_WindowWarn_OverLimit(t *testing.T) {
	// In warn mode, an over-window estimate logs a warning but does NOT block.
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 5000), // ~1250 tokens, over 1000-window
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 1000}}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{Window: scheduler.PrecheckWarn, WindowThreshold: 0.8})
	assert.Empty(t, res.BlockReason, "warn mode should never block")
	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "exceeds")
}

func TestPrecheck_WindowWarn_ThresholdBand(t *testing.T) {
	// Below the hard limit but above the warn threshold — emits warning,
	// no block in either warn or block mode.
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 4000), // ~1000 tokens, above 80% of 1024
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 1024}}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{Window: scheduler.PrecheckBlock, WindowThreshold: 0.8})
	assert.Empty(t, res.BlockReason, "below limit, threshold-band warns only")
	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "above 80%")
}

func TestPrecheck_WindowOff(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title:       "t",
		Description: strings.Repeat("x", 1_000_000),
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{Limit: modelsdev.Limits{ContextWindow: 100}}, true
	}
	// Zero-value PrecheckOptions has Window=PrecheckOff (empty string).
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("anthropic", "claude-sonnet-4-6", ""), "", lookup,
		scheduler.PrecheckOptions{})
	assert.Empty(t, res.BlockReason)
	assert.Empty(t, res.Warnings)
}

func TestPrecheck_CapabilityBlock(t *testing.T) {
	task := sqlstore.TaskRecord{
		Title: "t",
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
		scheduler.PrecheckOptions{Capabilities: scheduler.PrecheckBlock})
	assert.Contains(t, res.BlockReason, "tool-call")
}

func TestPrecheck_CapabilityWarn(t *testing.T) {
	// Warn mode logs but doesn't block tool-incapable dispatches.
	task := sqlstore.TaskRecord{
		Title: "t",
		Tools: sql.NullString{String: `["bash"]`, Valid: true},
	}
	lookup := func(string, string) (modelsdev.Model, bool) {
		return modelsdev.Model{
			Limit:        modelsdev.Limits{ContextWindow: 100000},
			Capabilities: modelsdev.Capabilities{ToolCall: false},
		}, true
	}
	res := scheduler.Precheck(task,
		scheduler.NewProfileForPrecheck("openai", "no-tools-model", ""), "", lookup,
		scheduler.PrecheckOptions{Capabilities: scheduler.PrecheckWarn})
	assert.Empty(t, res.BlockReason)
	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "tool-call")
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
		scheduler.PrecheckOptions{Capabilities: scheduler.PrecheckBlock})
	assert.Empty(t, res.BlockReason)
	assert.Empty(t, res.Warnings)
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
		scheduler.PrecheckOptions{Capabilities: scheduler.PrecheckBlock})
	assert.Empty(t, res.BlockReason, "no tools requested = no capability check needed")
}
