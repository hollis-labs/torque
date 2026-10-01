package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentruntime/runtimebind"

	"github.com/hollis-labs/torque/internal/config"
)

// ProtectEnv is the kill switch for write-protecting Torque's control-plane
// directories from the agents it launches (CW-20261001-0141). Protection is
// on by default; "0", "false", "off" or "no" turns it off, so an operator can
// disable it without a rollback if the sandbox backend misbehaves on a host.
const ProtectEnv = "TORQUE_SANDBOX_PROTECT"

// ProtectionEnabled reports whether ProtectEnv leaves write protection on.
func ProtectionEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ProtectEnv))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// ProtectEnvRecognized reports whether ProtectEnv is unset or holds a value
// ProtectionEnabled reads: an unrecognised one (say "disable") leaves
// protection on, which an operator who meant to turn it off should be told.
func ProtectEnvRecognized() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ProtectEnv))) {
	case "", "1", "true", "on", "yes", "0", "false", "off", "no":
		return true
	}
	return false
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

// codexDefaultSandboxMode is the sandbox_mode go-providers plants for a codex
// launch that asks for none.
const codexDefaultSandboxMode = "workspace-write"

// codexBypassSandboxMode is the sandbox_mode an app-server launch under
// bypassPermissions plants. factory.go sets it on the adapter, and
// codexOwnSandbox asks for it, through the same agentkit function, so the two
// cannot drift.
func codexBypassSandboxMode(mode runtimes.Mode) (string, error) {
	policy, err := runtimebind.ResolveCodexPolicy(runtimebind.CodexPolicyRequest{Runtime: mode, Bypass: true})
	if err != nil {
		return "", err
	}
	return policy.SandboxMode, nil
}

// codexOwnSandbox reports the sandbox a codex launch runs the agent's
// commands under, and whether it is codex's own OS sandbox. It fails closed:
// own is true only when the launch POSITIVELY selects a codex sandbox_mode
// that confines writes, read-only or workspace-write, and every other answer
// leaves the launch wrapped in Torque's. That covers danger-full-access
// (however spelled), an unknown mode, selectors that disagree, and any
// argument bearing on the sandbox or its permissions that this does not
// understand (CW-20261001-0141, CW-20261001-0256).
//
// go-providers plants workspace-write; an app-server launch under
// bypassPermissions plants the mode codexBypassSandboxMode names; the
// profile's own args override the planted config on the command line.
//
// Precedence in codex-cli 0.159.3 (`codex exec`, checked against its session
// banner): --yolo and --dangerously-bypass-approvals-and-sandbox beat every
// other selector wherever they appear; --sandbox beats -c sandbox_mode= in
// either order; among -c overrides the last wins. Torque does not rely on
// that ranking: a bypass flag anywhere wraps, and when more than one mechanism
// (--sandbox/-s, --full-auto, -c sandbox_mode=) selects a mode, they must all
// agree on a confining one, or the launch is wrapped. Within -c the last
// override is the mechanism's value.
//
// The forms read: --sandbox/-s <mode>, --sandbox=<mode>, -c/--config
// sandbox_mode=<mode> and --full-auto (workspace-write). What is not read, and
// wraps the launch: any other -c/--config key that names a sandbox or
// permission (sandbox_workspace_write.writable_roots, default_permissions,
// …), --add-dir (a writable root), --profile/-p (a codex config profile that
// may set its own sandbox), a selector or flag missing its value, any
// single-dash argument with an attached value (-s=danger-full-access,
// -sdanger-full-access: clap accepts both), and any other argument naming a
// sandbox, permission, bypass or yolo.
//
// mode is the sandbox_mode identified, "" when none was.
func codexOwnSandbox(profile config.AgentProfile, kind RuntimeKind) (mode string, own bool) {
	if runtimeIDFor(profile.Provider) != string(runtimes.Codex) || kind.ACP() {
		return "", false
	}
	planted := codexDefaultSandboxMode
	if kind.Mode() == runtimes.ModeJSONRPCStdio && profile.ResolvedPermissionMode() == config.PermissionModeBypass {
		m, err := codexBypassSandboxMode(kind.Mode())
		if err != nil {
			return "", false
		}
		planted = m
	}

	var flagModes []string // --sandbox / -s, each occurrence
	var configMode string  // -c sandbox_mode=, the last override
	var configSet, fullAuto bool
	args := profile.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// value returns the argument that follows a selector flag; ok is
		// false when the flag is the last argument.
		value := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "--dangerously-bypass-approvals-and-sandbox" || arg == "--yolo":
			// Sticky: codex ranks it above every other selector, wherever it is.
			return "danger-full-access", false
		case arg == "--full-auto":
			fullAuto = true
		case arg == "--sandbox" || arg == "-s":
			v, ok := value()
			if !ok {
				return "", false
			}
			flagModes = append(flagModes, v)
		case strings.HasPrefix(arg, "--sandbox="):
			flagModes = append(flagModes, strings.TrimPrefix(arg, "--sandbox="))
		case arg == "-c" || arg == "--config" || strings.HasPrefix(arg, "--config="):
			kv := strings.TrimPrefix(arg, "--config=")
			if arg == "-c" || arg == "--config" {
				v, ok := value()
				if !ok {
					return "", false
				}
				kv = v
			}
			key, v := codexConfigOverride(kv)
			switch {
			case key == "sandbox_mode":
				configMode, configSet = v, true
			case codexSandboxish(key):
				return "", false
			}
		case arg == "--add-dir" || strings.HasPrefix(arg, "--add-dir="),
			arg == "--profile" || arg == "-p" || strings.HasPrefix(arg, "--profile="),
			isAttachedShortOption(arg),
			codexSandboxish(arg):
			return "", false
		}
	}

	// Each mechanism that selects a mode gets one vote: --sandbox (every
	// occurrence is its own, since codex refuses a repeat), --full-auto, and
	// -c sandbox_mode=. They must all agree.
	var selected []string
	selected = append(selected, flagModes...)
	if fullAuto {
		selected = append(selected, "workspace-write")
	}
	if configSet {
		selected = append(selected, configMode)
	}
	mode = planted
	if len(selected) > 0 {
		mode = selected[0]
		for _, m := range selected[1:] {
			if m != mode {
				return "", false
			}
		}
	}
	return mode, mode == "read-only" || mode == "workspace-write"
}

// isAttachedShortOption reports whether arg is a single-dash option with its
// value attached to it (-sdanger-full-access, -s=read-only, -cmodel=x): clap
// accepts those spellings, and Torque reads none of them.
func isAttachedShortOption(arg string) bool {
	return len(arg) > 2 && arg[0] == '-' && arg[1] != '-'
}

// codexConfigOverride splits a `-c key=value` override into its key and its
// value with TOML string quotes stripped.
func codexConfigOverride(kv string) (key, value string) {
	k, v, _ := strings.Cut(kv, "=")
	return strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
}

// codexSandboxish reports whether an argument or config key names a sandbox,
// permission, bypass or yolo.
func codexSandboxish(s string) bool {
	s = strings.ToLower(s)
	for _, word := range []string{"sandbox", "permission", "bypass", "yolo"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}
