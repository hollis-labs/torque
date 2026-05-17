package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepoPathError signals that a project's repo_path does not resolve to an
// existing directory on disk. It is a distinct, named error type (rather
// than a generic ValidationError) so the MCP adapter can map it to a clear
// arg_invalid surface with field=repo_path, and callers can errors.As it to
// distinguish "stale metadata" from ordinary field-shape validation.
//
// Motivating bug: CW-20260517-0011 edge 7 — projects carrying pre-rebrand
// ~/Projects-apps/* repo_path values that no longer exist on disk. Stale
// metadata previously sent agents to a missing directory with no signal;
// this error makes the drift loud at create/update time.
type RepoPathError struct {
	// Raw is the repo_path exactly as supplied (pre-expansion), so the
	// message echoes what the caller actually wrote.
	Raw string
	// Resolved is the absolute, ~-expanded path that was checked. Empty
	// when expansion itself failed.
	Resolved string
	// Reason is a short human-readable cause ("directory does not exist",
	// "path is not a directory", "could not resolve HOME", ...).
	Reason string
}

func (e *RepoPathError) Error() string {
	if e.Resolved != "" && e.Resolved != e.Raw {
		return fmt.Sprintf("repo_path %q (resolved to %q) %s — update the project's repo_path to an existing directory, or create the directory", e.Raw, e.Resolved, e.Reason)
	}
	return fmt.Sprintf("repo_path %q %s — update the project's repo_path to an existing directory, or create the directory", e.Raw, e.Reason)
}

// expandUserPath expands a leading ~ / ~/ in a path against the current
// user's home directory. ~otheruser/ forms are rejected (no portable
// cross-user lookup). Mirrors the policy of executor.ResolveWorkingDir but
// is kept local to the service package so the service layer carries no
// dependency on the runtime/executor package.
func expandUserPath(raw string) (string, error) {
	if raw == "" || raw[0] != '~' {
		return raw, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve HOME to expand %q: %w", raw, err)
	}
	if raw == "~" {
		return home, nil
	}
	if strings.HasPrefix(raw, "~/") {
		return filepath.Join(home, raw[2:]), nil
	}
	return "", fmt.Errorf("path %q uses the ~user/ form which is not supported; use an absolute path", raw)
}

// validateRepoPath checks that a project repo_path resolves (after ~
// expansion) to an existing directory. It returns a *RepoPathError on any
// failure so callers get a single, well-typed signal. An empty repo_path is
// the caller's responsibility to reject separately (it is a different,
// "required field" class of problem) — validateRepoPath only runs on
// non-empty input.
func validateRepoPath(raw string) error {
	resolved, err := expandUserPath(raw)
	if err != nil {
		return &RepoPathError{Raw: raw, Reason: err.Error()}
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return &RepoPathError{Raw: raw, Resolved: resolved, Reason: "directory does not exist"}
		}
		return &RepoPathError{Raw: raw, Resolved: resolved, Reason: fmt.Sprintf("could not be accessed (%v)", statErr)}
	}
	if !info.IsDir() {
		return &RepoPathError{Raw: raw, Resolved: resolved, Reason: "path is not a directory"}
	}
	return nil
}

// CheckRepoPath is the exported validation/fixup helper for repo_path drift
// (CW-20260517-0011 edge 7). It does NOT mutate any data — it resolves the
// path and reports whether it currently points at an existing directory.
// Use it for a project-doctor / health pass without forcing a create or
// update. Returns (resolvedAbsolutePath, nil) when the path is healthy, or
// (resolvedAbsolutePath, *RepoPathError) when it is stale/missing.
func CheckRepoPath(raw string) (string, error) {
	resolved, err := expandUserPath(raw)
	if err != nil {
		return "", &RepoPathError{Raw: raw, Reason: err.Error()}
	}
	if err := validateRepoPath(raw); err != nil {
		return resolved, err
	}
	return resolved, nil
}
