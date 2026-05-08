// Package agent is clockwork's unified agent-boot substrate.
//
// One entry point — Boot — replaces the prior split between cliexec (per-task
// scheduler-dispatched executor) and sessionmgr (long-lived sessions). Every
// spawn, regardless of lifecycle, gets:
//
//   - Per-task ephemeral boot dir (planted CLAUDE.md / AGENTS.md / .mcp.json)
//   - Persistent workspace dir (root + prompts/ state/ logs/ scaffolded; the
//     lib writes session.log when LogPath is unset, but clockwork doesn't
//     mirror prompts/state into it today — see workspace.go for the
//     fence on what the substrate does vs. what's reserved)
//   - Composed system prompt (role + agent-file + project context)
//   - MCP loopback (closure-bound, no task_id parameter)
//   - Composed env (filtered OS env + CLOCKWORK_TASK_ID + CLOCKWORK_RUN_ID +
//     agent_file.environment + opts.Env + per-provider amendments)
//   - Sandbox profile (zero-value preserves "no sandbox" today)
//
// The lifecycle policy is captured by Mode (LongLived / OneShot / Resume /
// Subagent / Background). All five modes share the setup; only the lifecycle
// policy differs.
//
// Cross-app design: agent-workspaces/planning/agent-boot-unification/
// 2026-05-07-cross-app-design.md. Lib-tier prerequisites: 2026-05-08-lib-tier-
// status.md. Authoritative ticket: CW-20260508-0001.
package agent

import (
	"errors"
	"time"
)

// Mode encodes the session lifecycle policy. Caller picks one; the enum is
// flat (no inheritance) so each value is mutually exclusive.
type Mode int

const (
	// ModeLongLived is the default. Session stays alive across turns;
	// orchestrator / reviewer / planner agents use this. Caps.PTY=true on
	// providers whose PTY shape is verified (claude); Caps.PTY=false on
	// adapter-runtime providers.
	ModeLongLived Mode = iota

	// ModeOneShot is the scheduler-dispatched executor lifecycle. One turn,
	// auto-stop on return. The first-turn fires synchronously via SendInput;
	// AutoFireFirstTurn stays false for this mode (the lib's adapter runtime
	// would block Start otherwise).
	ModeOneShot

	// ModeResume re-launches a session from a previous checkpoint. Requires
	// Options.ResumeFromCheckpoint. The provider's session-id (when the
	// adapter exposes one) is threaded via StartOptions.SessionIDPreset.
	ModeResume

	// ModeSubagent boots a nested session under a parent. Requires
	// Options.ParentSessionID. Lifecycle is bound to the parent — when the
	// parent stops the orphan sweep closes the subagent.
	ModeSubagent

	// ModeBackground returns the moment Start succeeds. Caller does not wait
	// or attach. AutoFireFirstTurn=true delivers the kickoff async via the
	// lib's PTY runtime; the call returns once the kickoff is in flight.
	ModeBackground
)

// String renders the mode as a stable string for log and DB shapes.
func (m Mode) String() string {
	switch m {
	case ModeLongLived:
		return "long_lived"
	case ModeOneShot:
		return "one_shot"
	case ModeResume:
		return "resume"
	case ModeSubagent:
		return "subagent"
	case ModeBackground:
		return "background"
	default:
		return "unknown"
	}
}

// parseModeString is the inverse of Mode.String — used by sessionFromRecord
// to recover Mode from the SessionMeta `clockwork.mode` key. Unknown / empty
// strings round-trip to ModeLongLived (the zero value), matching the default
// for any session whose meta predates the stamping convention.
func parseModeString(s string) Mode {
	switch s {
	case "long_lived":
		return ModeLongLived
	case "one_shot":
		return ModeOneShot
	case "resume":
		return ModeResume
	case "subagent":
		return ModeSubagent
	case "background":
		return ModeBackground
	default:
		return ModeLongLived
	}
}

// Status is the public, user-facing session lifecycle state. Mirrors the
// go-agent-sessions State strings plus `crashed` (set by the orphan sweep
// when a daemon restart finds a session row whose process is gone).
type Status string

const (
	StatusLaunching Status = "launching"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCrashed   Status = "crashed"
)

// Terminal reports whether the status indicates the session has stopped.
func (s Status) Terminal() bool {
	switch s {
	case StatusDone, StatusFailed, StatusCrashed:
		return true
	}
	return false
}

// Session is the public snapshot of a registered session. Manager.Get / List
// returns these; raw go-agent-sessions handles stay internal.
type Session struct {
	ID              string
	Mode            Mode
	AgentProfile    string
	Provider        string
	RuntimeID       string
	RuntimeKind     string
	Workdir         string // spawned process cwd (boot dir for claude/codex; project dir for opencode)
	BootDir         string // ephemeral per-task tempdir
	WorkspaceDir    string // persistent ~/.clockwork/workspaces/<project>/<sessID>/
	ProjectID       string
	TaskID          string
	ParentSessionID string // ModeSubagent
	Status          Status
	PID             int
	ExitCode        *int
	ResumeHint      []byte
	Meta            map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastActivity    time.Time
	EndedAt         *time.Time
}

// Checkpoint is the public snapshot of a session_checkpoints row.
type Checkpoint struct {
	ID         string
	SessionID  string
	Payload    string
	ResumeHint []byte
	Note       string
	CreatedAt  time.Time
}

// CheckpointRequest captures a checkpoint for the named session.
type CheckpointRequest struct {
	SessionID string
	Payload   string
	Note      string
}

// ResumeRequest re-launches a session against a previously persisted
// checkpoint. Only the lifecycle path; the boot-time entry uses
// agent.Boot(Mode=ModeResume, ResumeFromCheckpoint=...) instead.
type ResumeRequest struct {
	SessionID    string
	CheckpointID string // optional — empty means latest checkpoint
	AgentProfile string
	Workdir      string
	SystemPrompt string
	Env          []string
}

// Sentinel errors. Wrap with errors.Is at call sites.
var (
	// ErrSessionNotFound — the named session has no row in the store.
	ErrSessionNotFound = errors.New("agent: session not found")

	// ErrSessionNotRunning — registered but not in-memory (terminated, never
	// resumed, or daemon restarted since).
	ErrSessionNotRunning = errors.New("agent: session not running in this process")

	// ErrManagerStopped — Manager.Shutdown has been called.
	ErrManagerStopped = errors.New("agent: manager stopped")

	// ErrNoCheckpoint — Resume can't find the named (or latest) checkpoint.
	ErrNoCheckpoint = errors.New("agent: no checkpoint available")

	// ErrAdapterNotFound — the profile names a provider the factory can't
	// resolve.
	ErrAdapterNotFound = errors.New("agent: adapter not registered for provider")

	// ErrBootFailed — Boot() returned because Manager.Start (or kickoff for
	// ModeOneShot) failed. Wraps the underlying cause.
	ErrBootFailed = errors.New("agent: boot failed")

	// ErrParentSessionRequired — ModeSubagent without Options.ParentSessionID.
	ErrParentSessionRequired = errors.New("agent: ModeSubagent requires Options.ParentSessionID")

	// ErrResumeCheckpointRequired — ModeResume without Options.ResumeFromCheckpoint.
	ErrResumeCheckpointRequired = errors.New("agent: ModeResume requires Options.ResumeFromCheckpoint")

	// ErrWorkdirRequired — Options.Workdir is empty and there's no fallback.
	ErrWorkdirRequired = errors.New("agent: Workdir is required")

	// ErrBootDirNotImplemented — provider's bootdir layout is a stub (gemini,
	// copilot today). The lib's BootDirSpec.Notes describes what to verify
	// before promoting the per-provider file to a concrete impl.
	ErrBootDirNotImplemented = errors.New("agent: bootdir layout not yet implemented for this provider")
)
