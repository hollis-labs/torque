package bootstrap

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/go-sandbox/sandbox"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// ProtectControlPlane registers Torque's control-plane directories as the
// ProtectedPaths of every agent launch (CW-20261001-0141), so an agent
// running as the operator's uid cannot rewrite Torque's databases, config or
// session workspaces to grant itself authority. agent.ProtectEnv=0 turns it
// off. It logs what it protects, and loudly when the sandbox backend cannot
// write-protect: every launch is then refused (it fails closed) until the
// backend is fixed or protection is turned off.
func ProtectControlPlane(deps *agent.Dependencies, cfg *config.Config) {
	if deps == nil || cfg == nil {
		return
	}
	if !agent.ProtectionEnabled() {
		deps.ProtectedPaths = nil
		log.Printf("[sandbox] %s=0: agent launches do not write-protect Torque's state", agent.ProtectEnv)
		return
	}
	deps.ProtectedPaths = agent.ResolveProtectedPaths(safeControlPlaneDirs(controlPlaneCandidates(cfg, deps.WorkspacesRoot)))
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		log.Printf("[sandbox] ERROR: the %s sandbox backend on %s cannot write-protect paths, so every agent launch will be refused; set %s=0 to launch agents without protection", caps.Backend, caps.GOOS, agent.ProtectEnv)
	}
	log.Printf("[sandbox] agent launches write-protect %v (%s backend; %s=0 turns this off)", deps.ProtectedPaths, caps.Backend, agent.ProtectEnv)
}

// controlPlaneCandidates are the directories holding Torque's state: the
// data dir and the main database's dir, the state dir and the queue
// database's dir, the config dir and the profiles file's dir, the session
// workspaces root, and ~/.torque (default workspaces, agent templates).
func controlPlaneCandidates(cfg *config.Config, workspacesRoot string) []string {
	out := []string{
		cfg.DataDir,
		filepath.Dir(cfg.DBPath),
		cfg.StateDir,
		cfg.ConfigDir,
		filepath.Dir(cfg.ProfilesPath),
		workspacesRoot,
	}
	if q := cfg.Concurrency.QueueDBPath; q != "" {
		out = append(out, filepath.Dir(q))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".torque"))
	}
	return out
}

// safeControlPlaneDirs drops a candidate that is the root, the home
// directory or an ancestor of it: an override pointing a Torque path at one
// of those would write-protect everything the agent works in.
func safeControlPlaneDirs(candidates []string) []string {
	home, _ := os.UserHomeDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c == "" {
			continue
		}
		clean := filepath.Clean(c)
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			clean = real
		}
		if clean == string(filepath.Separator) || (home != "" && isAncestorOrSelf(clean, home)) {
			log.Printf("[sandbox] not write-protecting %s: it is / or contains the home directory", c)
			continue
		}
		out = append(out, c)
	}
	return out
}

// isAncestorOrSelf reports whether dir is path or one of its ancestors.
func isAncestorOrSelf(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
