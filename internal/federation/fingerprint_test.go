package federation

import (
	"strings"
	"testing"
)

func TestFingerprintIsStableAndHex(t *testing.T) {
	id := validIdentity(t)
	fp := Fingerprint(id.Leaf)
	if len(fp) != fingerprintHexLen {
		t.Fatalf("fingerprint length = %d, want %d", len(fp), fingerprintHexLen)
	}
	if fp != fingerprintBytes(id.Certificate[0]) {
		t.Fatal("Fingerprint(leaf) != fingerprintBytes(rawDER) — pin computed at handshake would not match")
	}
	if fp != strings.ToLower(fp) {
		t.Fatal("fingerprint is not lower-cased")
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	cases := map[string]string{
		"AB:CD:EF":   "abcdef",
		"ab cd ef":   "abcdef",
		"  ABCDEF  ": "abcdef",
		"aB:cD\neF":  "abcdef",
		"abcdef":     "abcdef",
	}
	for in, want := range cases {
		if got := NormalizeFingerprint(in); got != want {
			t.Errorf("NormalizeFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateFingerprint(t *testing.T) {
	good := strings.Repeat("ab", 32) // 64 hex chars
	if norm, err := ValidateFingerprint(strings.ToUpper(good)); err != nil || norm != good {
		t.Fatalf("ValidateFingerprint(valid) = %q, %v; want %q, nil", norm, err, good)
	}
	// openssl colon form normalizes and validates.
	colon := strings.Join(splitN(good, 2), ":")
	if _, err := ValidateFingerprint(colon); err != nil {
		t.Errorf("ValidateFingerprint(colon form) errored: %v", err)
	}
	for _, bad := range []string{"", "abcd", strings.Repeat("ab", 33), strings.Repeat("zz", 32)} {
		if _, err := ValidateFingerprint(bad); err == nil {
			t.Errorf("ValidateFingerprint(%q) accepted an invalid fingerprint", bad)
		}
	}
}

func splitN(s string, n int) []string {
	var out []string
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
