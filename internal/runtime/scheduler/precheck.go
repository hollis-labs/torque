package scheduler

import (
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// PrecheckMode is the per-check enforcement level. Tri-state by design — a
// boolean off/on hides the most useful middle ground (collect signal in logs
// before promoting to a hard block). Zero value is PrecheckOff so an
// unconfigured PrecheckOptions is a strict no-op.
type PrecheckMode string

const (
	// PrecheckOff disables the check entirely. No estimate, no log line.
	PrecheckOff PrecheckMode = "off"
	// PrecheckWarn runs the check and emits a warning (logged at the
	// scheduler level) but always proceeds with dispatch. Use to gather
	// signal against real traffic before promoting to block.
	PrecheckWarn PrecheckMode = "warn"
	// PrecheckBlock runs the check and refuses dispatch on failure,
	// transitioning the task to blocked with a reason.
	PrecheckBlock PrecheckMode = "block"
)

// DefaultPrecheckOptions returns the recommended starting point: warn-only
// on both gates, with the window threshold at 80%. Warn mode is harmless to
// real traffic while still surfacing problems in logs, so flipping it on by
// default is safer than off-by-default (which gives ops nothing to trust).
func DefaultPrecheckOptions() PrecheckOptions {
	return PrecheckOptions{
		Window:          PrecheckWarn,
		WindowThreshold: 0.8,
		Capabilities:    PrecheckWarn,
	}
}

// PrecheckOptions configures the pre-dispatch model checks. Both gates are
// tri-state (off / warn / block). Defaults to all-off zero value; serve.go
// calls DefaultPrecheckOptions to override before reading settings.
type PrecheckOptions struct {
	// Window controls the context-window estimate. In warn mode an over-window
	// estimate just logs; in block mode it refuses dispatch (transitions task
	// to blocked with reason). The threshold-band warning fires in both warn
	// and block modes whenever the estimate exceeds WindowThreshold * window.
	Window PrecheckMode
	// WindowThreshold is the warning fraction (0.0-1.0). 0.8 means warn at
	// 80% of the context window. Values <=0 or >=1 disable the warning band;
	// the over-window check still applies.
	WindowThreshold float64
	// Capabilities controls the tool-call gate. In warn mode a tool-requesting
	// task dispatched to a tool-incapable model logs but proceeds; in block
	// mode it refuses.
	Capabilities PrecheckMode
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

	if modeEnforced(opts.Window) && model.Limit.ContextWindow > 0 {
		est := EstimatePromptTokens(
			task.Title,
			task.Description,
			task.SystemPrompt,
			profile.SystemPrompt,
			agentFileContents,
		)
		window := model.Limit.ContextWindow
		if est > window {
			msg := fmt.Sprintf(
				"prompt estimate %d tokens exceeds %s/%s context window %d",
				est, profile.Provider, profile.Model, window,
			)
			if opts.Window == PrecheckBlock {
				res.BlockReason = msg
				return res
			}
			res.Warnings = append(res.Warnings, msg)
		} else if opts.WindowThreshold > 0 && opts.WindowThreshold < 1 {
			warnAt := int(float64(window) * opts.WindowThreshold)
			if est > warnAt {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"prompt estimate %d tokens above %.0f%% of %s/%s context window (%d/%d)",
					est, opts.WindowThreshold*100, profile.Provider, profile.Model, est, window,
				))
			}
		}
	}

	if modeEnforced(opts.Capabilities) && taskRequiresTools(task) && !model.Capabilities.ToolCall {
		msg := fmt.Sprintf(
			"task requires tool-call but %s/%s does not support it",
			profile.Provider, profile.Model,
		)
		if opts.Capabilities == PrecheckBlock {
			res.BlockReason = msg
			return res
		}
		res.Warnings = append(res.Warnings, msg)
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

// modeEnforced reports whether the mode actively runs the check. Both the
// zero value (empty string) and explicit "off" disable; warn and block enforce.
func modeEnforced(m PrecheckMode) bool {
	return m == PrecheckWarn || m == PrecheckBlock
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
	profiles := config.CurrentProfiles(s.Profiles)
	if len(profiles) == 0 {
		return PrecheckResult{}
	}
	rawProfile, ok := profiles[task.AgentProfile]
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
