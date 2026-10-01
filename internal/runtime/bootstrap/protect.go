package bootstrap

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/hollis-labs/go-sandbox/sandbox"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/orchestrator"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// ProtectControlPlane registers Torque's control-plane directories as the
// ProtectedPaths of every agent launch (CW-20261001-0141), so an agent
// running as the operator's uid cannot rewrite Torque's databases, config or
// session workspaces to grant itself authority. agent.ProtectEnv=0 turns it
// off, with a warning.
//
// It fails closed: when protection is on but no directory can be protected,
// or one is reached through a symlink an agent could re-point, it sets
// deps.ProtectRefusal and every launch is refused. A sandbox backend that
// cannot write-protect refuses every launch too, and this logs it at
// startup. Each candidate is logged as protected or skipped, with why.
//
// While protection is on, the planted mux server serves no torque tools
// (deps.MuxOmitsTorque): mux would run `torque mcp` inside the agent's
// sandbox, where Torque's database is read-only.
func ProtectControlPlane(deps *agent.Dependencies, cfg *config.Config) {
	if deps == nil || cfg == nil {
		return
	}
	deps.ProtectedPaths, deps.ProtectRefusal = nil, ""
	if !agent.ProtectionEnabled() {
		log.Printf("[sandbox] WARN: %s=%q: agent launches do not write-protect Torque's state", agent.ProtectEnv, os.Getenv(agent.ProtectEnv))
		return
	}
	paths, refusal := resolveControlPlane(controlPlaneCandidates(cfg, deps))
	if refusal == "" && len(paths) == 0 {
		refusal = "no Torque control-plane directory could be write-protected"
	}
	if refusal != "" {
		deps.ProtectRefusal = fmt.Sprintf("%s, so agent launches are refused; set %s=0 to launch agents without protection", refusal, agent.ProtectEnv)
		log.Printf("[sandbox] ERROR: %s", deps.ProtectRefusal)
		return
	}
	deps.ProtectedPaths = paths
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		log.Printf("[sandbox] ERROR: the %s sandbox backend on %s cannot write-protect paths, so every agent launch will be refused; set %s=0 to launch agents without protection", caps.Backend, caps.GOOS, agent.ProtectEnv)
	}
	log.Printf("[sandbox] agent launches write-protect %v (%s backend; %s=0 turns this off)", deps.ProtectedPaths, caps.Backend, agent.ProtectEnv)
	guardProfilesReload(deps, cfg)
	muxWithoutTorque(deps)
	warnWorkInsideProtected(deps, cfg)
}

// controlPlaneDir is a directory holding Torque state, and what it is.
type controlPlaneDir struct {
	what, path string
}

// controlPlaneCandidates are the directories holding Torque's state: the
// data dir and the main database's dir, the state dir and the queue
// database's dir, the config dir and the dir of the profiles file the
// daemon actually reads, the session workspaces root, ~/.torque (default
// workspaces, agent templates), and the agent and end-agent template dirs
// when they are overridden.
func controlPlaneCandidates(cfg *config.Config, deps *agent.Dependencies) []controlPlaneDir {
	out := []controlPlaneDir{
		{"data dir", cfg.DataDir},
		{"main database dir", dirOf(cfg.DBPath)},
		{"state dir", cfg.StateDir},
		{"queue database dir", dirOf(cfg.Concurrency.QueueDBPath)},
		{"config dir", cfg.ConfigDir},
		{"profiles dir", dirOf(effectiveProfilesPath(cfg, deps))},
		{"session workspaces root", deps.WorkspacesRoot},
		{orchestrator.TemplateEnvVar, os.Getenv(orchestrator.TemplateEnvVar)},
		{scheduler.EndAgentTemplateEnvVar, os.Getenv(scheduler.EndAgentTemplateEnvVar)},
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, controlPlaneDir{"~/.torque", filepath.Join(home, ".torque")})
	}
	return out
}

func dirOf(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

// effectiveProfilesPath is the profiles file the daemon reads and watches:
// the profile source's own path (TORQUE_PROFILES_PATH when set), otherwise
// the override itself, otherwise the configured default.
func effectiveProfilesPath(cfg *config.Config, deps *agent.Dependencies) string {
	if src, ok := deps.Profiles.(interface{ Path() string }); ok && src.Path() != "" {
		return src.Path()
	}
	if raw := os.Getenv("TORQUE_PROFILES_PATH"); raw != "" {
		return raw
	}
	return cfg.ProfilesPath
}

// resolveControlPlane turns the candidates into the directories to protect.
// A candidate that is unset is skipped; one that is /, the home directory or
// an ancestor of it is skipped, since protecting it would leave the agent
// nothing to write. A missing one is created 0700, so a fresh host protects
// it before an agent can plant into it. Reaching one through a symlink an
// agent could re-point refuses protection outright: the sandbox protects the
// real path it resolves at launch, and an agent that re-points the link
// redirects Torque to a directory of its own. Each decision is logged.
func resolveControlPlane(candidates []controlPlaneDir) ([]string, string) {
	home, _ := os.UserHomeDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	var kept []string
	for _, c := range candidates {
		if c.path == "" {
			continue
		}
		path, err := filepath.Abs(c.path)
		if err != nil {
			log.Printf("[sandbox] not write-protecting %s %s: %v", c.what, c.path, err)
			continue
		}
		if link := repointableSymlink(path); link != "" {
			return nil, fmt.Sprintf("the %s %s is reached through the symlink %s, which an agent could re-point", c.what, path, link)
		}
		real := path
		if r, err := filepath.EvalSymlinks(path); err == nil {
			real = r
		}
		if real == string(filepath.Separator) || (home != "" && isAncestorOrSelf(real, home)) {
			log.Printf("[sandbox] not write-protecting %s %s: it is / or contains the home directory", c.what, path)
			continue
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			log.Printf("[sandbox] not write-protecting %s %s: create it: %v", c.what, path, err)
			continue
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			log.Printf("[sandbox] not write-protecting %s %s: not a directory", c.what, path)
			continue
		}
		log.Printf("[sandbox] write-protecting %s %s", c.what, path)
		kept = append(kept, path)
	}
	return agent.ResolveProtectedPaths(kept), ""
}

// repointableSymlink returns the first symlink on path that sits in a
// directory the daemon's uid can write, and so an agent running as that uid
// could replace; "" when there is none. A symlink in a directory the agent
// cannot write (macOS's /var, owned by root) is followed and its target
// checked the same way. A component that does not exist yet ends the walk:
// it will be created as a directory.
func repointableSymlink(path string) string {
	cur := string(filepath.Separator)
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		next := filepath.Join(cur, part)
		st, err := os.Lstat(next)
		if err != nil {
			return ""
		}
		if st.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if syscall.Access(cur, accessWrite) == nil {
			return next
		}
		target, err := filepath.EvalSymlinks(next)
		if err != nil {
			return ""
		}
		if link := repointableSymlink(target); link != "" {
			return link
		}
		cur = target
	}
	return ""
}

// accessWrite is access(2)'s W_OK.
const accessWrite = 0x2

// isAncestorOrSelf reports whether dir is path or one of its ancestors.
func isAncestorOrSelf(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// guardProfilesReload makes the profiles watcher reload only while the
// profiles file's directory is still the one protected now, by device and
// inode. go-sandbox already stops an agent from moving it aside and
// recreating it; this keeps a replaced directory from reaching agent launches
// within a second, whatever replaced it.
func guardProfilesReload(deps *agent.Dependencies, cfg *config.Config) {
	src, ok := deps.Profiles.(interface{ SetReloadGuard(func() error) })
	if !ok {
		return
	}
	dir := dirOf(effectiveProfilesPath(cfg, deps))
	if dir == "" {
		return
	}
	id, err := config.RecordDirIdentity(dir)
	if err != nil {
		log.Printf("[sandbox] WARN: cannot record the profiles directory %s to guard its reloads: %v", dir, err)
		return
	}
	src.SetReloadGuard(id.Check)
}

// muxWithoutTorque drops the torque server from the planted mux's
// --servers, so an agent's mux never runs `torque mcp` inside the sandbox:
// there it opens Torque's database read-only, and its startup work fails.
// The session's loopback still serves the task's Torque tools. When the
// servers cannot be narrowed (no --servers under --proxy proxies every
// server, and an emptied list means the same), mux is not planted at all.
func muxWithoutTorque(deps *agent.Dependencies) {
	if deps.MuxCommand == "" {
		return
	}
	deps.MuxOmitsTorque = true
	args, ok := muxArgsWithoutServer(deps.MuxArgs, "torque")
	if !ok {
		log.Printf("[sandbox] WARN: the mux args do not name the servers to proxy without torque, so agents get no mux server while Torque's state is write-protected")
		deps.MuxCommand, deps.MuxArgs, deps.MuxEnv = "", nil, nil
		return
	}
	deps.MuxArgs = args
	log.Printf("[sandbox] agents' mux server proxies no torque tools while Torque's state is write-protected; the session loopback serves the task's Torque tools")
}

// muxArgsWithoutServer returns mux's args with server removed from every
// --servers list. ok is false when that would leave mux proxying every
// server: --proxy with no --servers, or a list that empties.
func muxArgsWithoutServer(args []string, server string) ([]string, bool) {
	out := slices.Clone(args)
	proxy, named := false, false
	narrow := func(list string) (string, bool) {
		var keep []string
		for _, s := range strings.Split(list, ",") {
			if s = strings.TrimSpace(s); s != "" && s != server {
				keep = append(keep, s)
			}
		}
		return strings.Join(keep, ","), len(keep) > 0
	}
	for i := 0; i < len(out); i++ {
		flag, value, inline := strings.Cut(out[i], "=")
		switch strings.TrimLeft(flag, "-") {
		case "proxy":
			proxy = value != "false"
		case "servers":
			named = true
			if !inline {
				if i+1 >= len(out) {
					return nil, false
				}
				i++
				value = out[i]
			}
			list, ok := narrow(value)
			if !ok {
				return nil, false
			}
			if inline {
				out[i] = flag + "=" + list
			} else {
				out[i] = list
			}
		}
	}
	if proxy && !named {
		return nil, false
	}
	return out, true
}

// warnWorkInsideProtected warns loudly when agents' work would land inside a
// protected directory: the per-run worktree root, or a project's repo_path.
// An agent launched there cannot write its work.
func warnWorkInsideProtected(deps *agent.Dependencies, cfg *config.Config) {
	check := func(what, path string) {
		if path == "" {
			return
		}
		real := filepath.Clean(path)
		if r, err := filepath.EvalSymlinks(path); err == nil {
			real = r
		}
		for _, dir := range deps.ProtectedPaths {
			if isAncestorOrSelf(dir, real) {
				log.Printf("[sandbox] WARN: %s %s is inside the write-protected %s: agents launched there cannot write their work; move it, or set %s=0", what, path, dir, agent.ProtectEnv)
				return
			}
		}
	}
	check("TORQUE_WORKTREE_ROOT", cfg.Scheduler.WorktreeRoot)
	if deps.Store == nil {
		return
	}
	projects, err := deps.Store.ListProjects(sqlstore.ProjectFilter{})
	if err != nil {
		log.Printf("[sandbox] could not list projects to check their repo paths against the protected directories: %v", err)
		return
	}
	for _, p := range projects {
		check(fmt.Sprintf("project %s repo_path", p.ID), p.RepoPath)
	}
}
