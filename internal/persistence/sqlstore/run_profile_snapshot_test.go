package sqlstore

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectRunProfileSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		stored sql.NullString
		want   map[string]string
	}{
		{
			name: "current allowlist shape",
			stored: sql.NullString{Valid: true, String: `{
				"provider":"openai","model":"gpt-current","executor":"cli",
				"runtime_kind":"jsonrpc-stdio","permission_mode":"acceptEdits",
				"launch_profile_id":"worker.current","role":"worker","tier":"standard"
			}`},
			want: map[string]string{"provider": "openai", "model": "gpt-current", "executor": "cli", "runtime_kind": "jsonrpc-stdio", "permission_mode": "acceptEdits", "launch_profile_id": "worker.current", "role": "worker", "tier": "standard"},
		},
		{
			name: "legacy nested shape",
			stored: sql.NullString{Valid: true, String: `{
				"Profile":{"ID":"reviewer.legacy","Role":"reviewer","Tier":"trusted","DisplayName":"sk-DUMMY-display"},
				"AgentProfile":{"Provider":"anthropic","Model":"claude-legacy","Executor":"cli","RuntimeKind":"subprocess-per-turn","PermissionMode":"plan","APIKey":"sk-DUMMY-legacy","SystemPrompt":"sk-DUMMY-prompt","BaseURL":"https://sk-DUMMY.invalid","Args":["sk-DUMMY-arg"]},
				"AgentProfileName":"sk-DUMMY-name","Provenance":"sk-DUMMY-provenance"
			}`},
			want: map[string]string{"provider": "anthropic", "model": "claude-legacy", "executor": "cli", "runtime_kind": "subprocess-per-turn", "permission_mode": "plan", "launch_profile_id": "reviewer.legacy", "role": "reviewer", "tier": "trusted"},
		},
		{name: "malformed", stored: sql.NullString{Valid: true, String: `{"api_key":"sk-DUMMY-malformed"`}},
		{name: "non-object", stored: sql.NullString{Valid: true, String: `["sk-DUMMY-array"]`}},
		{name: "empty", stored: sql.NullString{Valid: true, String: ""}},
		{name: "null", stored: sql.NullString{}},
		{
			name:   "unknown keys dropped",
			stored: sql.NullString{Valid: true, String: `{"provider":"ollama","api_key":"sk-DUMMY-extra","system_prompt":"sk-DUMMY-extra-prompt","unknown":"sk-DUMMY-unknown"}`},
			want:   map[string]string{"provider": "ollama"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectRunProfileSnapshot(tt.stored)
			if strings.Contains(got.String, "sk-DUMMY-") {
				t.Fatal("secret-shaped value present in output")
			}
			if tt.want == nil {
				if got.Valid || got.String != "" {
					t.Fatal("unsafe stored shape did not project to an empty result")
				}
				return
			}
			if !got.Valid {
				t.Fatal("valid stored object did not produce a projected result")
			}
			var decoded map[string]string
			if err := json.Unmarshal([]byte(got.String), &decoded); err != nil {
				t.Fatal("projected result is not valid JSON")
			}
			if len(decoded) != len(tt.want) {
				t.Fatal("projected result contains an unexpected field set")
			}
			for key, want := range tt.want {
				if decoded[key] != want {
					t.Fatal("projected result does not preserve an allowlisted field")
				}
			}
		})
	}
}
