package agent

import (
	"fmt"
	"os"
	"sort"

	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// composeEnv assembles the environment slice handed to the spawned subprocess.
// Order:
//  1. Filtered OS environment (secrets stripped per profile.EnvStripPrefixes)
//  2. TORQUE_TASK_ID + TORQUE_RUN_ID (always present, even when zero)
//  3. agent_file.environment (skipped for keys the caller overrides; secrets
//     filtered via executor.ShouldStripEnvVar — provider-auth allowlist applied)
//  4. opts.Env (secrets filtered, same allowlist semantics)
//
// Forked from internal/runtime/cliexec/env.go without semantic change. The
// addition over cliexec is the per-provider amendment hook (e.g.
// OPENCODE_CONFIG_DIR=<bootDir>) which the bootdir layout returns separately
// and the caller appends after this function.
func composeEnv(profile config.AgentProfile, opts Options, agent *agentfile.AgentFile) []string {
	extras := []string{
		"TORQUE_TASK_ID=" + opts.TaskID,
		fmt.Sprintf("TORQUE_RUN_ID=%d", opts.RunID),
	}

	if agent != nil {
		// Sort agent-file env keys for deterministic spawn-arg ordering;
		// avoids spurious diffs in tests that compare the env slice.
		keys := make([]string, 0, len(agent.Environment))
		for k := range agent.Environment {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, overridden := opts.Env[k]; overridden {
				continue
			}
			if !executor.ShouldStripEnvVar(k) {
				extras = append(extras, k+"="+agent.Environment[k])
			}
		}
	}

	// opts.Env keys override agent-file values (precedence above).
	keys := make([]string, 0, len(opts.Env))
	for k := range opts.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !executor.ShouldStripEnvVar(k) {
			extras = append(extras, k+"="+opts.Env[k])
		}
	}

	filtered, _ := executor.FilterEnv(os.Environ(), executor.FilterEnvOpts{
		StripPrefixes: profile.EnvStripPrefixes,
		ExtraVars:     extras,
	})
	return filtered
}
