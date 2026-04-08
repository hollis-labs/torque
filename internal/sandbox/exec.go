package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxOutputBytes      = 1 << 20 // 1 MiB
	defaultAgentTimeout = 30 * time.Second
	maxAgentTimeout     = 5 * time.Minute
	defaultUserTimeout  = 60 * time.Second
	maxUserTimeout      = 10 * time.Minute
	sandboxBaseDirName  = ".clockwork/sandboxes"
)

var (
	essentialBinDirs = []string{"/usr/bin", "/bin", "/usr/local/bin"}
	secretKeyPatterns = []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "CREDENTIAL", "AUTH"}
	minimalEnvKeys    = []string{"HOME", "USER", "LANG", "TERM"}
)

// AgentExecOpts configures an agent-run command execution.
type AgentExecOpts struct {
	SessionID    string
	Command      string
	Args         []string
	Timeout      time.Duration
	Env          map[string]string
	NetworkAllow []string
}

// UserExecOpts configures a user-facing command execution.
type UserExecOpts struct {
	Command   string
	Args      []string
	Dir       string
	Timeout   time.Duration
	Sandboxed bool
}

// ExecResult holds the outcome of a command execution.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

// SandboxDir returns (creating if necessary) the sandbox directory for sessionID.
func SandboxDir(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox dir: get home: %w", err)
	}
	dir := filepath.Join(home, sandboxBaseDirName, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("sandbox dir: mkdir: %w", err)
	}
	return dir, nil
}

// AgentExec runs a command in a sandboxed agent context.
func AgentExec(opts AgentExecOpts) (*ExecResult, error) {
	// Denylist check
	full := opts.Command
	if len(opts.Args) > 0 {
		full = opts.Command + " " + strings.Join(opts.Args, " ")
	}
	if blocked, reason := CheckDenylist(full); blocked {
		return nil, fmt.Errorf("command blocked by denylist: %s", reason)
	}

	// Resolve sandbox dir
	sandboxDir, err := SandboxDir(opts.SessionID)
	if err != nil {
		return nil, err
	}

	timeout := clampTimeout(opts.Timeout, defaultAgentTimeout, maxAgentTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	cmd.Dir = sandboxDir
	cmd.Env = buildAgentEnv(opts.Env)

	// Optional network proxy
	var proxy *Proxy
	if len(opts.NetworkAllow) > 0 {
		proxy = NewProxy(opts.NetworkAllow)
		if err := proxy.Start(); err == nil {
			defer proxy.Stop()
			cmd.Env = append(cmd.Env,
				"http_proxy=http://"+proxy.Addr,
				"https_proxy=http://"+proxy.Addr,
				"HTTP_PROXY=http://"+proxy.Addr,
				"HTTPS_PROXY=http://"+proxy.Addr,
			)
		}
	}

	// OS-level sandbox
	cleanup, err := applyOSSandbox(cmd, sandboxDir, opts.NetworkAllow)
	if err != nil {
		return nil, fmt.Errorf("apply os sandbox: %w", err)
	}
	defer cleanup()

	return runCmd(ctx, cmd, timeout)
}

// UserExec runs a command in the user's context with optional sandboxing.
func UserExec(opts UserExecOpts) (*ExecResult, error) {
	// Denylist check
	full := opts.Command
	if len(opts.Args) > 0 {
		full = opts.Command + " " + strings.Join(opts.Args, " ")
	}
	if blocked, reason := CheckDenylist(full); blocked {
		return nil, fmt.Errorf("command blocked by denylist: %s", reason)
	}

	timeout := clampTimeout(opts.Timeout, defaultUserTimeout, maxUserTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = filterSecrets(os.Environ())

	if opts.Sandboxed {
		cleanup, err := applyOSSandbox(cmd, opts.Dir, nil)
		if err != nil {
			return nil, fmt.Errorf("apply os sandbox: %w", err)
		}
		defer cleanup()
	}

	return runCmd(ctx, cmd, timeout)
}

// runCmd executes cmd, capturing stdout/stderr with size limits.
func runCmd(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (*ExecResult, error) {
	stdoutBuf := &limitedBuffer{max: maxOutputBytes}
	stderrBuf := &limitedBuffer{max: maxOutputBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	err := cmd.Run()
	result := &ExecResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = 124
			result.TimedOut = true
			return result, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}

	return result, nil
}

// buildAgentEnv constructs a minimal environment for agent commands.
func buildAgentEnv(extra map[string]string) []string {
	env := make([]string, 0, len(minimalEnvKeys)+len(essentialBinDirs)+len(extra))

	// Copy minimal keys from current env
	for _, key := range minimalEnvKeys {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}

	// Restricted PATH
	env = append(env, "PATH="+strings.Join(essentialBinDirs, ":"))

	// Extra env vars — filter secrets
	for k, v := range extra {
		if !isSecretKey(k) {
			env = append(env, k+"="+v)
		}
	}

	return env
}

// filterSecrets removes environment entries whose key matches secretKeyPatterns.
func filterSecrets(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) < 1 {
			continue
		}
		if !isSecretKey(parts[0]) {
			out = append(out, entry)
		}
	}
	return out
}

// isSecretKey returns true if name contains any secretKeyPattern (case-insensitive).
func isSecretKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range secretKeyPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// clampTimeout returns t clamped to [defaultVal, maxVal].
// If t == 0, returns defaultVal.
func clampTimeout(t, defaultVal, maxVal time.Duration) time.Duration {
	if t <= 0 {
		return defaultVal
	}
	if t > maxVal {
		return maxVal
	}
	return t
}

// limitedBuffer is a bytes.Buffer that silently discards writes beyond max bytes.
type limitedBuffer struct {
	bytes.Buffer
	max     int
	dropped bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	available := b.max - b.Buffer.Len()
	if available <= 0 {
		b.dropped = true
		return len(p), nil
	}
	if len(p) > available {
		b.dropped = true
		n, err := b.Buffer.Write(p[:available])
		return n + (len(p) - available), err
	}
	return b.Buffer.Write(p)
}

func (b *limitedBuffer) WriteTo(w io.Writer) (int64, error) {
	return b.Buffer.WriteTo(w)
}
