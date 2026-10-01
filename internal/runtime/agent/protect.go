package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	out := dirs[:0]
	for _, d := range dirs {
		if len(out) > 0 && within(d, out[len(out)-1]) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
