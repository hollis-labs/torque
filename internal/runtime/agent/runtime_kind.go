package agent

import (
	"fmt"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
)

// RuntimeKind identifies the go-agent-sessions Runtime shape a session
// should use. Each kind corresponds to a distinct lifecycle + turn
// delivery surface in the lib:
//
//   - Subprocess: spawn-per-turn child process; SendInput writes the
//     turn prompt to stdin, child emits stream-json + exits.
//   - PTY: long-lived TUI driven via a real PTY; auto-fire-first-turn
//     and SendInput drive keystroke-like delivery. Programmatic TUI
//     driving remains unproven for the dogfooded providers; no provider
//     defaults to PTY today, but the value is retained as an operator
//     escape hatch via profile.RuntimeKind.
//   - StreamingStdio: long-lived NDJSON-over-stdin (Anthropic's
//     "Streaming Input Mode (Default & Recommended)" — claude-code).
//     SendInput delivers a turn as a single line; the adapter's
//     ParseLine projects each output line into typed StreamEvents.
//   - JsonRpcStdio: long-lived JSON-RPC 2.0 over stdio (Codex
//     `app-server`). Turn delivery requires the consumer to call
//     `initialize` + `thread/start` once per session and then
//     `turn/start` per turn — SendInput's raw-bytes path will NOT work
//     because the codex app-server rejects unframed JSON-RPC. Use
//     SendTurn (in this package) which routes by RuntimeKind.
//
// The substrate's per-provider matrix lives in selectRuntimeKind below;
// operators override per-profile via `profile.RuntimeKind: <kind>`.
type RuntimeKind string

const (
	RuntimeKindSubprocess     RuntimeKind = "subprocess"
	RuntimeKindPTY            RuntimeKind = "pty"
	RuntimeKindStreamingStdio RuntimeKind = "streaming-stdio"
	RuntimeKindJsonRpcStdio   RuntimeKind = "jsonrpc-stdio"
)

// validate reports whether the value is a known kind. Empty is the
// substrate-default sentinel ("ask selectRuntimeKind") and is not
// considered a validation failure here — callers that need to assert a
// non-default kind check string equality directly.
func (rk RuntimeKind) validate() error {
	switch rk {
	case "", RuntimeKindSubprocess, RuntimeKindPTY, RuntimeKindStreamingStdio, RuntimeKindJsonRpcStdio:
		return nil
	default:
		return fmt.Errorf("unknown runtime kind %q (expected: subprocess|pty|streaming-stdio|jsonrpc-stdio or empty for per-provider default)", string(rk))
	}
}

// selectRuntimeKind picks the agentsessions Runtime kind a session
// should use, given the Mode, the resolved provider, and the operator's
// profile-level override (profile.RuntimeKind).
//
// Decision priority (highest first):
//
//  1. profile.RuntimeKind, when non-empty, is the operator's explicit
//     pick. Subject to ModeOneShot's constraints (see #2) for now.
//
//  2. The per-provider default matrix, used when profile.RuntimeKind
//     is empty:
//
//     codex       → JsonRpcStdio  (app-server, JSON-RPC 2.0 over stdio)
//     claude-code → StreamingStdio (NDJSON-over-stdin)
//     claude      → Subprocess     (bare-mode subprocess-per-turn — config-bleed isolation requires bare)
//     opencode    → Subprocess     (no long-lived adapter exists)
//
// post-2026-05-13:
//
//   - shouldUsePTY's ModeOneShot short-circuit is gone. With boot.go's
//     ModeOneShot lifecycle fixed (turn-complete wait bounded by
//     profile.TimeoutSeconds — see boot.go ModeOneShot block), long-lived
//     adapters are valid in ModeOneShot. selectRuntimeKind no longer
//     special-cases ModeOneShot.
//   - The legacy `profile.PTY *bool` field is replaced by
//     `profile.RuntimeKind string`. No compat shim per
//     feedback_no_compat_shims — pre-launch, no operator config relied
//     on the old field name.
func selectRuntimeKind(provider string, profileKind string) (RuntimeKind, error) {
	kind := RuntimeKind(profileKind)
	if err := kind.validate(); err != nil {
		return "", err
	}
	if kind != "" {
		return kind, nil
	}
	switch provider {
	case "codex":
		return RuntimeKindJsonRpcStdio, nil
	case "claude-code":
		return RuntimeKindStreamingStdio, nil
	case "claude":
		return RuntimeKindSubprocess, nil
	case "opencode":
		return RuntimeKindSubprocess, nil
	case "":
		// Empty provider falls through to adapterFor's nominative-error
		// path; the runtime kind is meaningless without a known
		// provider.
		return RuntimeKindSubprocess, nil
	default:
		// Unknown provider — same rationale as the empty case. The
		// adapterFor path emits the actionable error; we pick a
		// conservative default here so the caller doesn't have to
		// wrap the error.
		return RuntimeKindSubprocess, nil
	}
}

// resolveRuntimeKind composes the runtime-kind resolution chain: the
// per-Boot override wins, then profile.RuntimeKind, then the
// per-provider default matrix. Empty input at each layer falls through
// to the next. Returns an error only when an explicit value (override
// or profile) fails validate(); per-provider defaults always validate.
func resolveRuntimeKind(profile config.AgentProfile, opts Options) (RuntimeKind, error) {
	if opts.RuntimeKindOverride != "" {
		if err := opts.RuntimeKindOverride.validate(); err != nil {
			return "", fmt.Errorf("Options.RuntimeKindOverride: %w", err)
		}
		return opts.RuntimeKindOverride, nil
	}
	return selectRuntimeKind(profile.Provider, profile.RuntimeKind)
}

// capabilitiesForRuntimeKind maps a RuntimeKind to the base
// agentsessions.Capabilities the runtime should declare. Provider-
// specific extras (e.g. claude's CheckpointResume) are layered on top in
// adapterFor.
//
// The lib's Runtime selector reads exactly one of {PTY, StreamingStdio,
// JsonRpcStdio} from Capabilities to pick which session implementation
// to spawn. All four kinds set BinaryRequired=true here (every adapter
// today shells out to a CLI binary).
func capabilitiesForRuntimeKind(kind RuntimeKind) agentsessions.Capabilities {
	switch kind {
	case RuntimeKindPTY:
		return agentsessions.Capabilities{
			BinaryRequired: true,
			PTY:            true,
			Resize:         true,
		}
	case RuntimeKindStreamingStdio:
		return agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			StreamingStdio:    true,
		}
	case RuntimeKindJsonRpcStdio:
		return agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			JsonRpcStdio:      true,
		}
	default:
		// Subprocess + empty default both land here.
		return agentsessions.Capabilities{
			BinaryRequired: true,
		}
	}
}
