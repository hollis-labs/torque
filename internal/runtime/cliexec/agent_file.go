package cliexec

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// loadAgentFile resolves and loads the task's agent_file (if any) at dispatch.
// Returns nil, nil when the task has no agent_file set; returns a descriptive
// error when the path can't be resolved or parsed — the executor converts
// this into a blocked run so operators can fix the file without losing the
// task.
func loadAgentFile(job *executor.ExecutionJob) (*agentfile.AgentFile, error) {
	if job.AgentFile == "" {
		return nil, nil
	}
	resolved, err := agentfile.Resolve(job.AgentFile, job.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("agent_file: %w", err)
	}
	return agentfile.Load(resolved)
}
