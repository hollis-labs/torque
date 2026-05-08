package agent

import (
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

// Options describes a Boot request. Mode picks the lifecycle policy; the
// other fields parametrize the boot. Required vs optional is documented per
// field; Validate enforces the cross-Mode constraints.
type Options struct {
	// Mode is the lifecycle policy. Defaults to ModeLongLived (zero-value).
	Mode Mode

	// AgentProfile is the clockwork agent profile name. Resolves through
	// config.GetProfileOrDefault — operators register profiles in
	// profiles.yaml; the substrate ships builtins for orchestrator / planner
	// / reviewer-end-agent / cli.
	AgentProfile string

	// Role is a human-readable role tag persisted in SessionMeta and the
	// boot.md kickoff. Values today: "orchestrator", "planner", "reviewer-
	// end-agent", "executor". Empty defaults to the AgentProfile value.
	Role string

	// Workdir is the project directory the agent should reason about. For
	// claude/codex this is reached via --add-dir; for opencode it's the
	// spawn cwd directly. Required (Boot rejects empty).
	Workdir string

	// ProjectID / TaskID propagate to the session row's soft-FKs so
	// `clockwork_session_list project_id=...` returns this session.
	ProjectID string
	TaskID    string

	// RunID stamps the boot dir name (clockwork-boot-<provider>-<task>-r<run>-*)
	// for forensic discoverability. Zero means "no run dispatched this boot".
	RunID int64

	// ParentSessionID — required when Mode == ModeSubagent.
	ParentSessionID string

	// ResumeFromCheckpoint — required when Mode == ModeResume.
	ResumeFromCheckpoint string

	// OneShotPrompt is the user-message body for ModeOneShot. When empty,
	// the kickoff defaults to "Boot @./boot.md" and the boot.md content
	// drives the turn (matches ModeLongLived's framing). Most callers leave
	// this empty.
	OneShotPrompt string

	// SystemPrompt allows a caller to inject task-specific framing on top
	// of the role template. Composed in front of the agent-file's
	// system_prompt via composeSystemPrompt.
	SystemPrompt string

	// AgentFile is the task's agent_file path, verbatim. Resolved + loaded
	// at Boot time via internal/agentfile.
	AgentFile string

	// Env is caller-supplied env additions. Composed with profile.Environment
	// + CLOCKWORK_TASK_ID/RUN_ID + agent-file environment by composeEnv.
	Env map[string]string

	// SubprocessPerTurnOverride forces Caps.PTY=false even on providers/Modes
	// that would otherwise opt-in to PTY. Escape hatch for diagnostics; most
	// callers leave it false.
	SubprocessPerTurnOverride bool

	// SandboxProfile, when non-zero, applies to the spawn. AllowLoopback is
	// force-set to true on Boot so the per-task MCP loopback URL resolves.
	// Zero-value preserves "no sandbox" today.
	SandboxProfile *sandbox.Profile

	// SessionMeta is opaque key/value metadata persisted with the session row.
	// Mode/Role/PlanID conventions are stamped automatically; callers add
	// extras here.
	SessionMeta map[string]string

	// Description is the OneShot user-prompt body when OneShotPrompt is
	// empty. Mirrors ExecutionJob.Description from the executor path.
	Description string

	// Tools / Permissions / Files / Deliverables / Limits / Metadata /
	// Tools propagate from the executor.ExecutionJob shape so the OneShot
	// path can reuse Options without a parallel struct. Most ModeLongLived
	// callers leave them zero.
	Metadata map[string]any

	// TypedEventCallback, when non-nil, is forwarded to
	// StartOptions.TypedEventCallback. PTY runtime fires per-line via the
	// adapter's provider.EventParser interface; adapter runtime ignores it
	// (typed events on the subprocess-per-turn path is a lib-side follow-up
	// per go-agent-sessions v0.6.0 changelog). Callers that want token-usage
	// tracking on ModeOneShot should keep using the legacy eventFanout
	// channel routed through Executor.Run.
	TypedEventCallback provider.EventsCallback

	// Supervisor, when non-nil, is forwarded to StartOptions.Supervisor.
	// PTY path enforces it natively (idle-kill, restart-on-crash, watchdog).
	// Adapter path forwards the field but the lib silently ignores it
	// pending go-runner v0.3.x publishing the supervision API; callers can
	// set it now and pickup is automatic when the lib unblocks.
	Supervisor *agentsessions.SupervisorOptions

	// ResourceLimits, when non-nil and non-zero, is forwarded to
	// StartOptions.ResourceLimits. Same PTY-vs-adapter forwarding contract
	// as Supervisor.
	ResourceLimits *agentsessions.ResourceLimits

	// IDFn lets tests pin session IDs. Production wires defaultSessionID().
	IDFn func() string

	// eventFanout is the legacy provider.StreamEvent channel used by the
	// scheduler-dispatched ModeOneShot path to accumulate token usage. Lower-
	// case so external callers route through the executor wrapper rather
	// than constructing it themselves; the wrapper allocates the chan,
	// reads from it in a goroutine, and closes it after Boot returns.
	eventFanout chan<- provider.StreamEvent
}

// withEventFanout sets the unexported eventFanout field. Used by the
// Executor.Run wrapper to thread the OneShot token-usage chan into Boot.
func (o Options) withEventFanout(c chan<- provider.StreamEvent) Options {
	o.eventFanout = c
	return o
}

// Validate enforces cross-Mode constraints. Called by Boot before any work.
func (o Options) Validate() error {
	if o.AgentProfile == "" {
		return errAgentProfileRequired
	}
	if o.Workdir == "" {
		return ErrWorkdirRequired
	}
	switch o.Mode {
	case ModeSubagent:
		if o.ParentSessionID == "" {
			return ErrParentSessionRequired
		}
	case ModeResume:
		if o.ResumeFromCheckpoint == "" {
			return ErrResumeCheckpointRequired
		}
	case ModeLongLived, ModeOneShot, ModeBackground:
		// no extra constraints
	default:
		return errInvalidMode
	}
	return nil
}

// errors that aren't part of the public sentinel surface (compile-time only).
var (
	errAgentProfileRequired = errStr("agent: Options.AgentProfile is required")
	errInvalidMode          = errStr("agent: Options.Mode is not a recognized value")
)

// errStr is a minimal error used for never-public sentinel cases. Avoids
// expanding the public errors.go surface for assertion-only conditions.
type errStr string

func (e errStr) Error() string { return string(e) }
