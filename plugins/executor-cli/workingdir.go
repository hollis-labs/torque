package executorcli

import (
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// resolveWorkingDir is a package-local alias for executor.ResolveWorkingDir.
// Kept as a wrapper so plugin.go call sites remain unchanged.
func resolveWorkingDir(raw string) (string, error) {
	return executor.ResolveWorkingDir(raw)
}
