// Command clockwork-apikey-helper resolves an Anthropic API bearer token
// for the bare-mode claude CLI's apiKeyHelper hook.
//
// Resolution order:
//
//  1. ANTHROPIC_API_KEY in env — printed verbatim, exit 0.
//  2. ANTHROPIC_AUTH_TOKEN in env — printed verbatim, exit 0.
//  3. macOS keychain (security find-generic-password -s "Claude Code-credentials"):
//     parses the keychain payload's claudeAiOauth.accessToken (the
//     `sk-ant-oat01-...` OAuth access token) and prints it.
//  4. (No fallbacks beyond macOS today.) Other-OS branches print a clear
//     error to stderr and exit 1; consumers on those platforms must set
//     ANTHROPIC_API_KEY explicitly until a portable secret-store path
//     lands.
//
// Stdout discipline: exactly one line containing the token, no trailing
// whitespace beyond the implicit newline. Bare-mode claude consumes the
// first line of stdout per its docs.
//
// Exit codes:
//   - 0: token resolved, written to stdout.
//   - 1: no token available; reason logged to stderr.
//
// Token format empirically verified (CW-20260509-0016): the keychain's
// `claudeAiOauth.accessToken` (`sk-ant-oat01-...`) authenticates against
// the Anthropic API directly via apiKeyHelper — no exchange to
// `sk-ant-api03-` needed. Subscription users (Pro/Max/Team) authenticated
// via `claude` interactive login → keychain are covered automatically.
//
// Token refresh is OUT OF SCOPE for V1: if `claudeAiOauth.expiresAt` is
// past, this helper returns the (now-expired) token and lets the API
// surface the 401. The caller must run `claude` interactively to refresh
// the keychain entry. The `claude setup-token` flow generates a long-
// lived API key (`sk-ant-api03-...`) which can be put in
// $ANTHROPIC_API_KEY for unattended daemons — V1's recommended setup for
// long-running scenarios.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func main() {
	if tok := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); tok != "" {
		fmt.Println(tok)
		return
	}
	if tok := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); tok != "" {
		fmt.Println(tok)
		return
	}

	tok, err := resolveFromKeychain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clockwork-apikey-helper: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}

// resolveFromKeychain extracts the OAuth access token from the macOS
// keychain entry written by `claude` interactive login. Returns an error
// (not exit) so main() can format the stderr message uniformly.
func resolveFromKeychain() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf(
			"keychain fallback only available on macOS (GOOS=%s); "+
				"set ANTHROPIC_API_KEY in env or run `claude setup-token` to generate one",
			runtime.GOOS)
	}

	user := os.Getenv("USER")
	if user == "" {
		// LOGNAME is the POSIX-portable spelling; some daemon contexts (e.g.
		// launchd plists that don't propagate USER) still set LOGNAME.
		user = os.Getenv("LOGNAME")
	}
	if user == "" {
		return "", fmt.Errorf(
			"USER and LOGNAME both empty; cannot key the keychain lookup. " +
				"Set USER in the daemon env or set ANTHROPIC_API_KEY directly")
	}

	cmd := exec.Command("security",
		"find-generic-password",
		"-s", "Claude Code-credentials",
		"-a", user,
		"-w",
	)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	out, err := cmd.Output()
	if err != nil {
		// Provide a usable error: capture security's stderr (the meaningful
		// part — `cmd.Output` would discard it) and special-case the
		// "binary not found" case so a misconfigured PATH gets a clearer
		// hint than "exit status N".
		stderrText := strings.TrimSpace(stderrBuf.String())
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf(
				"`security` binary not on PATH: %w; this helper relies on "+
					"macOS keychain extraction. Set ANTHROPIC_API_KEY in env, "+
					"or ensure /usr/bin is on the daemon's PATH",
				err)
		}
		// security exits non-zero when the entry doesn't exist — surface that
		// distinctly so operators know they need to run `claude` once
		// interactively to seed the keychain (or set ANTHROPIC_API_KEY).
		if stderrText != "" {
			return "", fmt.Errorf(
				"security find-generic-password (Claude Code-credentials, account=%s): %w (stderr: %s); "+
					"set ANTHROPIC_API_KEY in env, run `claude setup-token`, or "+
					"sign in via `claude` interactively first",
				user, err, stderrText)
		}
		return "", fmt.Errorf(
			"security find-generic-password (Claude Code-credentials, account=%s): %w; "+
				"set ANTHROPIC_API_KEY in env, run `claude setup-token`, or "+
				"sign in via `claude` interactively first",
			user, err)
	}

	// The keychain payload is a JSON document with the shape:
	//   {"claudeAiOauth": {"accessToken": "sk-ant-oat01-...", ...},
	//    "mcpOAuth": {...}}
	// The exact extra fields (refreshToken, expiresAt, scopes,
	// subscriptionType, rateLimitTier, etc.) are stable across claude 2.1.x;
	// the helper only reads accessToken, so additions are forward-compatible.
	var payload struct {
		ClaudeAIOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", fmt.Errorf("keychain entry is empty (account=%s)", user)
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf(
			"parse keychain payload (account=%s): %w; "+
				"the entry may be from an older claude version with a different shape",
			user, err)
	}
	tok := strings.TrimSpace(payload.ClaudeAIOauth.AccessToken)
	if tok == "" {
		return "", fmt.Errorf(
			"keychain entry has no claudeAiOauth.accessToken (account=%s); "+
				"the keychain may be in a broken state — sign in again via `claude`",
			user)
	}
	return tok, nil
}
