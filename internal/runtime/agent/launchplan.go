package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/torque/internal/config"
)

// buildLaunchPlan and buildLaunchPlanInput moved to
// internal/launchprofile/{assemble,launchprofile}.go as part of the
// launch-profile refactor (2026-05-26). This file now hosts the small
// translation helpers boot.go still needs to bridge Torque's enums into
// the shared agentlaunch contract.

// mapProviderID normalizes a Torque provider name to the provider id the
// agentlaunch matrix recognizes. Torque models the claude streaming-stdio
// runtime as a distinct provider name ("claude-code") for adapter-factory
// purposes; the matrix models it as the streaming-stdio runtime of
// provider "claude". Every other name passes through verbatim.
func mapProviderID(torqueProvider string) string {
	if torqueProvider == "claude-code" {
		return "claude"
	}
	return torqueProvider
}

// mapRuntimeKind maps Torque's RuntimeKind onto the agentlaunch RuntimeKind
// taxonomy. The two enums are intentionally distinct types (Torque's
// predates the shared one); this is the single conversion point. An
// unrecognized kind is a hard error — Boot must not silently downgrade to
// subprocess.
//
// agentlaunch.RuntimeServeHTTP added in v0.4.0 alongside agentkit
// agentsessions (serve_http_session.go) + go-providers v0.23.0
// (NewOpencodeAdapterServeHTTP). Without this case, profiles opting into
// serve-http would fail Boot here with "unmappable runtime kind" before
// the session is started.
func mapRuntimeKind(k RuntimeKind) (agentlaunch.RuntimeKind, error) {
	switch k {
	case RuntimeKindSubprocess, "":
		return agentlaunch.RuntimeSubprocess, nil
	case RuntimeKindPTY:
		return agentlaunch.RuntimePTY, nil
	case RuntimeKindStreamingStdio:
		return agentlaunch.RuntimeStreamingStdio, nil
	case RuntimeKindJsonRpcStdio:
		return agentlaunch.RuntimeJsonRpcStdio, nil
	case RuntimeKindServeHTTP:
		return agentlaunch.RuntimeServeHTTP, nil
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
