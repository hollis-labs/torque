package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/registry"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtimetoken"
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
//   - ServeHTTP: long-lived child exposing an HTTP API with server-
//     sent events (opencode `serve`). go-agent-sessions spawns
//     `opencode serve --port 0 --hostname 127.0.0.1`, captures the
//     bound port from stdout, then attaches via the child's HTTP API
//     for session + message endpoints. Added 2026-05-21 alongside
//     go-providers v0.23.0 (NewOpencodeAdapterServeHTTP) +
//     go-agent-sessions v0.10.0 (serve_http_session.go).
//
// The per-runtime default comes from the go-providers registry (see
// selectRuntimeKind); operators override per-profile via
// `profile.RuntimeKind: <kind>`.
//
// The values are agent-contracts-leaf runtimes.Mode spellings, the
// vocabulary agentkit v0.12.0 takes everywhere (D-73). Two differ from the
// tokens Torque used before: Subprocess is `subprocess-per-turn` (was
// `subprocess`) and ServeHTTP is `http-sse` (was `serve-http`).
// ParseRuntimeKind still accepts the old tokens, which live profiles and
// stored session rows carry (CW-20261001-0064).
type RuntimeKind string

const (
	RuntimeKindSubprocess     RuntimeKind = RuntimeKind(runtimes.ModeSubprocessPerTurn)
	RuntimeKindPTY            RuntimeKind = RuntimeKind(runtimes.ModePTY)
	RuntimeKindStreamingStdio RuntimeKind = RuntimeKind(runtimes.ModeStreamingStdio)
	RuntimeKindJsonRpcStdio   RuntimeKind = RuntimeKind(runtimes.ModeJSONRPCStdio)
	RuntimeKindServeHTTP      RuntimeKind = RuntimeKind(runtimes.ModeHTTPSSE)
)

// Mode returns the kind as the runtimes.Mode the libraries take.
func (rk RuntimeKind) Mode() runtimes.Mode { return runtimes.Mode(rk) }

// validate reports whether the value is a known kind. Empty is the
// substrate-default sentinel ("ask selectRuntimeKind") and is not
// considered a validation failure here — callers that need to assert a
// non-default kind check string equality directly.
func (rk RuntimeKind) validate() error {
	switch rk {
	case "", RuntimeKindSubprocess, RuntimeKindPTY, RuntimeKindStreamingStdio, RuntimeKindJsonRpcStdio, RuntimeKindServeHTTP:
		return nil
	default:
		return fmt.Errorf("unknown runtime kind %q (expected: subprocess-per-turn|pty|streaming-stdio|jsonrpc-stdio|http-sse, the older subprocess|serve-http, or empty for the runtime's default)", string(rk))
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
//     opencode    → Subprocess     (default; ServeHTTP is opt-in via
//     profile.RuntimeKind=serve-http for
//     long-lived multi-turn workers)
//
// `claude` (bare) was retired 2026-05-16 — it has no matrix entry and
// adapterFor rejects it; use claude-code.
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
	kind := runtimeKindFromConfigValue(profileKind)
	if err := RuntimeKind(kind).validate(); err != nil {
		return "", err
	}
	if kind != "" {
		return RuntimeKind(kind), nil
	}
	// The runtime's own default: codex jsonrpc-stdio (app-server),
	// claude streaming-stdio, opencode subprocess-per-turn.
	if desc, ok := registry.Lookup(provider); ok {
		return RuntimeKind(desc.DefaultMode), nil
	}
	// An empty or unknown provider gets adapterFor's actionable error;
	// a conservative default here spares the caller wrapping one.
	return RuntimeKindSubprocess, nil
}

// ParseRuntimeKind normalizes a runtime-kind token from a profile or a
// stored session row through runtimetoken: the pre-v0.12.0 spellings
// (subprocess, cli, serve-http, app-server, pty-debug) map onto their
// current ones. agentkit fails an old token with ErrUnknownRuntime, so this
// translation happens at Torque's boundary. An unknown token is returned
// as given for validate to reject.
func ParseRuntimeKind(raw string) RuntimeKind {
	return runtimeKindFromConfigValue(raw)
}

func runtimeKindFromConfigValue(raw string) RuntimeKind {
	tok, err := runtimetoken.Normalize(raw)
	if err != nil {
		return RuntimeKind(strings.TrimSpace(raw))
	}
	// tok.Debug (pty-debug) has no Torque posture to carry it; the kind
	// is pty.
	return RuntimeKind(tok.Mode)
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
// The lib's Runtime selector reads at most one of {PTY, StreamingStdio,
// JsonRpcStdio, ServeHTTP} from Capabilities to pick which long-lived
// session implementation to spawn — those four are mutually exclusive
// (enforced in go-agent-sessions v0.10.0's Capabilities.Validate).
// Subprocess sets none of the lifecycle flags — it's the fallback shape
// (one-shot fork-exec per turn), distinguished by absence rather than a
// dedicated flag. All five kinds set BinaryRequired=true here (every
// adapter today shells out to a CLI binary).
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
	case RuntimeKindServeHTTP:
		return agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			ServeHTTP:         true,
		}
	default:
		// Subprocess + empty default both land here.
		return agentsessions.Capabilities{
			BinaryRequired: true,
		}
	}
}
