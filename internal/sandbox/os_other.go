//go:build !(darwin || linux)

package sandbox

import (
	"log"
	"os/exec"
	"sync"
)

var osWarnOnce sync.Once

// applyOSSandbox is a no-op on unsupported platforms.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	osWarnOnce.Do(func() {
		log.Println("[sandbox] warning: OS sandbox not supported on this platform")
	})
	return func() {}, nil
}
