// Package redact scrubs known secret values out of text before Torque
// persists it (CW-20261001-0123).
//
// It follows Tether's internal/redact: exact occurrences only, values of at
// least four bytes, the longest first, each replaced by Marker. Unlike
// Tether's, the secret set is fixed when a Redactor is built, because
// Torque redacts every line a session writes and a session's launch env does
// not change while it runs.
package redact

import (
	"sort"
	"strings"
)

// Marker replaces each redacted value, matching Tether and go-mcp's
// supervise.Redact.
const Marker = "[redacted]"

// minLen is the shortest value treated as a secret. Shorter values (an env
// flag such as "1" or "on") are never credentials, and replacing them would
// mangle every number or word that contains them.
const minLen = 4

// Redactor replaces a fixed set of secret values. A nil *Redactor redacts
// nothing.
type Redactor struct {
	secrets []string // distinct, at least minLen bytes, longest first
}

// New returns a Redactor for values, or nil when none is at least minLen
// bytes long.
func New(values ...string) *Redactor {
	seen := make(map[string]bool, len(values))
	var secrets []string
	for _, v := range values {
		if len(v) >= minLen && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	if len(secrets) == 0 {
		return nil
	}
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &Redactor{secrets: secrets}
}

// Text returns text with every occurrence of each secret replaced by Marker.
// Longer secrets are replaced first, so one secret that contains another is
// never left half-scrubbed.
func (r *Redactor) Text(text string) string {
	if r == nil || text == "" {
		return text
	}
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, Marker)
	}
	return text
}
