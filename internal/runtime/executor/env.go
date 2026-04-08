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

// safeTokenVars are env var names that contain "TOKEN" but are not secrets.
var safeTokenVars = map[string]bool{
	"COLORTERM":     true,
	"TERM_PROGRAM":  true,
	"ITERM_SESSION": true,
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

// LooksLikeSecret returns true if the env var key looks like it contains a secret.
func LooksLikeSecret(key string) bool {
	upper := strings.ToUpper(key)

	// Check safe list first (vars that match patterns but aren't secrets)
	if safeTokenVars[upper] {
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
