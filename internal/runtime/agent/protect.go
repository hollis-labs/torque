package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/torque/internal/config"
)

// ProtectEnv is the kill switch for write-protecting Torque's control-plane
// directories from the agents it launches (CW-20261001-0141). Protection is
// on by default; "0", "false", "off" or "no" turns it off, so an operator can
// disable it without a rollback if the sandbox backend misbehaves on a host.
const ProtectEnv = "TORQUE_SANDBOX_PROTECT"

// errACPProtectUnsupported is the refusal an ACP boot gets while protection
// is on: go-agent-wrapper has no protect-only sandbox for ACP launches yet.
const errACPProtectUnsupported = "ACP sandbox protect not yet supported (CW-20261001-0162); set " + ProtectEnv + "=0 to launch ACP unprotected"

// ProtectionEnabled reports whether ProtectEnv leaves write protection on.
func ProtectionEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ProtectEnv))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// ResolveProtectedPaths turns candidate control-plane directories into the
// list an agent launch protects. go-sandbox protects directories only, by
// their real path, and refuses a missing one the agent could create, so each
// candidate is resolved through symlinks and kept only if it is an existing
// directory. Duplicates and directories inside another kept one are dropped.
// The result is sorted.
func ResolveProtectedPaths(candidates []string) []string {
	var dirs []string
	for _, c := range candidates {
		if c == "" || !filepath.IsAbs(c) {
			continue
		}
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if st, err := os.Stat(real); err != nil || !st.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Clean(real))
	}
	slices.Sort(dirs)
	dirs = slices.Compact(dirs)
	// Sorted, a directory can still sit after an unrelated sibling that
	// separates it from its parent ("/a", "/a-b", "/a/c"), so each is checked
	// against every directory kept so far.
	var out []string
	for _, d := range dirs {
		if !slices.ContainsFunc(out, func(kept string) bool { return within(d, kept) }) {
			out = append(out, d)
		}
	}
	return out
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// launchProtectedPaths returns the directories this launch write-protects.
// It refuses the launch when protection is on but could not be set up
// (deps.ProtectRefusal): Torque fails closed rather than launching an agent
// that can rewrite its state. A codex launch that runs its commands in
// codex's own OS sandbox gets none: codex's sandbox confines their writes to
// the working directory, temp and its writable_roots, and inside Torque's
// sandbox it could not start (a nested user namespace is denied).
func launchProtectedPaths(deps *Dependencies, profile config.AgentProfile, kind RuntimeKind, sessID string) ([]string, error) {
	if deps.ProtectRefusal != "" {
		return nil, fmt.Errorf("%s", deps.ProtectRefusal)
	}
	if len(deps.ProtectedPaths) == 0 {
		return nil, nil
	}
	if mode, own := codexOwnSandbox(profile, kind); own {
		log.Printf("agent.Boot: session=%s: codex runs its commands in its own %s sandbox, so Torque does not write-protect this launch (a sandbox cannot nest inside Torque's)", sessID, mode)
		return nil, nil
	}
	return deps.ProtectedPaths, nil
}

// codexOwnSandbox reports the sandbox_mode a codex launch runs the agent's
// commands under, and whether that is codex's own OS sandbox, that is
// anything but danger-full-access. go-providers plants workspace-write; an
// app-server launch under bypassPermissions plants danger-full-access
// (applyProfileOptions); the profile's own args override the planted config
// on the command line.
func codexOwnSandbox(profile config.AgentProfile, kind RuntimeKind) (string, bool) {
	if runtimeIDFor(profile.Provider) != string(runtimes.Codex) || kind.ACP() {
		return "", false
	}
	mode := "workspace-write"
	if kind.Mode() == runtimes.ModeJSONRPCStdio && profile.ResolvedPermissionMode() == config.PermissionModeBypass {
		mode = "danger-full-access"
	}
	args := profile.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dangerously-bypass-approvals-and-sandbox":
			mode = "danger-full-access"
		case (arg == "--sandbox" || arg == "-s") && i+1 < len(args):
			i++
			mode = args[i]
		case strings.HasPrefix(arg, "--sandbox="):
			mode = strings.TrimPrefix(arg, "--sandbox=")
		case (arg == "-c" || arg == "--config") && i+1 < len(args):
			i++
			if v, ok := codexSandboxOverride(args[i]); ok {
				mode = v
			}
		case strings.HasPrefix(arg, "--config="):
			if v, ok := codexSandboxOverride(strings.TrimPrefix(arg, "--config=")); ok {
				mode = v
			}
		}
	}
	return mode, mode != "danger-full-access"
}

// codexSandboxOverride reads a `-c sandbox_mode=<toml string>` override.
func codexSandboxOverride(kv string) (string, bool) {
	key, value, ok := strings.Cut(kv, "=")
	if !ok || strings.TrimSpace(key) != "sandbox_mode" {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(value), `"'`), true
}
