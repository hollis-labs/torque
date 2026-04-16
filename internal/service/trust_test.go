package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func TestResolveTrust_Defaults(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		sourceRef  string
		want       string
	}{
		{"system_any", "system", "", "trusted"},
		{"user_any", "user", "chrispian", "normal"},
		{"agent_any", "agent", "claude-code", "normal"},
		{"api_any", "api", "nanite", "normal"},
		{"webhook_any", "webhook", "github", "untrusted"},
		{"import_any", "import", "", "untrusted"},
		{"unknown_falls", "bogus", "", "normal"},
		{"empty_falls", "", "", "normal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := service.ResolveTrust(tc.sourceType, tc.sourceRef)
			if got != tc.want {
				t.Errorf("ResolveTrust(%q, %q) = %q, want %q",
					tc.sourceType, tc.sourceRef, got, tc.want)
			}
		})
	}
}
