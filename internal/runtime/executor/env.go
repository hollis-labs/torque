package executor

import (
	"strings"
)

// secretPatterns are substrings in env var names that indicate secrets.
// Any env var whose key (uppercased) contains one of these is stripped.
var secretPatterns = []string{
	"SECRET",
	"API_KEY",
	"APIKEY",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"PRIVATE_KEY",
	"SIGNING_KEY",
	"ENCRYPTION_KEY",
	"AUTH_KEY",
}

// safeTokenVars are env var names that contain "TOKEN" but are not secrets
// (false positives — terminal/shell metadata that happens to contain the
// substring).
var safeTokenVars = map[string]bool{
	"COLORTERM":     true,
	"TERM_PROGRAM":  true,
	"ITERM_SESSION": true,
}

// providerAuthEnvVars are env var names that ARE secrets but MUST pass through
// to the spawned subprocess, because the subprocess IS the LLM provider CLI
// (claude / codex / opencode / gemini / copilot) and the variable is the
// provider's documented authentication mechanism.
//
// CW-20260509-0011: bare-mode claude (--bare) explicitly documents:
//
//	"Anthropic auth is strictly ANTHROPIC_API_KEY or apiKeyHelper via
//	 --settings (OAuth and keychain are never read)."
//
// Without ANTHROPIC_API_KEY surviving FilterEnv, bare-mode claude in the
// daemon-spawned subprocess fails with "Not logged in · Please run /login |
// exit 1" — surfaced 2026-05-09 in S2.5 smoke retry, blocked the
// re-trigger. The passthrough also covers the analogous variables for the
// other providers in the portfolio so this same gap doesn't bite when
// other adapters move to bare-equivalent modes.
//
// Subscription/OAuth users (no ANTHROPIC_API_KEY in env, authenticated via
// claude.ai login → macOS keychain) should run `claude setup-token` once to
// generate a long-lived API token and place it in the daemon's environment
// (e.g. cerberus launchd plist). Bare mode then honors it via this
// passthrough. Auto-extracting the OAuth credential at run time is tracked
// as a follow-up (apiKeyHelper-automation) and out of scope here.
var providerAuthEnvVars = map[string]bool{
	"ANTHROPIC_API_KEY":    true, // claude (--bare requires this or apiKeyHelper)
	"ANTHROPIC_AUTH_TOKEN": true, // claude (alternative auth surface)
	"OPENAI_API_KEY":       true, // codex
	"GEMINI_API_KEY":       true, // gemini
	"GOOGLE_API_KEY":       true, // gemini (alternative)
	"GITHUB_TOKEN":         true, // copilot
	"GH_TOKEN":             true, // copilot (alternative)
}

// FilterEnvOpts controls how environment variables are filtered.
type FilterEnvOpts struct {
	// StripPrefixes removes any env var whose key starts with one of these.
	StripPrefixes []string
	// ExtraVars are appended after filtering.
	ExtraVars []string
	// AllowList, if non-empty, switches to allow-list mode: only vars whose
	// key exactly matches an entry are kept (before extras and GUI prevention).
	AllowList []string
}

// FilterEnv filters environment variables for subprocess execution.
// It strips secrets, applies prefix stripping, adds GUI prevention vars,
// and appends extra vars. Returns the filtered env and the list of stripped keys.
func FilterEnv(env []string, opts FilterEnvOpts) (filtered []string, stripped []string) {
	allowSet := make(map[string]bool, len(opts.AllowList))
	for _, k := range opts.AllowList {
		allowSet[k] = true
	}
	useAllowList := len(opts.AllowList) > 0

	for _, entry := range env {
		key := envKey(entry)

		// Allow-list mode: only pass explicitly listed vars
		if useAllowList && !allowSet[key] {
			stripped = append(stripped, key)
			continue
		}

		// Strip by prefix
		if stripByPrefix(key, opts.StripPrefixes) {
			stripped = append(stripped, key)
			continue
		}

		// Strip secrets
		if LooksLikeSecret(key) {
			stripped = append(stripped, key)
			continue
		}

		filtered = append(filtered, entry)
	}

	// Add GUI prevention variables to prevent browser/window popups
	guiPrevention := []string{
		"BROWSER=",
		"DISPLAY=",
		"NO_GUI=1",
		"HEADLESS=1",
	}
	filtered = append(filtered, guiPrevention...)

	// Add extra vars last
	filtered = append(filtered, opts.ExtraVars...)

	return filtered, stripped
}

// LooksLikeSecret returns true if the env var key looks like it contains a
// secret AND should be stripped from the spawned subprocess's environment.
//
// Two escape hatches override the secret-pattern match:
//
//  1. safeTokenVars — names that contain a secret-pattern substring as a
//     false positive (e.g. COLORTERM, TERM_PROGRAM contain "TOKEN" but
//     aren't secrets).
//
//  2. providerAuthEnvVars — names that ARE secrets but the spawned
//     subprocess REQUIRES (because it IS the LLM provider CLI and the
//     variable is its documented auth mechanism). Without this allowlist,
//     bare-mode claude can't authenticate (CW-20260509-0011). The
//     allowlist is portfolio-wide; misuse risk (leaking ANTHROPIC_API_KEY
//     to a non-claude subprocess) is mitigated because the value is only
//     ever set in the daemon's environment by the operator, who controls
//     where the daemon spawns subprocesses.
func LooksLikeSecret(key string) bool {
	upper := strings.ToUpper(key)

	// Check safe list first (vars that match patterns but aren't secrets)
	if safeTokenVars[upper] {
		return false
	}
	// Provider auth vars are secrets BUT must pass through to the spawned
	// LLM provider CLI. See providerAuthEnvVars godoc for rationale.
	if providerAuthEnvVars[upper] {
		return false
	}

	for _, pattern := range secretPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// envKey extracts the key portion of a KEY=VALUE environment variable string.
func envKey(entry string) string {
	if idx := strings.IndexByte(entry, '='); idx >= 0 {
		return entry[:idx]
	}
	return entry
}

// stripByPrefix returns true if the key starts with any of the given prefixes.
func stripByPrefix(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
