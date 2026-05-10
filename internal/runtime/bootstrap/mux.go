package bootstrap

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// muxResolution captures the bootstrap-time decision about how every
// per-task bootdir plant should expose Mux to the spawned agent.
//
// Empty Command means "no Mux entry"; the bootdir plant then carries
// only the per-task clockwork loopback (the existing pre-CW-20260510-0110
// behavior). Non-empty Command flows into PlantContext.MuxCommand /
// MuxArgs / MuxEnv at Boot time so go-providers' renderers emit a second
// `mux` MCP server entry alongside the loopback.
//
// Why a single resolution at bootstrap (not per-Boot): the Mux binary
// + args don't vary per task. Resolving once keeps the per-Boot
// fast-path free of filesystem stat / env-var lookups and gives the
// daemon a single startup log line that operators can correlate with
// downstream auth/MCP behavior.
type muxResolution struct {
	Command string
	Args    []string
	Env     []string
}

// defaultMuxArgs mirrors the user's interactive ~/.claude.json
// `mcpServers.mux` shape — verified against the user's actual config
// at the time of CW-20260510-0110. The token + scopes here are the
// project-default development values; operators with hardened
// deployments override via CLOCKWORK_MUX_ARGS (whitespace-split tokens)
// or via direct config plumbing in the future. (A JSON-array variant —
// CLOCKWORK_MUX_ARGS_JSON — is a deferred follow-up; not wired yet.)
//
// Spelling these out as a single source of truth prevents drift between
// the per-task plant shape and the user's interactive shell shape —
// agents that work interactively will see the same aggregator surface
// when dispatched as a clockwork task.
//
// Mirroring the interactive shape verbatim is also the lowest-risk
// initial default: anything the interactive `claude → mux` flow can do,
// the per-task agent can also do (modulo whatever stdio-ownership
// semantics differ between claude and opencode/codex).
var defaultMuxArgs = []string{
	"mcp", "--proxy",
	"--servers", "vanta,clockwork,cerberus",
	"--token", "local-dev",
	"--scopes", "session.write,message.write",
}

// resolveMuxConfig discovers the Mux binary path + args the per-task
// bootdir plant should reference. Returns a zero-value muxResolution
// (empty Command) when Mux is not available; the bootdir plant then
// emits no `mux` MCP server entry (CW-20260510-0110 back-compat fence —
// existing planted shape stays byte-identical for callers without
// Mux installed).
//
// Resolution order for the binary path:
//
//  1. CLOCKWORK_MUX_COMMAND env var (operator override; useful for
//     dev sessions where the binary lives in a non-standard dir).
//  2. <dir(os.Executable())>/mux — the production deployment shape:
//     when cerberus syncs both binaries into the same artifact dir.
//  3. exec.LookPath equivalent against the daemon's PATH — fallback
//     for non-cerberus deployments where mux is on PATH (the common
//     case for `go install ./...` developer setups).
//
// Args resolution: defaults to the canonical interactive shape
// (defaultMuxArgs). CLOCKWORK_MUX_ARGS env var, when set, overrides
// the default. The value is parsed as whitespace-split tokens with no
// quoting; operators with values containing spaces will need to wait
// for the deferred CLOCKWORK_MUX_ARGS_JSON follow-up or for structured
// config plumbing.
//
// Whitespace-only override behavior: if CLOCKWORK_MUX_ARGS is set but
// strings.Fields() returns an empty slice (whitespace-only value), we
// log a WARN and fall back to defaultMuxArgs rather than emitting a
// "mux with no args" invocation. Empty-after-Fields is much more likely
// a misconfiguration than a deliberate "run mux bare" request, so the
// safer default is to keep the canonical shape.
//
// Env passthrough is left at zero today: spawned agents inherit the
// daemon's env (which already carries auth tokens, $HOME, $PATH).
// Per-Mux-entry env scoping would require a separate config surface
// and is filed as a follow-up.
//
// Returns a clearly-logged result regardless of outcome so operators
// can correlate auth/MCP behavior with whether Mux is wired in. The
// override-log line redacts the raw env-var value (it can carry
// secrets — `--token <real>`, `--api-key <real>`, etc.) and surfaces
// only the parsed token count.
func resolveMuxConfig() muxResolution {
	command := resolveMuxCommand()
	if command == "" {
		log.Printf("[bootstrap] mux NOT resolved (no CLOCKWORK_MUX_COMMAND override, no sibling binary, no PATH match) — per-task bootdir plants will carry only the clockwork loopback (no Vanta / cross-task / cerberus access for spawned agents)")
		return muxResolution{}
	}

	// Defensive copy of the package-level defaultMuxArgs so consumers
	// downstream (Dependencies → plantParams → go-providers renderers)
	// can't mutate the shared backing slice via append/aliasing. Cheap
	// (small, fixed slice) and prevents an entire class of "why did the
	// args list grow across tasks?" footguns.
	args := append([]string(nil), defaultMuxArgs...)
	if override := os.Getenv("CLOCKWORK_MUX_ARGS"); override != "" {
		// Whitespace-split with no quoting. Operators needing values
		// with spaces (rare for Mux args) will need the deferred
		// CLOCKWORK_MUX_ARGS_JSON path or future structured config.
		parsed := strings.Fields(override)
		if len(parsed) == 0 {
			// Whitespace-only override → fall back to defaults (safer
			// than emitting a bare-`mux` invocation; empty-after-Fields
			// is far more likely a misconfiguration than intent). Log a
			// WARN so operators can spot the bad value.
			log.Printf("[bootstrap] WARN: CLOCKWORK_MUX_ARGS is set but parses to zero tokens (whitespace-only?); falling back to default args")
		} else {
			args = parsed
			// Redacted log: never echo the raw value — it routinely
			// carries secrets (`--token <real>`, `--api-key <real>`,
			// etc.). Operators who need to debug already have the env
			// var on hand; the daemon log is not the right channel for
			// the verbatim value.
			log.Printf("[bootstrap] mux args overridden via CLOCKWORK_MUX_ARGS (%d tokens; values redacted)", len(parsed))
		}
	}

	return muxResolution{
		Command: command,
		Args:    args,
		// Env passthrough deferred — see godoc.
		Env: nil,
	}
}

// resolveMuxCommand handles just the binary-path resolution branch of
// resolveMuxConfig. Split out so unit tests can probe each leg
// independently and so the args/env logic above isn't drowned in path
// resolution boilerplate.
//
// Mirrors the resolveApiKeyHelperPath shape from agent.go (same
// override-then-sibling-then-PATH order, same isExecutableFile
// predicate, same EvalSymlinks normalization for the override path).
func resolveMuxCommand() string {
	if override := os.Getenv("CLOCKWORK_MUX_COMMAND"); override != "" {
		// Normalize to absolute + symlink-resolved so the contract
		// "absolute path" holds even when an operator sets a relative
		// path or routes through a symlink. Failures fall back to the
		// unresolved override; the executable-file check below catches
		// outright bogus paths regardless.
		resolved := override
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		if eval, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = eval
		}
		if isExecutableFile(resolved) {
			log.Printf("[bootstrap] mux resolved via CLOCKWORK_MUX_COMMAND=%s", resolved)
			return resolved
		}
		log.Printf("[bootstrap] CLOCKWORK_MUX_COMMAND=%s (resolved=%s) set but path is not an executable file; ignoring", override, resolved)
	}

	exe, err := os.Executable()
	if err == nil {
		// Resolve symlinks so an artifact-dir mux is found even when
		// the daemon was launched via a symlink.
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		candidate := filepath.Join(filepath.Dir(exe), "mux")
		if isExecutableFile(candidate) {
			log.Printf("[bootstrap] mux resolved next to daemon binary at %s", candidate)
			return candidate
		}
	}

	// PATH lookup as a last resort — homebrew installs, dev `go install`
	// targets, etc.
	if path, lookErr := lookExecOnPath("mux"); lookErr == nil {
		log.Printf("[bootstrap] mux resolved on PATH at %s", path)
		return path
	}

	return ""
}
