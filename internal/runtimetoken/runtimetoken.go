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

	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
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

// profileLegacy are the older spellings a profile's runtime_kind accepted
// before agentkit v0.12.0. The other older spellings (cli, app-server,
// pty-debug) were never valid in a profile; they occur only in stored
// session rows.
var profileLegacy = map[string]bool{"subprocess": true, "serve-http": true}

// ErrNotAProfileKind is returned by NormalizeProfile for an older spelling
// that is valid in a stored session row but was never valid in a profile.
var ErrNotAProfileKind = errors.New("runtime kind not accepted in a profile")

// Normalize maps raw onto its canonical mode, accepting every older
// spelling: it reads stored session rows. Case and surrounding space are
// ignored and `_` reads as `-`. An empty token is the zero Token and no
// error: it means "use the runtime's default". Any other token that is not
// a runtimes.Mode or a known older spelling is ErrUnknown.
func Normalize(raw string) (Token, error) {
	s := canonical(raw)
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

// NormalizeProfile is Normalize for a profile's runtime_kind
// (CW-20261001-0064 review). Of the older spellings it accepts only those a
// profile accepted before agentkit v0.12.0, subprocess and serve-http,
// reported with Legacy set so the loader can warn. cli, app-server and
// pty-debug stay errors in a profile, as they were: accepting them quietly
// would change what an existing profile launches.
func NormalizeProfile(raw string) (Token, error) {
	t, err := Normalize(raw)
	if err != nil {
		return Token{}, err
	}
	if t.Legacy && !profileLegacy[canonical(raw)] {
		return Token{}, fmt.Errorf("%w: %q; use %q", ErrNotAProfileKind, raw, t.Mode)
	}
	return t, nil
}

func canonical(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
}

func modeList() string {
	modes := runtimes.Modes()
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = string(m)
	}
	return strings.Join(out, ", ")
}
