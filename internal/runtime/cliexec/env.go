package cliexec

import (
	"fmt"
	"os"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// buildEnv assembles the environment slice handed to the spawned subprocess.
// Order:
//  1. Filtered OS environment (secrets stripped per profile.EnvStripPrefixes)
//  2. CLOCKWORK_TASK_ID + CLOCKWORK_RUN_ID (always present)
//  3. agent_file.environment (skipped for keys the job overrides; secrets
//     filtered via LooksLikeSecret)
//  4. job.Environment (secrets filtered)
//
// Mirrors the precedence rules from the legacy executor-cli plugin.
func buildEnv(profile config.AgentProfile, job *executor.ExecutionJob, agent *agentfile.AgentFile) []string {
	opts := executor.FilterEnvOpts{
		StripPrefixes: profile.EnvStripPrefixes,
		ExtraVars: []string{
			"CLOCKWORK_TASK_ID=" + job.TaskID,
			fmt.Sprintf("CLOCKWORK_RUN_ID=%d", job.RunID),
		},
	}

	if agent != nil {
		for k, v := range agent.Environment {
			if _, overridden := job.Environment[k]; overridden {
				continue
			}
			if !executor.LooksLikeSecret(k) {
				opts.ExtraVars = append(opts.ExtraVars, k+"="+v)
			}
		}
	}

	for k, v := range job.Environment {
		if !executor.LooksLikeSecret(k) {
			opts.ExtraVars = append(opts.ExtraVars, k+"="+v)
		}
	}

	filtered, _ := executor.FilterEnv(os.Environ(), opts)
	return filtered
}
