package mcpadapter

// Unit tests for the reqInt / reqFloat helpers. These live in the
// mcpadapter package (no _test suffix) so they can exercise the helpers
// directly without going through the full server.HandleMessage path.
// End-to-end coverage — schema validation plus round-trip through the
// handler — lives in adapter_numeric_test.go (external _test package).

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// buildRequest returns the supplied arguments map as-is. Handler helpers now
// take the args map directly rather than a mark3labs CallToolRequest
// wrapper; kept as a named helper so call sites below read the same as
// before.
func buildRequest(args map[string]interface{}) map[string]interface{} {
	return args
}

func TestReqInt(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want int
	}{
		{"float64_50", map[string]interface{}{"k": float64(50)}, 50},
		{"int_50", map[string]interface{}{"k": 50}, 50},
		{"string_50", map[string]interface{}{"k": "50"}, 50},
		{"string_neg_one_sentinel", map[string]interface{}{"k": "-1"}, -1},
		{"string_zero_sentinel", map[string]interface{}{"k": "0"}, 0},
		{"string_empty", map[string]interface{}{"k": ""}, 0},
		// Silent-zero policy: malformed numeric string resolves to 0, the
		// same value a missing key would return. Documented in adapter.go
		// and exercised here so the behavior doesn't regress silently.
		{"string_malformed_silent_zero", map[string]interface{}{"k": "abc"}, 0},
		{"missing_key", map[string]interface{}{}, 0},
		// Bonus sanity: a fractional string should truncate toward zero,
		// matching the existing float64 branch (int(50.9) == 50).
		{"string_fractional_truncates", map[string]interface{}{"k": "50.9"}, 50},
		// Whitespace-only strings are not numeric — silent-zero applies.
		{"string_whitespace", map[string]interface{}{"k": "  "}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reqInt(buildRequest(tc.args), "k")
			require.Equal(t, tc.want, got)
		})
	}
}

func TestReqFloat(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want float64
	}{
		{"float64_50", map[string]interface{}{"k": float64(50)}, 50},
		{"int_50", map[string]interface{}{"k": 50}, 50},
		{"string_50", map[string]interface{}{"k": "50"}, 50},
		{"string_fractional", map[string]interface{}{"k": "12.5"}, 12.5},
		{"string_neg_one_sentinel", map[string]interface{}{"k": "-1"}, -1},
		{"string_zero_sentinel", map[string]interface{}{"k": "0"}, 0},
		{"string_empty", map[string]interface{}{"k": ""}, 0},
		{"string_malformed_silent_zero", map[string]interface{}{"k": "abc"}, 0},
		{"missing_key", map[string]interface{}{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reqFloat(buildRequest(tc.args), "k")
			require.InDelta(t, tc.want, got, 1e-9)
		})
	}
}
