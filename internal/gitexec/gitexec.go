// Package gitexec runs git as the Torque daemon in repositories its agents
// can write (CW-20261001-0141).
//
// An agent runs as the operator's uid and can edit the repository it works
// in, its .git/config included, and git runs commands that config names:
// core.fsmonitor on status, hooks on checkout, a filter driver's clean and
// smudge commands, core.sshCommand or a credential helper on fetch. Run
// unguarded by the daemon, they execute outside any sandbox with the
// daemon's authority. Command neutralizes those for every git the daemon
// runs; LocalRemoteExecKey lets a best-effort fetch be skipped instead.
//
// What this does not cover: config inside a submodule's own git dir, which
// `git status` reads when it recurses into the submodule.
package gitexec

import (
	"context"
	"os/exec"
	"slices"
	"strings"
)

// hardening is the config every daemon-run git carries, overriding the
// repository's own: no fsmonitor command, no hooks, no ext:: transport
// (which runs a command), and no recursing into submodules on checkout.
var hardening = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
	"-c", "protocol.ext.allow=never",
	"-c", "submodule.recurse=false",
}

// Command returns `git args...` to run in dir with the repository's
// command-running config neutralized: hardening, and every filter driver the
// repository's own config defines emptied and made optional, so its clean,
// smudge and process commands never run. Filter drivers from the operator's
// global and system config (git-lfs) are left alone.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := slices.Clone(hardening)
	for _, driver := range localFilterDrivers(ctx, dir) {
		full = append(full,
			"-c", "filter."+driver+".clean=",
			"-c", "filter."+driver+".smudge=",
			"-c", "filter."+driver+".process=",
			"-c", "filter."+driver+".required=false",
		)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Dir = dir
	return cmd
}

// LocalRemoteExecKey returns the first key in the repository's own config
// that changes what a fetch or push runs, or where it reaches: a credential
// helper, core.sshCommand or core.gitProxy, a remote's uploadpack or
// receivepack command, a URL rewrite, or a protocol policy. "" when there is
// none. An error reading the config is returned as is; a caller doing
// best-effort remote work should skip it then.
func LocalRemoteExecKey(ctx context.Context, dir string) (string, error) {
	keys, err := localKeys(ctx, dir)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		section, rest, _ := strings.Cut(key, ".")
		name := rest[strings.LastIndex(rest, ".")+1:]
		switch {
		case section == "credential",
			section == "protocol",
			section == "url" && (name == "insteadof" || name == "pushinsteadof"),
			section == "core" && (rest == "sshcommand" || rest == "gitproxy" || rest == "askpass"),
			section == "remote" && (name == "uploadpack" || name == "receivepack" || name == "vcs"):
			return key, nil
		}
	}
	return "", nil
}

// localFilterDrivers are the filter drivers the repository's own config
// (local or worktree scope, includes followed) defines.
func localFilterDrivers(ctx context.Context, dir string) []string {
	keys, _ := localKeys(ctx, dir)
	var drivers []string
	for _, key := range keys {
		rest, ok := strings.CutPrefix(key, "filter.")
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, ".")
		if i <= 0 {
			continue
		}
		if driver := rest[:i]; !slices.Contains(drivers, driver) {
			drivers = append(drivers, driver)
		}
	}
	return drivers
}

// localKeys lists the config keys, lower-cased as git reports them, set in
// the repository's own config: local and worktree scope, with includes
// followed. Reading config runs nothing.
func localKeys(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--list", "--show-scope", "--includes", "--name-only")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, line := range strings.Split(string(out), "\n") {
		scope, key, ok := strings.Cut(line, "\t")
		if ok && (scope == "local" || scope == "worktree") {
			keys = append(keys, key)
		}
	}
	return keys, nil
}
