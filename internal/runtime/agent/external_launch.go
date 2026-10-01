package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

const SessionAllowedRootsEnv = "TORQUE_SESSION_ALLOWED_ROOTS"

var ErrLaunchPathRefused = errors.New("agent: external launch path refused")

type externalLaunchKey struct{}

// BootExternal is the untrusted HTTP/MCP launch entry. Scheduler boots use
// Boot directly; a caller cannot turn this check off through request/env/meta.
func (m *Manager) BootExternal(ctx context.Context, opts Options) (*Session, error) {
	return m.Boot(context.WithValue(ctx, externalLaunchKey{}, true), opts)
}

// ResumeExternal keeps the same boundary when a resume changes its workdir,
// including the fresh fallback if the provider lost the conversation.
func (m *Manager) ResumeExternal(ctx context.Context, req ResumeRequest) (string, error) {
	return m.Resume(context.WithValue(ctx, externalLaunchKey{}, true), req)
}

func launchPathRefusal(rule, path string) error {
	return fmt.Errorf("%w: %s: %q; allowed bases are registered project repo paths plus operator-set %s; caller identity is required for per-caller authorization", ErrLaunchPathRefused, rule, path, SessionAllowedRootsEnv)
}

// constrainExternalLaunch is shared by every externally initiated boot and
// resume before boot directories, loopbacks, sessions or runtimes are created.
// The allowlist is server-side; the denylist wins even beneath an allowed base.
func (m *Manager) constrainExternalLaunch(opts Options) (Options, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return opts, launchPathRefusal("operator home unavailable", home)
	}
	home, err = canonicalPath(home)
	if err != nil {
		return opts, launchPathRefusal("cannot resolve operator home", home)
	}
	denied := append([]string(nil), m.deps.ProtectedPaths...)
	for _, dir := range []string{".tether", "tether", ".torque", ".config", ".local/share", ".local/state", ".claude", ".codex", ".gemini"} {
		denied = append(denied, filepath.Join(home, dir))
	}
	for _, pattern := range []string{".claude*", ".codex*", ".gemini*"} {
		matches, err := filepath.Glob(filepath.Join(home, pattern))
		if err != nil {
			return opts, launchPathRefusal("cannot enumerate control-plane paths", pattern)
		}
		denied = append(denied, matches...)
	}
	for i, dir := range denied {
		denied[i], err = canonicalPath(dir)
		if err != nil {
			return opts, launchPathRefusal("cannot resolve denied directory", dir)
		}
	}
	roots := append([]string(nil), m.sessionAllowedRoots...)
	if m.deps.Store != nil {
		projects, err := m.deps.Store.ListProjects(sqlstore.ProjectFilter{})
		if err != nil {
			return opts, launchPathRefusal("cannot read registered project roots", "")
		}
		for _, project := range projects {
			if project.RepoPath != "" {
				roots = append(roots, project.RepoPath)
			}
		}
	}
	var allowed []string
	for _, root := range roots {
		if resolved, err := existingLaunchDir(root); err == nil {
			allowed = append(allowed, resolved)
		}
	}
	if len(allowed) == 0 {
		return opts, launchPathRefusal("no usable allowed bases configured", "")
	}
	validate := func(path string) (string, error) {
		resolved, err := existingLaunchDir(path)
		if err != nil {
			return "", launchPathRefusal("launch path must be an existing absolute directory", path)
		}
		if pathWithin(home, resolved) { // home itself and every ancestor
			return "", launchPathRefusal("operator home or its ancestor is forbidden", resolved)
		}
		for _, dir := range denied {
			if pathWithin(resolved, dir) || pathWithin(dir, resolved) {
				return "", launchPathRefusal("control-plane directory or its ancestor is forbidden", resolved)
			}
		}
		for _, root := range allowed {
			if pathWithin(resolved, root) {
				return resolved, nil
			}
		}
		return "", launchPathRefusal("path is outside allowed bases", resolved)
	}
	opts.Workdir, err = validate(opts.Workdir)
	if err != nil {
		return opts, err
	}
	opts.RepoRoot, err = validate(resolveRepoRoot(opts))
	return opts, err
}

func pathWithin(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func existingLaunchDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("directory required")
	}
	return filepath.Clean(resolved), nil
}

// canonicalPath resolves existing symlink ancestors even when a protected
// directory has not yet been created. An uninspectable ancestor fails closed.
func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) || filepath.Dir(path) == path {
		return "", err
	}
	parent, err := canonicalPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
