package judge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScrubRemovesRecognisableCredentials(t *testing.T) {
	// Fake tokens built at run time so no real-looking secret sits in the repo.
	fake := map[string]string{
		"openai":    "sk-" + strings.Repeat("a1B2", 8),
		"github":    "ghp_" + strings.Repeat("Z9y8", 9),
		"slack":     "xoxb-" + "1234567890-abcdefghij",
		"aws":       "AKIA" + "ABCDEFGHIJKLMNOP",
		"google":    "AIza" + strings.Repeat("Qw3_", 9),
		"jwt":       "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "abcDEF123456_-xyz",
		"bearer":    "Bearer " + strings.Repeat("tok", 10),
		"githubpat": "github_pat_" + strings.Repeat("A1b2C3", 8),
	}
	for name, secret := range fake {
		got := Scrub("before " + secret + " after")
		assert.Equal(t, "before [redacted] after", got, name)
		assert.NotContains(t, got, secret, name)
	}
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\ndef\n-----END RSA PRIVATE KEY-----"
	assert.Equal(t, "key: [redacted] done", Scrub("key: "+pem+" done"))
}

func TestScrubLeavesOrdinaryTextAlone(t *testing.T) {
	for _, text := range []string{
		"Fix the crash in sk-learn style naming",
		"Task CW-20261011-0050 depends on PR #249 and the AKIA prefix is mentioned only",
		"Use a bearer token for the API", // the word, no token
		"eyJ is how a JWT starts",
		"",
	} {
		assert.Equal(t, text, Scrub(text))
	}
}

func TestBuildRequestScrubsState(t *testing.T) {
	sets, _ := LoadSets()
	secret := "ghp_" + strings.Repeat("Q1w2", 9)
	req := sets["task-kind"].BuildRequest(map[string]string{"title": "t", "description": "token " + secret + " leaked"}, 1)
	state := req.State.(map[string]string)
	assert.Equal(t, "token [redacted] leaked", state["description"])
}

func TestScrubHappensBeforeTheLengthLimitSoACutTokenCannotLeak(t *testing.T) {
	sets, _ := LoadSets()
	secret := "sk-" + strings.Repeat("a1B2", 8)
	// Put the token so the 4000-character limit would cut it in half.
	text := strings.Repeat("x", 3989) + " " + secret
	req := sets["task-kind"].BuildRequest(map[string]string{"title": "t", "description": text}, 1)
	got := req.State.(map[string]string)["description"]
	assert.NotContains(t, got, "sk-", "no part of the token survives, not even a prefix")
	assert.Contains(t, got, scrubMarker)
}
