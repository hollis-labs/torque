package executorcli

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// resolveProfile returns the AgentProfile for the given job, using GetProfileOrDefault.
func resolveProfile(profiles config.ProfileMap, job *executor.ExecutionJob) config.AgentProfile {
	return config.GetProfileOrDefault(profiles, job.AgentProfile)
}
