package judge

import "regexp"

// Marker replaces a scrubbed credential.
const scrubMarker = "[redacted]"

// credentialShapes are well-known token formats. Task text sometimes carries a
// pasted token even when nothing is meant to be secret; state is scrubbed of
// these before it leaves the machine for a provider. This is a safety net for
// recognisable shapes, not a guarantee: an arbitrary secret has no shape.
var credentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),                                         // OpenAI, Anthropic
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`),                                    // GitHub tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`),                                  // GitHub fine-grained
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),                                  // Slack
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                                            // AWS access key id
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),                                        // Google API key
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWT
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`),                          // Authorization: Bearer ...
}

// Scrub replaces recognisable credentials in text with a marker.
func Scrub(text string) string {
	for _, re := range credentialShapes {
		text = re.ReplaceAllString(text, scrubMarker)
	}
	return text
}
