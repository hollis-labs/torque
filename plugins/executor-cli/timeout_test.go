package executorcli

import (
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

func TestResolveTimeout_MetadataOverrideWinsOverProfile(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	job := &executor.ExecutionJob{
		Metadata: map[string]any{"timeout_seconds_override": 1800},
	}
	assert.Equal(t, 1800*time.Second, resolveTimeout(profile, job))
}

func TestResolveTimeout_MetadataOverrideAtBoundaries(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	cases := []struct {
		name string
		secs int
		want time.Duration
	}{
		{"min", 60, 60 * time.Second},
		{"max", 7200, 7200 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			job := &executor.ExecutionJob{
				Metadata: map[string]any{"timeout_seconds_override": c.secs},
			}
			assert.Equal(t, c.want, resolveTimeout(profile, job))
		})
	}
}

func TestResolveTimeout_MetadataOverrideOutOfRangeFallsBackToProfile(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	cases := []struct {
		name string
		val  any
	}{
		{"below_min", 59},
		{"above_max", 7201},
		{"zero", 0},
		{"negative", -5},
		{"string", "1800"},
		{"non_integer_float", 1800.5},
		{"bool", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			job := &executor.ExecutionJob{
				Metadata: map[string]any{"timeout_seconds_override": c.val},
			}
			assert.Equal(t, 1200*time.Second, resolveTimeout(profile, job))
		})
	}
}

func TestResolveTimeout_MetadataOverrideAsFloat64(t *testing.T) {
	// JSON-unmarshalled integers arrive as float64; integral values must be accepted.
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	job := &executor.ExecutionJob{
		Metadata: map[string]any{"timeout_seconds_override": float64(1800)},
	}
	assert.Equal(t, 1800*time.Second, resolveTimeout(profile, job))
}

func TestResolveTimeout_MissingOverrideUsesProfile(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	job := &executor.ExecutionJob{}
	assert.Equal(t, 1200*time.Second, resolveTimeout(profile, job))
}

func TestResolveTimeout_NilMetadataUsesProfile(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 1200}
	job := &executor.ExecutionJob{Metadata: nil}
	assert.Equal(t, 1200*time.Second, resolveTimeout(profile, job))
}

func TestResolveTimeout_NoProfileNoOverrideFallsBackToLimits(t *testing.T) {
	profile := config.AgentProfile{} // TimeoutSeconds == 0
	max := 30 * time.Second
	job := &executor.ExecutionJob{
		Limits: executor.ExecutionLimits{MaxDuration: &max},
	}
	assert.Equal(t, 30*time.Second, resolveTimeout(profile, job))
}

func TestResolveTimeout_NoProfileNoOverrideNoLimitsUsesDefault(t *testing.T) {
	profile := config.AgentProfile{}
	job := &executor.ExecutionJob{}
	// EffectiveTimeout default = 5 minutes.
	assert.Equal(t, 5*time.Minute, resolveTimeout(profile, job))
}
