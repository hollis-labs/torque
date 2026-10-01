// Package runtimetoken normalizes the runtime-kind tokens Torque reads from
// profiles.yaml (`runtime_kind`) and from stored session rows onto the
// agent-contracts-leaf runtimes.Mode vocabulary the libraries take since
// agentkit v0.12.0 (D-73), which fails an old token with ErrUnknownRuntime.
//
// It imports only the leaf vocabulary so the profile loader, the agent
// package and the session store can share one table (CW-20261001-0064;
// CW-20261001-0063 normalizes profile and session-row reads through it
// rather than rewriting stored values).
package runtimetoken

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// ErrUnknown is returned for a token that is neither a runtimes.Mode nor a
// known older spelling.
var ErrUnknown = errors.New("unknown runtime kind")

// Token is a normalized runtime-kind token.
type Token struct {
	// Mode is the canonical runtimes.Mode.
	Mode runtimes.Mode
	// Legacy reports that the input used a spelling from before agentkit
	// v0.12.0, so a caller can log it or count it.
	Legacy bool
	// Debug reports `pty-debug`, which is now `pty` plus the debug
	// posture.
	Debug bool
}

// legacy maps the older spellings onto the current modes.
var legacy = map[string]Token{
	"subprocess": {Mode: runtimes.ModeSubprocessPerTurn, Legacy: true},
	"cli":        {Mode: runtimes.ModeSubprocessPerTurn, Legacy: true},
	"serve-http": {Mode: runtimes.ModeHTTPSSE, Legacy: true},
	"app-server": {Mode: runtimes.ModeJSONRPCStdio, Legacy: true},
	"pty-debug":  {Mode: runtimes.ModePTY, Legacy: true, Debug: true},
}

// Normalize maps raw onto its canonical mode. Case and surrounding space
// are ignored and `_` reads as `-`. An empty token is the zero Token and no
// error: it means "use the runtime's default". Any other token that is not
// a runtimes.Mode or a known older spelling is ErrUnknown.
func Normalize(raw string) (Token, error) {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
	if s == "" {
		return Token{}, nil
	}
	if t, ok := legacy[s]; ok {
		return t, nil
	}
	if m := runtimes.Mode(s); m.Valid() {
		return Token{Mode: m}, nil
	}
	return Token{}, fmt.Errorf("%w %q (expected one of %s, or an older spelling: subprocess, cli, serve-http, app-server, pty-debug)", ErrUnknown, raw, modeList())
}

func modeList() string {
	modes := runtimes.Modes()
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = string(m)
	}
	return strings.Join(out, ", ")
}
