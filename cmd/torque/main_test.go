package main

import (
	"os"
	"testing"

	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// TestMain runs the package's tests with no TORQUE_* configuration
// inherited from the shell (CW-20260520-0040).
func TestMain(m *testing.M) {
	testenv.UnsetTorqueEnv()
	os.Exit(m.Run())
}
