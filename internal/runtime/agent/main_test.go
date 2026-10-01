package agent

import (
	"os"
	"testing"

	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// TestMain runs the package's tests with no TORQUE_* configuration
// inherited from the shell (CW-20260520-0040), and with refusing agent CLI
// shims first on PATH so no test can run a real claude/codex/opencode
// (CW-20261001-0041).
func TestMain(m *testing.M) {
	testenv.UnsetTorqueEnv()
	os.Exit(testenv.RunWithAgentShims(m))
}
