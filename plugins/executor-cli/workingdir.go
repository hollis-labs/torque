package executorcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWorkingDir normalizes a task's raw working_dir string into an absolute
// path suitable for os/exec.Cmd.Dir. The CLI stores working_dir exactly as the
// user wrote it (including shell metacharacters like ~/ and $HOME) because
// capture is frictionless by design and tasks.working_dir should stay
// human-readable. Go's exec.Cmd.Dir does not expand shell metacharacters —
// this helper is the single translation point at the executor boundary.
//
// Policy (matches CW-20260418-0014):
//   - Empty input is preserved (executor falls back to process cwd).
//   - Leading `~/` expands via os.UserHomeDir.
//   - `~otheruser/...` is rejected (intentionally unsupported; Go has no
//     portable user-lookup that respects NSS, and it would invite ambiguity
//     between "user root" and "arbitrary ~prefix paths").
//   - `$VAR` / `${VAR}` expands via os.LookupEnv. Any undefined variable is
//     rejected rather than silently becoming empty (the historical failure
//     mode of os.ExpandEnv, which produced `chdir "/foo" — no such file`
//     errors two layers down the stack).
//   - Relative paths are rejected. Silent resolution against the executor's
//     cwd is an orchestrator footgun — the scheduler may run from anywhere.
//   - Symlinks are NOT resolved (filepath.Clean + filepath.Abs only).
//   - Result is filepath.Abs + filepath.Clean (trailing slash removed).
//
// Errors are returned unwrapped so callers can decide on PermanentError
// semantics (or not). See plugin.go Validate + Run for the canonical
// wrap-as-PermanentError call sites.
func resolveWorkingDir(raw string) (string, error) {
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

// expandTilde replaces a leading `~/` (or a bare `~`) with the executor's
// home directory. Rejects `~user/...` forms — we don't expose an escape
// hatch for other users because the orchestrator runs as a single identity
// and cross-user paths are a footgun (permission, symlink, or missing-home
// surprises the operator didn't consent to).
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
	// `~someuser/...` or `~someuser` — intentionally unsupported.
	return "", fmt.Errorf("working_dir %q: ~user/ form is not supported; use an absolute path or $HOME", raw)
}

// expandEnvStrict mirrors os.ExpandEnv but errors on any referenced variable
// that is not set in the executor's environment. os.ExpandEnv silently
// substitutes "" for undefined vars, which turned out to be the proximate
// cause of CW-20260418-0014-style failures: the task was captured with a
// literal `~/` the parse layer never saw, or a `$PROJ_ROOT` that wasn't
// exported in the scheduler's env.
//
// Variable syntax accepted:
//
//	${NAME}   — delimited form
//	$NAME     — bare form; NAME is [A-Za-z_][A-Za-z0-9_]*
//
// Bare `$` that is not followed by a valid identifier is left untouched so
// paths containing literal `$` (rare on unix, but possible) aren't mangled.
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

		// `$NAME` form — consume as long as the next byte is a valid
		// identifier char. First char must be letter or underscore.
		name, adv := consumeEnvName(raw[i+1:])
		if name == "" {
			// Bare `$` with no valid name — leave literal.
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
// Returns the identifier and the number of bytes consumed. Returns "" / 0
// when s does not start with a valid identifier character.
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
