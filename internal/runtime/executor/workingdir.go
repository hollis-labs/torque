package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWorkingDir normalizes a task's raw working_dir string into an
// absolute path suitable for os/exec.Cmd.Dir. The scheduler stores
// working_dir exactly as the user wrote it (including shell metacharacters
// like ~/ and $HOME) because capture is frictionless by design.
// Go's exec.Cmd.Dir does not expand shell metacharacters — this helper is
// the single translation point at the executor boundary.
//
// Policy:
//   - Empty input is preserved (executor falls back to process cwd).
//   - Leading `~/` expands via os.UserHomeDir.
//   - `~otheruser/...` is rejected (no portable user-lookup, footgun).
//   - `$VAR` / `${VAR}` expands via os.LookupEnv; undefined variables are
//     rejected rather than silently becoming empty.
//   - Relative paths are rejected — silent cwd-relative resolution is a
//     footgun when the scheduler may run from anywhere.
//   - Result is filepath.Abs + filepath.Clean (trailing slash removed).
//
// Errors are returned unwrapped so callers can apply whatever
// PermanentError semantics are appropriate at their executor boundary.
// RequireExistingDir stat()s an already-resolved working_dir path and
// returns an error if it is missing or not a directory. Callers (executor
// Validate hooks) wrap the result in PermanentError; this helper stays
// classification-neutral so it can be reused outside the dispatch path.
//
// Empty input is a no-op (mirrors ResolveWorkingDir's "empty preserved"
// shape) — executors that require a non-empty working_dir must check that
// separately. Distinguishes "does not exist" from "exists but is a regular
// file" so the blocked_reason an operator sees points at the actual cause.
func RequireExistingDir(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("working_dir %q: not a directory", path)
	}
	return nil
}

func ResolveWorkingDir(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	expanded, err := expandTilde(raw)
	if err != nil {
		return "", err
	}

	expanded, err = expandEnvStrict(expanded)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("working_dir %q is relative; only absolute paths (including ~/ and $HOME-prefixed) are accepted", raw)
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("working_dir %q: %w", raw, err)
	}
	return filepath.Clean(abs), nil
}

// expandTilde replaces a leading `~/` (or bare `~`) with the executor's home
// directory. Rejects `~user/...` forms — cross-user paths are unsupported.
func expandTilde(raw string) (string, error) {
	if raw == "" || raw[0] != '~' {
		return raw, nil
	}
	if raw == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve HOME for working_dir %q: %w", raw, err)
		}
		return home, nil
	}
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve HOME for working_dir %q: %w", raw, err)
		}
		return filepath.Join(home, raw[2:]), nil
	}
	return "", fmt.Errorf("working_dir %q: ~user/ form is not supported; use an absolute path or $HOME", raw)
}

// expandEnvStrict mirrors os.ExpandEnv but returns an error for any
// referenced variable that is not set, rather than silently substituting "".
//
// Variable syntax accepted:
//
//	${NAME}   — delimited form
//	$NAME     — bare form; NAME is [A-Za-z_][A-Za-z0-9_]*
//
// Bare `$` not followed by a valid identifier is left untouched.
func expandEnvStrict(raw string) (string, error) {
	if !strings.ContainsRune(raw, '$') {
		return raw, nil
	}

	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}

		// `${NAME}` form
		if i+1 < len(raw) && raw[i+1] == '{' {
			end := strings.IndexByte(raw[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("working_dir %q: unterminated ${ in env reference", raw)
			}
			name := raw[i+2 : i+2+end]
			if name == "" {
				return "", fmt.Errorf("working_dir %q: empty ${} env reference", raw)
			}
			val, ok := os.LookupEnv(name)
			if !ok {
				return "", fmt.Errorf("working_dir %q: env var %q is not set", raw, name)
			}
			b.WriteString(val)
			i += 2 + end + 1
			continue
		}

		// `$NAME` form
		name, adv := consumeEnvName(raw[i+1:])
		if name == "" {
			b.WriteByte(c)
			i++
			continue
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("working_dir %q: env var %q is not set", raw, name)
		}
		b.WriteString(val)
		i += 1 + adv
	}
	return b.String(), nil
}

// consumeEnvName reads a bare shell-style identifier from the start of s.
func consumeEnvName(s string) (string, int) {
	if len(s) == 0 {
		return "", 0
	}
	if !isEnvNameStart(s[0]) {
		return "", 0
	}
	i := 1
	for i < len(s) && isEnvNameContinue(s[i]) {
		i++
	}
	return s[:i], i
}

func isEnvNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isEnvNameContinue(c byte) bool {
	return isEnvNameStart(c) || (c >= '0' && c <= '9')
}
