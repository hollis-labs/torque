package scheduler

import (
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/worktree"
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
// on the model gates, with the window threshold at 80%, and block on the
// worktree gate. Warn mode is harmless to real traffic while still surfacing
// problems in logs, so flipping it on by default is safer than off-by-default
// (which gives ops nothing to trust). The worktree gate defaults to block
// because a non-git working dir is an unambiguous setup error — silently
// falling back mid-dispatch hides a misconfiguration the operator should fix.
func DefaultPrecheckOptions() PrecheckOptions {
	return PrecheckOptions{
		Window:          PrecheckWarn,
		WindowThreshold: 0.8,
		Capabilities:    PrecheckWarn,
		Worktree:        PrecheckBlock,
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
	// Worktree controls the git-repo gate. It only runs when per-run worktrees
	// are enabled (TORQUE_WORKTREE_PER_RUN): it verifies the task's working dir
	// resolves to a git repo BEFORE dispatch. In warn mode a non-git working
	// dir logs but proceeds (SetupPerRun would then fall back to the working
	// dir); in block mode it refuses dispatch with a clear reason instead of a
	// silent mid-dispatch fallback.
	Worktree PrecheckMode
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

// WorktreePrecheck is the git-repo gate. When per-run worktrees are enabled
// it verifies that workingDir resolves to a git repo (FindRepoRoot succeeds)
// before dispatch, so a non-git working dir produces a clean blocked-task
// reason rather than a silent mid-dispatch fallback in SetupPerRun.
//
//   - mode off (or zero value): no-op, returns an empty result.
//   - worktreeEnabled false: no-op — the worktree path won't run, nothing to
//     check.
//   - mode warn: a non-git working dir adds a warning but dispatch proceeds.
//   - mode block: a non-git working dir sets BlockReason.
//
// An empty workingDir is treated as "no working dir to check" — the per-run
// worktree path itself is gated on a non-empty working dir, so there is
// nothing to refuse.
func WorktreePrecheck(workingDir string, worktreeEnabled bool, mode PrecheckMode) PrecheckResult {
	res := PrecheckResult{}
	if !modeEnforced(mode) || !worktreeEnabled || strings.TrimSpace(workingDir) == "" {
		return res
	}
	if _, err := worktree.FindRepoRoot(workingDir); err != nil {
		msg := fmt.Sprintf(
			"per-run worktrees are enabled but working dir %q is not a git repo: %v",
			workingDir, err,
		)
		if mode == PrecheckBlock {
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
// Profiles fields, reads the agent file, runs the model policy, and runs the
// worktree git-repo gate. Returns the merged result so the caller can decide
// between block and dispatch.
func (s *Scheduler) precheckDispatch(task sqlstore.TaskRecord, opts PrecheckOptions) PrecheckResult {
	// Worktree git-repo gate runs independently of the model checks — it does
	// not need a resolvable profile/model, only the per-run-worktree config
	// and the task's working dir. Run it first; a block here short-circuits.
	wt := WorktreePrecheck(task.WorkingDir, s.worktreeSpec().WorktreeEnabled(), opts.Worktree)
	if wt.BlockReason != "" {
		return wt
	}

	profiles := config.CurrentProfiles(s.Profiles)
	if len(profiles) == 0 {
		return wt
	}
	rawProfile, ok := profiles[task.AgentProfile]
	if !ok {
		return wt
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
	res := Precheck(task, prof, readAgentFile(task.AgentFile), lookup, opts)
	res.Warnings = append(wt.Warnings, res.Warnings...)
	return res
}
