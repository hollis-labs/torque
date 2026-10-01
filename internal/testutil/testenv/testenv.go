// Package testenv keeps a package's tests hermetic against TORQUE_* in the
// environment `go test` runs in (CW-20260520-0040).
//
// Torque reads most of its configuration from TORQUE_* variables, and the
// shells tests run in carry them: an operator's dev shell, a Torque-dispatched
// worker (which inherits TORQUE_TASK_ID, TORQUE_RUN_ID, TORQUE_WORK_ROOT,
// TORQUE_REPO_ROOT and the service's own TORQUE_WORKTREE_PER_RUN and
// TORQUE_WORKTREE_PRECHECK), or CI. A test that asserts a default, or that
// derives its expectations from an unset variable, then fails only there.
package testenv

import (
	"os"
	"strings"
	"testing"
)

// keepPrefix marks variables that tests set on purpose for a re-executed
// helper process (for example TORQUE_TEST_CODEX_HELPER); they are not
// configuration and must survive.
const keepPrefix = "TORQUE_TEST_"

// UnsetTorqueEnv removes every TORQUE_* variable except TORQUE_TEST_* from
// the process environment. Call it from TestMain before m.Run; tests that
// need a variable set it themselves with t.Setenv.
func UnsetTorqueEnv() {
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "TORQUE_") && !strings.HasPrefix(key, keepPrefix) {
			_ = os.Unsetenv(key)
		}
	}
}

// WorkspacesRoot returns a temp dir for a test's agent.Dependencies
// WorkspacesRoot. Left empty, the root defaults to $HOME/.torque/workspaces,
// the operator's real session logs, and agent.WorkspaceCreate refuses that
// in a test (CW-20261001-0175).
func WorkspacesRoot(t testing.TB) string {
	t.Helper()
	return t.TempDir()
}
