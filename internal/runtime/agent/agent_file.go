package agent

import (
	"fmt"

	"github.com/hollis-labs/torque/internal/agentfile"
)

// loadAgentFile resolves and loads the task's agent_file (if any) at boot.
// Returns (nil, nil) when Options.AgentFile is empty; returns a descriptive
// error when the path can't be resolved or parsed — Boot converts this into
// ErrBootFailed so the caller can surface a precise reason.
//
// Forked from internal/runtime/cliexec/agent_file.go.
func loadAgentFile(opts Options) (*agentfile.AgentFile, error) {
	if opts.AgentFile == "" {
		return nil, nil
	}
	resolved, err := agentfile.Resolve(opts.AgentFile, opts.Workdir)
	if err != nil {
		return nil, fmt.Errorf("agent_file: %w", err)
	}
	return agentfile.Load(resolved)
}
