//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// applyOSSandbox wraps cmd with macOS sandbox-exec using a seatbelt profile.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	profile := buildSeatbeltProfile(sandboxDir, networkAllow)

	// Write profile to temp file
	f, err := os.CreateTemp("", "clockwork-sandbox-*.sb")
	if err != nil {
		return func() {}, fmt.Errorf("sandbox profile tempfile: %w", err)
	}
	profilePath := f.Name()

	if _, err := f.WriteString(profile); err != nil {
		f.Close()
		os.Remove(profilePath)
		return func() {}, fmt.Errorf("sandbox profile write: %w", err)
	}
	f.Close()

	cleanup = func() {
		os.Remove(profilePath)
	}

	// Wrap the command: sandbox-exec -f <profile> <original cmd>
	origPath := cmd.Path
	origArgs := cmd.Args // Args[0] is typically the path

	newArgs := make([]string, 0, 3+len(origArgs))
	newArgs = append(newArgs, "/usr/bin/sandbox-exec", "-f", profilePath)
	newArgs = append(newArgs, origArgs...)

	cmd.Path = "/usr/bin/sandbox-exec"
	cmd.Args = newArgs
	_ = origPath // suppress unused warning

	return cleanup, nil
}

func buildSeatbeltProfile(sandboxDir string, networkAllow []string) string {
	var sb strings.Builder

	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")

	// Deny file-write outside sandboxDir, /tmp, /dev/null
	sb.WriteString(fmt.Sprintf("(deny file-write*\n  (require-not\n    (require-any\n      (subpath %q)\n      (subpath \"/tmp\")\n      (literal \"/dev/null\")\n    )\n  )\n)\n", sandboxDir))

	// Network: deny unless proxy domains specified
	if len(networkAllow) == 0 {
		sb.WriteString("(deny network*)\n")
	} else {
		// Allow loopback for the proxy
		sb.WriteString("(allow network* (remote ip \"localhost:*\"))\n")
		sb.WriteString("(allow network* (remote ip \"127.0.0.1:*\"))\n")
		sb.WriteString("(deny network*)\n")
	}

	return sb.String()
}
