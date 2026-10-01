package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentruntime/runtimebind"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/torque/internal/config"
)

// selectedRuntime is a profile's runtime as Boot launches it: the
// go-providers adapter Torque configured from the profile, the
// go-agent-wrapper adapter launch.Select returned for it, and the
// agentsessions capabilities the lib reads to pick the session shape.
type selectedRuntime struct {
	// cli is the configured go-providers adapter. bootLegacy drives it
	// directly; providerplant plants the boot dir from it on both paths.
	// Nil for an ACP mode, which has no go-providers adapter (bootACP).
	cli provider.CLIAdapter
	// wrapper is launch.Select's adapter for the same (runtime, mode),
	// built around cli. bootWrapper hands it to wrapper.Config.Adapter.
	wrapper adapters.Adapter
	caps    agentsessions.Capabilities
}

// selectRuntime resolves the profile's provider through the go-providers
// runtime registry and launches it with go-agent-wrapper's launch.Select
// (CW-20260930-0134). It replaces the per-provider constructor switch:
//
//  1. The provider must name a registry runtime (an id such as `codex`, or
//     an alias such as `claude-code` or `agy`). The bare `claude` name stays
//     retired (see below).
//  2. The mode is the resolved RuntimeKind, passed to Select explicitly so
//     a registry default can never change what an existing profile runs
//     (Codex's registry default is jsonrpc-stdio, Torque's too, but the
//     choice stays Torque's).
//  3. go-providers' provider.NewAdapter builds the adapter in that mode's
//     shape, the constructor table agentkit's planting path shares, and
//     the profile's options are set on it (applyProfileOptions).
//  4. launch.Select validates the pair against the registry and the
//     wrapper's closed factory set and returns the wrapper adapter. A mode
//     it does not drive, Claude's PTY TUI for one, is refused here, before
//     anything is planted.
//
// An ACP mode (Copilot and Pi have no other; Claude, Codex and OpenCode
// take acp-stdio from profile.RuntimeKind) has no go-providers adapter:
// launch.Select builds the wrapper's ACP adapter from the runtime and mode
// alone, and bootACP launches it without a planted boot dir
// (CW-20261001-0097).
//
// profileName is the torque agent-profile lookup key. OpenCode needs it:
// `opencode run` dispatches via `--agent <name>`, and by convention the
// torque profile name is the opencode agent name.
func selectRuntime(profile config.AgentProfile, profileName string, kind RuntimeKind) (selectedRuntime, error) {
	switch profile.Provider {
	case "":
		return selectedRuntime{}, fmt.Errorf(
			"profile has empty provider; agent.Boot requires a runtime from the go-providers registry (%s)", launchableProviderList())
	case "claude":
		// Bare-mode claude (subprocess-per-turn) and claude PTY were
		// retired 2026-05-16. Bare mode's unsolved problem was the
		// OAuth/"Not logged in" auth gap; claude-code (streaming-stdio)
		// does not have it and is the supported claude path.
		return selectedRuntime{}, fmt.Errorf(
			"bare claude provider retired 2026-05-16; use provider=claude-code (streaming-stdio)")
	}
	desc, ok := registry.Lookup(profile.Provider)
	if !ok {
		return selectedRuntime{}, fmt.Errorf(
			"unknown provider %q; agent.Boot accepts the go-providers registry runtimes: %s", profile.Provider, launchableProviderList())
	}
	mode := kind.Mode()
	if mode == "" {
		mode = desc.DefaultMode
	}
	if !desc.Supports(mode) {
		return selectedRuntime{}, fmt.Errorf(
			"%s provider does not support runtime kind %q; supported: %s", profile.Provider, string(mode), modeList(supportedModes(desc)))
	}
	if mode.ACP() {
		return selectACPRuntime(profile, desc, mode)
	}
	cli, err := provider.NewAdapter(desc.ID, mode)
	if err != nil {
		return selectedRuntime{}, fmt.Errorf("%s provider, runtime kind %q: %w", profile.Provider, string(mode), err)
	}
	if err := applyProfileOptions(cli, profile, profileName, mode); err != nil {
		return selectedRuntime{}, err
	}
	launched, err := launch.Select(launch.Selection{
		Runtime:    string(desc.ID),
		Mode:       mode,
		CLIAdapter: cli,
	})
	if err != nil {
		return selectedRuntime{}, fmt.Errorf("%s provider, runtime kind %q: %w", profile.Provider, string(mode), err)
	}
	return selectedRuntime{cli: cli, wrapper: launched, caps: capabilitiesForRuntimeKind(RuntimeKind(mode))}, nil
}

// adapterFor returns the configured go-providers adapter and capabilities
// for the profile's runtime; see selectRuntime.
func adapterFor(profile config.AgentProfile, profileName string, kind RuntimeKind) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	sel, err := selectRuntime(profile, profileName, kind)
	if err != nil {
		return nil, agentsessions.Capabilities{}, err
	}
	return sel.cli, sel.caps, nil
}

// applyProfileOptions sets the profile's options on the adapter
// provider.NewAdapter built. Every runtime takes the profile's model on its
// adapter: the projected launch convention places it in the provider's own
// spelling (`--model`, or codex's `-c model=`), before the extra-argument
// slot and any `-- <prompt>`, on every turn (CW-20261001-0094). Beyond that:
//
//   - Claude: --dangerously-skip-permissions in profile.Args selects the
//     developer variant (SkipPermissions, which go-providers plants as
//     permissions.defaultMode=bypassPermissions); otherwise the resolved
//     permission mode is planted into .claude/settings.json
//     (CW-20260517-0038).
//   - Codex app-server: an operator's explicit bypassPermissions disables
//     codex's OS sandbox (danger-full-access) so the MCP loopback is
//     reachable; nothing else changes the sandbox. The exec path is left as
//     it was.
//   - OpenCode: `--agent <profile name>`.
//   - Antigravity (agy): the resolved permission mode as agy's headless
//     posture (bypassPermissions → bypass, acceptEdits → accept-edits,
//     plan → plan, default → agy's own auto-deny).
//
// The profile's own args are not adapter fields; Boot adds them to the
// prepared launch template (torqueLaunchArgs).
func applyProfileOptions(cli provider.CLIAdapter, profile config.AgentProfile, profileName string, mode runtimes.Mode) error {
	switch a := cli.(type) {
	case *provider.ClaudeAdapter:
		a.Model = profile.Model
		if profileIsDevMode(profile) {
			a.SkipPermissions = true
			return nil
		}
		a.PermissionMode = string(profile.ResolvedPermissionMode())
	case *provider.CodexAdapter:
		a.Model = profile.Model
		if mode != runtimes.ModeJSONRPCStdio || profile.ResolvedPermissionMode() != config.PermissionModeBypass {
			return nil
		}
		policy, err := runtimebind.ResolveCodexPolicy(runtimebind.CodexPolicyRequest{Runtime: mode, Bypass: true})
		if err != nil {
			return fmt.Errorf("resolve codex policy: %w", err)
		}
		a.SandboxMode = policy.SandboxMode
	case *provider.OpencodeAdapter:
		if profileName == "" {
			return fmt.Errorf("opencode provider requires Options.AgentProfile to be set (maps to opencode --agent)")
		}
		a.Agent = profileName
		a.Model = profile.Model
	case *provider.AntigravityAdapter:
		a.Model = profile.Model
		a.Permission = antigravityPermission(profile.ResolvedPermissionMode())
	}
	return nil
}

// antigravityPermission maps a profile permission mode onto agy's posture
// vocabulary (go-providers AntigravityAdapter.Permission).
func antigravityPermission(mode config.PermissionMode) string {
	switch mode {
	case config.PermissionModeBypass:
		return "bypass"
	case config.PermissionModeAcceptEdits:
		return "accept-edits"
	case config.PermissionModePlan:
		return "plan"
	default:
		return ""
	}
}

func launchableProviderList() string { return strings.Join(config.LaunchableProviders(), ", ") }

// supportedModes lists every mode the registry declares for the runtime,
// native and ACP.
func supportedModes(desc registry.Descriptor) []runtimes.Mode {
	out := make([]runtimes.Mode, len(desc.Modes))
	for i, ms := range desc.Modes {
		out[i] = ms.Mode
	}
	return out
}

func modeList(modes []runtimes.Mode) string {
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = string(m)
	}
	return strings.Join(out, ", ")
}

// shouldDropBootDirExtraArgs reports whether bootLegacy must drop the
// prepared launch's Argv[1:] instead of splicing it onto
// StartOptions.ExtraArgs for the given provider + runtime kind.
//
// opencode serve-http is the only case. On the legacy path the serve-http
// runtime builds its own `serve --port 0 --hostname 127.0.0.1` argv from the
// adapter and appends ExtraArgs after it, so the prepared copy must not
// follow:
//
//   - Before agentkit v0.12.0 the prepared argv carried `--dir <project>`,
//     which `opencode serve` rejects (exit before printing the listen URL;
//     CW-20260521-0022).
//   - Since v0.12.0 the projection carries no `--dir` for http-sse but the
//     whole serve command, which would repeat it (CW-20261001-0064).
//
// It does not apply on the wrapper path, where the prepared argv is the
// whole command and the wrapper adapter adds nothing; trimming there
// launched a bare `opencode`. Every other provider/runtime keeps its
// ExtraArgs. The argv-owner work (CW-20260930-0135) retires this.
func shouldDropBootDirExtraArgs(provider string, kind RuntimeKind) bool {
	return provider == "opencode" && kind == RuntimeKindServeHTTP
}

// legacyRuntimeKindAllowed reports whether bootLegacy may launch kind.
// Production routes only codex app-server (jsonrpc-stdio) there: every
// other kind launches through go-agent-wrapper, and PTY has no launch
// factory. bootLegacy's argv splice is right for app-server alone (see the
// ExtraArgs comment there), so another kind is refused rather than spawned
// with its command repeated. The RuntimeFactory test seam may run any kind;
// its runtimes spawn nothing (CW-20261001-0080).
func legacyRuntimeKindAllowed(kind RuntimeKind, testSeam bool) bool {
	return testSeam || kind == RuntimeKindJsonRpcStdio
}

// profileIsDevMode reports whether the profile opts into Claude's
// `--dangerously-skip-permissions` developer-mode flag. Forked from
// cliexec.ProfileIsDevMode (which is being deleted in P6).
func profileIsDevMode(profile config.AgentProfile) bool {
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			return true
		}
	}
	return false
}

// ProfileIsDevMode is exported so external callers (e.g. anyone migrating
// off of cliexec.ProfileIsDevMode) can re-derive the predicate.
func ProfileIsDevMode(profile config.AgentProfile) bool {
	return profileIsDevMode(profile)
}

// profileArgsExcludingDevFlag returns profile.Args with
// --dangerously-skip-permissions stripped. The dev flag is consumed by
// adapterFor to pick NewClaudeAdapterDev; passing it through profile.Args
// would double-add the flag.
func profileArgsExcludingDevFlag(profile config.AgentProfile) []string {
	if len(profile.Args) == 0 {
		return nil
	}
	out := make([]string, 0, len(profile.Args))
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			continue
		}
		out = append(out, a)
	}
	return out
}
