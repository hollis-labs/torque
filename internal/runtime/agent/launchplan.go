package agent

import (
	"fmt"
	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
	"strings"

	"github.com/hollis-labs/torque/internal/config"
)

// buildLaunchPlan and buildLaunchPlanInput moved to
// internal/launchprofile/{assemble,launchprofile}.go as part of the
// launch-profile refactor (2026-05-26). This file now hosts the small
// translation helpers boot.go still needs to bridge Torque's enums into
// the shared agentlaunch contract.

// runtimeIDFor resolves a profile's provider name to the go-providers
// registry's canonical runtime id, which the launch plan, the matrix and
// go-agent-wrapper all key on: "claude-code" is an alias of "claude". A name
// the registry does not know passes through unchanged so the caller's own
// validation names it. Replaces mapProviderID and adaptersProviderFor, which
// each kept a copy of the claude-code mapping (CW-20261001-0064).
func runtimeIDFor(provider string) string {
	if desc, ok := registry.Lookup(provider); ok {
		return string(desc.ID)
	}
	return provider
}

// mapRuntimeKind maps Torque's RuntimeKind onto the runtimes.Mode the
// launch plan carries (agentkit v0.12.0 replaced agentlaunch.RuntimeKind
// with the leaf vocabulary). RuntimeKind's values already are leaf
// spellings, so this is the place a kind is checked before it reaches the
// libraries. An unrecognized kind is a hard error — Boot must not silently
// downgrade to subprocess-per-turn.
func mapRuntimeKind(k RuntimeKind) (runtimes.Mode, error) {
	switch k {
	case "":
		return runtimes.ModeSubprocessPerTurn, nil
	case RuntimeKindSubprocess, RuntimeKindPTY, RuntimeKindStreamingStdio, RuntimeKindJsonRpcStdio, RuntimeKindServeHTTP:
		return k.Mode(), nil
	default:
		return "", fmt.Errorf("unmappable runtime kind %q", string(k))
	}
}

// resolveLaunchPermissionMode derives the permission posture Torque threads
// onto the launch plan. Threaded onto the plan so the agentlaunch compile
// guard (ErrHeadlessClaudeNeedsPermission) is a real backstop for the
// headless Mode boot.go sets: a claude launch that reaches Compile with no
// posture fails fast, instead of dispatching a run structurally guaranteed
// to hang on the first approval prompt.
//
// This is guard input only — the posture is actually delivered to the
// spawned agent by the adapter (factory.go: ClaudeAdapter.PermissionMode),
// since Torque plants via providerplant.WithAdapter, not DefaultResolver.
// codex is left empty: the guard exempts it (go-providers defaults codex
// approval_policy to the non-interactive "never").
func resolveLaunchPermissionMode(profile config.AgentProfile) string {
	if profile.Provider != "claude-code" {
		return ""
	}
	if profileIsDevMode(profile) {
		return string(config.PermissionModeBypass)
	}
	return string(profile.ResolvedPermissionMode())
}

// muxEnvSliceToMap converts Torque's Dependencies.MuxEnv ([]string of
// "KEY=VALUE" entries) into the map[string]string form
// PreparedPlantContext.SelfMCPEnv expects. Malformed entries (no "=") and
// empty keys are dropped — providerplant flattens the map back to the
// sorted "KEY=VALUE" slice provider.PlantContext.MuxEnv uses, so a
// malformed pass-through would just be silently re-emitted; dropping is
// the cleaner contract. Nil/empty in → nil out.
func muxEnvSliceToMap(in []string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for _, kv := range in {
		key, val, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergePreparedEnv appends the bootdir-derived env amendments
// sessionshim.ToSessionLaunch resolved from PreparedLaunch.Env (e.g.
// CODEX_HOME, OPENCODE_CONFIG_DIR) onto Torque's composed env slice. The
// amendments are appended last so they take precedence; os/exec resolves a
// duplicate KEY to the last occurrence, and go-agent-sessions forwards the
// slice verbatim.
func mergePreparedEnv(base []string, amendments []string) []string {
	if len(amendments) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for _, kv := range amendments {
		key, _, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		out = append(out, kv)
	}
	return out
}
