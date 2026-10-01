package planstart_test

import (
	"os"
	"testing"

	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// TestMain puts refusing agent CLI shims first on PATH, so no test here can
// run a real claude/codex/opencode (CW-20261001-0041).
func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithAgentShims(m))
}
