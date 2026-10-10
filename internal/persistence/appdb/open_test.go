package appdb

import (
	"strings"
	"testing"
)

func TestPostgresStartupPolicy(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		override string
		wantWarn bool
		wantErr  bool
	}{
		{name: "sqlite default"},
		{name: "whitespace is unset", dsn: "  "},
		{name: "postgres refused", dsn: "postgres://example.invalid/torque", wantErr: true},
		{name: "explicit unsupported override", dsn: "postgres://example.invalid/torque", override: "1", wantWarn: true},
		{name: "other override values are refused", dsn: "postgres://example.invalid/torque", override: "true", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warn, err := postgresStartupPolicy(tt.dsn, tt.override)
			if (err != nil) != tt.wantErr {
				t.Fatal("unexpected Postgres startup policy result")
			}
			if warn != tt.wantWarn {
				t.Fatal("unexpected Postgres startup warning decision")
			}
			if tt.wantErr && (!strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), allowUnsupportedPostgresEnv)) {
				t.Fatal("Postgres refusal does not explain the support boundary and override")
			}
		})
	}
}
