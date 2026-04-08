//go:build linux

package sandbox

import (
	"log"
	"os/exec"
	"sync"
)

var (
	bwrapOnce     sync.Once
	bwrapNotFound bool
)

// applyOSSandbox wraps cmd with bubblewrap (bwrap) for Linux sandboxing.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	bwrapOnce.Do(func() {
		if _, err := exec.LookPath("bwrap"); err != nil {
			bwrapNotFound = true
			log.Println("[sandbox] warning: bwrap not found, OS sandbox disabled")
		}
	})

	if bwrapNotFound {
		return func() {}, nil
	}

	origArgs := cmd.Args

	newArgs := []string{
		"bwrap",
		"--ro-bind", "/", "/",
		"--bind", sandboxDir, sandboxDir,
		"--bind", "/tmp", "/tmp",
		"--dev", "/dev",
		"--proc", "/proc",
	}

	if len(networkAllow) == 0 {
		newArgs = append(newArgs, "--unshare-net")
	}

	newArgs = append(newArgs, "--")
	newArgs = append(newArgs, origArgs...)

	cmd.Path = "bwrap"
	cmd.Args = newArgs

	return func() {}, nil
}
