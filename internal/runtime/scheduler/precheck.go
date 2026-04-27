package scheduler

import (
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
)

// PrecheckOptions configures the pre-dispatch model checks. All fields default
// to "off" so the guardrails stay opt-in until ops trust them.
type PrecheckOptions struct {
	// WindowEnabled gates the context-window estimate. When true, an estimate
	// over the model's context_window blocks dispatch; an estimate over
	// WindowThreshold * context_window emits a warning but still dispatches.
	WindowEnabled bool
	// WindowThreshold is the warning fraction (0.0-1.0). 0.8 means warn at
	// 80% of the context window. Values <=0 or >1 disable the warning band
	// entirely; the hard block at 100% still applies.
	WindowThreshold float64
	// CapabilitiesEnabled gates the capability check. When true, dispatches
	// to a model whose Capabilities.ToolCall is false are blocked when the
	// task declares tool requirements.
	CapabilitiesEnabled bool
}

// PrecheckResult is the policy decision. BlockReason non-empty means refuse
// dispatch and transition the task to blocked with that text. Warnings are
// advisory — log them but still dispatch.
type PrecheckResult struct {
	BlockReason string
	Warnings    []string
}

// EstimatePromptTokens is a chars/4 heuristic over the concatenated input
// material. Cheap, model-agnostic, and good enough for warning-class
// decisions. Real per-provider tokenizers can come later if false-positive
// rate matters; this is the same approximation OpenAI's docs cite as a
// rough upper bound for English text.
func EstimatePromptTokens(parts ...string) int {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	return total / 4
}

// Precheck applies the configured guardrails against a single dispatch.
// All gates are opt-in; cold catalog (model unknown) skips both checks
// silently — we don't refuse what we can't verify.
//
// modelLookup returns the model record for (provider, model). Pass nil to
// disable the capability check (window check needs the model too, so it
// also no-ops).
func Precheck(
	task sqlstore.TaskRecord,
	profile ProfileForPrecheck,
	agentFileContents string,
	modelLookup func(provider, model string) (modelsdev.Model, bool),
	opts PrecheckOptions,
) PrecheckResult {
	res := PrecheckResult{}

	if profile.Provider == "" || profile.Model == "" || modelLookup == nil {
		return res
	}
	model, ok := modelLookup(profile.Provider, profile.Model)
	if !ok {
		// Cold cache or unknown model — don't refuse what we can't verify.
		return res
	}

	if opts.WindowEnabled && model.Limit.ContextWindow > 0 {
		est := EstimatePromptTokens(
			task.Title,
			task.Description,
			task.SystemPrompt,
			profile.SystemPrompt,
			agentFileContents,
		)
		window := model.Limit.ContextWindow
		if est > window {
			res.BlockReason = fmt.Sprintf(
				"prompt estimate %d tokens exceeds %s/%s context window %d",
				est, profile.Provider, profile.Model, window,
			)
			return res
		}
		if opts.WindowThreshold > 0 && opts.WindowThreshold < 1 {
			warnAt := int(float64(window) * opts.WindowThreshold)
			if est > warnAt {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"prompt estimate %d tokens above %.0f%% of %s/%s context window (%d/%d)",
					est, opts.WindowThreshold*100, profile.Provider, profile.Model, est, window,
				))
			}
		}
	}

	if opts.CapabilitiesEnabled && taskRequiresTools(task) && !model.Capabilities.ToolCall {
		res.BlockReason = fmt.Sprintf(
			"task requires tool-call but %s/%s does not support it",
			profile.Provider, profile.Model,
		)
		return res
	}

	return res
}

// ProfileForPrecheck is the subset of config.AgentProfile that Precheck reads.
// Defining it locally keeps the dependency one-way and gives tests a small
// public type to construct without dragging in the full AgentProfile.
type ProfileForPrecheck struct {
	Provider     string
	Model        string
	SystemPrompt string
}

// NewProfileForPrecheck is a convenience constructor for tests and callers
// that don't want to populate the struct field-by-field.
func NewProfileForPrecheck(provider, model, systemPrompt string) ProfileForPrecheck {
	return ProfileForPrecheck{Provider: provider, Model: model, SystemPrompt: systemPrompt}
}

// taskRequiresTools reports whether the task declares a non-empty tool list.
// We look at the JSON-encoded Tools column rather than parsing it because the
// presence of any non-trivial value is enough to gate on; a more careful
// check (e.g., specific tool names) is a follow-up if false-positives matter.
func taskRequiresTools(task sqlstore.TaskRecord) bool {
	if !task.Tools.Valid {
		return false
	}
	s := strings.TrimSpace(task.Tools.String)
	return s != "" && s != "[]" && s != "null"
}

// readAgentFile loads the agent_file referenced by the task, or returns ""
// if missing/unreadable. Errors are swallowed because the precheck is
// advisory — we don't want a missing optional file to block dispatch.
func readAgentFile(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// precheckDispatch is the Scheduler-instance shim. Pulls Models +
// Profiles fields, reads the agent file, and runs the policy. Returns
// the result so the caller can decide between block and dispatch.
func (s *Scheduler) precheckDispatch(task sqlstore.TaskRecord, opts PrecheckOptions) PrecheckResult {
	if s.Profiles == nil {
		return PrecheckResult{}
	}
	rawProfile, ok := s.Profiles[task.AgentProfile]
	if !ok {
		return PrecheckResult{}
	}
	prof := ProfileForPrecheck{
		Provider:     rawProfile.Provider,
		Model:        rawProfile.Model,
		SystemPrompt: rawProfile.SystemPrompt,
	}
	var lookup func(string, string) (modelsdev.Model, bool)
	if s.Models != nil {
		lookup = s.Models.Get
	}
	return Precheck(task, prof, readAgentFile(task.AgentFile), lookup, opts)
}
