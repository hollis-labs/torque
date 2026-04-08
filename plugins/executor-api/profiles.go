package executorapi

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// resolveProfile returns the agent profile for the given job.
func resolveProfile(profiles config.ProfileMap, job *executor.ExecutionJob) config.AgentProfile {
	return config.GetProfileOrDefault(profiles, job.AgentProfile)
}

// buildMessages constructs the message list for a completion request from the job.
func buildMessages(profile config.AgentProfile, job *executor.ExecutionJob) []Message {
	return []Message{
		{Role: "user", Content: job.Description},
	}
}
