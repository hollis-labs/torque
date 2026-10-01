package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/config"
)

// plantsMux withholds mux short of an explicit bypassPermissions from codex
// sessions (CW-20261001-0110) and from every ACP session (CW-20261001-0120),
// and from a Claude session on any runtime kind unless its profile names
// mux_servers (CW-20261001-0226); OpenCode on its native runtime is
// unchanged.
func TestPlantsMux(t *testing.T) {
	granted := []string{"vanta", "tesseract"}
	for _, tc := range []struct {
		provider string
		kind     RuntimeKind
		mode     string
		servers  []string
		want     bool
	}{
		{"codex", RuntimeKindJsonRpcStdio, "", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "default", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "acceptEdits", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "plan", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "dontAsk", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "bypassPermissions", nil, true},
		{"codex", RuntimeKindJsonRpcStdio, "acceptEdits", granted, false},
		{"codex", RuntimeKindJsonRpcStdio, "bypassPermissions", granted, true},
		// Claude: none by default, whatever the posture or runtime kind.
		{"claude-code", RuntimeKindStreamingStdio, "", nil, false},
		{"claude-code", RuntimeKindStreamingStdio, "plan", nil, false},
		{"claude-code", RuntimeKindStreamingStdio, "bypassPermissions", nil, false},
		{"claude-code", RuntimeKindSubprocess, "", nil, false},
		{"claude-code", "", "", nil, false},
		{"Claude-Code", RuntimeKindStreamingStdio, "", nil, false},
		// Claude over ACP: none by default either, even under bypassPermissions.
		{"claude-code", RuntimeKindACPStdio, "", nil, false},
		{"claude-code", RuntimeKindACPStdio, "acceptEdits", nil, false},
		{"claude-code", RuntimeKindACPStdio, "bypassPermissions", nil, false},
		// Claude: what a profile names is planted, in any posture, on a native kind.
		{"claude-code", RuntimeKindStreamingStdio, "", granted, true},
		{"claude-code", RuntimeKindStreamingStdio, "plan", granted, true},
		{"claude-code", RuntimeKindSubprocess, "acceptEdits", granted, true},
		{"claude-code", "", "", granted, true},
		{"Claude-Code", RuntimeKindStreamingStdio, "", granted, true},
		// ...and over ACP only under bypassPermissions, like every ACP session.
		{"claude-code", RuntimeKindACPStdio, "acceptEdits", granted, false},
		{"claude-code", RuntimeKindACPStdio, "bypassPermissions", granted, true},
		{"opencode", RuntimeKindSubprocess, "", nil, true},
		{"opencode", RuntimeKindSubprocess, "", granted, true},
		{"copilot", RuntimeKindACPStdio, "", nil, false},
		{"copilot", RuntimeKindACPStdio, "default", nil, false},
		{"copilot", RuntimeKindACPStdio, "acceptEdits", nil, false},
		{"copilot", RuntimeKindACPStdio, "plan", nil, false},
		{"copilot", RuntimeKindACPTCP, "bypassPermissions", nil, true},
		{"copilot", RuntimeKindACPStdio, "bypassPermissions", nil, true},
		{"copilot", RuntimeKindACPStdio, "acceptEdits", granted, false},
		{"opencode", RuntimeKindACPStdio, "", nil, false},
		{"pi", RuntimeKindACPStdio, "bypassPermissions", nil, true},
	} {
		got := plantsMux(config.AgentProfile{Provider: tc.provider, PermissionMode: tc.mode, MuxServers: tc.servers}, tc.kind)
		assert.Equal(t, tc.want, got, "%s/%s/%q/%v", tc.provider, tc.kind, tc.mode, tc.servers)
	}
}

// A profile's mux_servers restrict mux with `--only`, not `--servers`:
// `--servers` only picks which upstream tools mux surfaces natively, while
// mux_discover and mux_call still reach every server in its catalog and mux's
// own Tether tools stay on the planted token and scopes; `--only` is the
// curated mode without any of that. mux refuses --only beside --servers or
// --broker, and without --proxy, so those are rewritten; the daemon's token
// and scopes stay.
func TestMuxArgsFor(t *testing.T) {
	base := []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus", "--token", "local-dev", "--scopes", "session.write"}
	named := []string{"vanta", "tesseract"}
	want := []string{"mcp", "--proxy", "--token", "local-dev", "--scopes", "session.write", "--only", "vanta,tesseract"}

	assert.Equal(t, base, muxArgsFor(base, nil), "no servers named: the daemon's own argv stands")
	assert.Equal(t, want, muxArgsFor(base, named))
	assert.NotContains(t, muxArgsFor(base, named), "--servers", "never --servers, which leaves mux_call open")

	for name, daemon := range map[string][]string{
		"the = spelling":              {"mcp", "--proxy", "--servers=torque", "--token", "local-dev", "--scopes", "session.write"},
		"an existing --only":          {"mcp", "--proxy", "--only", "torque", "--token", "local-dev", "--scopes", "session.write"},
		"an existing --only=":         {"mcp", "--proxy", "--only=torque", "--token", "local-dev", "--scopes", "session.write"},
		"--broker, which --only bars": {"mcp", "--proxy", "--broker", "--token", "local-dev", "--scopes", "session.write", "--servers", "torque"},
		"--servers twice":             {"mcp", "--proxy", "--servers", "torque", "--token", "local-dev", "--scopes", "session.write", "--servers", "vanta"},
	} {
		assert.Equal(t, want, muxArgsFor(daemon, named), name)
	}
	assert.Equal(t, []string{"mcp", "--proxy", "--only", "vanta,tesseract"}, muxArgsFor([]string{"mcp", "--proxy", "--servers"}, named), "a dangling --servers")
	assert.Equal(t, []string{"mcp", "--token", "x", "--proxy", "--only", "vanta,tesseract"}, muxArgsFor([]string{"mcp", "--servers", "--token", "x"}, named),
		"a dangling --servers does not swallow the next flag, and --proxy, which --only needs, is added")
	assert.Equal(t, []string{"mcp", "--proxy", "--only", "vanta,tesseract"}, muxArgsFor([]string{"mcp", "--proxy=false", "--servers", "torque"}, named), "--proxy=false")

	// cerberus is only ever in the planted set when the profile names it.
	assert.NotContains(t, strings.Join(muxArgsFor(base, named), " "), "cerberus")
	assert.Contains(t, strings.Join(muxArgsFor(base, []string{"vanta", "cerberus"}), " "), "--only vanta,cerberus")
	assert.Equal(t, "vanta,torque,cerberus", base[3], "the daemon's argv is not modified")
}

// planMux decides what mux a session gets (CW-20261001-0226).
func TestPlanMux(t *testing.T) {
	daemon := &Dependencies{MuxCommand: "/usr/bin/mux", MuxArgs: []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus", "--token", "t"}}
	claude := config.AgentProfile{Provider: "claude-code"}
	granted := claude
	granted.MuxServers = []string{"vanta", "torque"}

	// Claude gets none by default.
	plan := planMux(daemon, claude, RuntimeKindStreamingStdio)
	assert.False(t, plan.Plant)
	assert.Contains(t, plan.Why, "mux_servers")

	// A grant plants exactly those servers.
	plan = planMux(daemon, granted, RuntimeKindStreamingStdio)
	assert.True(t, plan.Plant)
	assert.Equal(t, []string{"mcp", "--proxy", "--token", "t", "--only", "vanta,torque"}, plan.Args)
	assert.Empty(t, plan.Notes)

	// A grant with no mux on the daemon cannot be honoured, and says so.
	plan = planMux(&Dependencies{}, granted, RuntimeKindStreamingStdio)
	assert.False(t, plan.Plant)
	assert.True(t, plan.Warn, "a grant that cannot be made is a WARN, not a silent nothing")
	assert.Contains(t, plan.Why, "no mux server to plant")
	// ...but a profile that asked for nothing is not warned.
	plan = planMux(&Dependencies{}, claude, RuntimeKindStreamingStdio)
	assert.False(t, plan.Plant)
	assert.False(t, plan.Warn)

	// While Torque's state is write-protected, torque is dropped from a
	// profile's grant, with a note; the others stay.
	protected := *daemon
	protected.MuxOmitsTorque = true
	plan = planMux(&protected, granted, RuntimeKindStreamingStdio)
	assert.True(t, plan.Plant)
	assert.Equal(t, []string{"mcp", "--proxy", "--token", "t", "--only", "vanta"}, plan.Args)
	if assert.Len(t, plan.Notes, 1) {
		assert.Contains(t, plan.Notes[0], "torque is dropped")
	}
	// A grant of torque alone drops to nothing: no mux, never the daemon's wider default.
	torqueOnly := claude
	torqueOnly.MuxServers = []string{"torque"}
	plan = planMux(&protected, torqueOnly, RuntimeKindStreamingStdio)
	assert.False(t, plan.Plant)
	assert.True(t, plan.Warn)
	opencodeTorqueOnly := config.AgentProfile{Provider: "opencode", MuxServers: []string{"torque"}}
	plan = planMux(&protected, opencodeTorqueOnly, RuntimeKindSubprocess)
	assert.False(t, plan.Plant, "opencode, which gets the daemon's default set when it names none, must not fall back to it")
	// Without protection torque is the profile's to name.
	plan = planMux(daemon, torqueOnly, RuntimeKindStreamingStdio)
	assert.True(t, plan.Plant)

	// Claude over ACP: none even under bypassPermissions unless granted.
	acp := config.AgentProfile{Provider: "claude-code", PermissionMode: "bypassPermissions"}
	assert.False(t, planMux(daemon, acp, RuntimeKindACPStdio).Plant)
	acp.MuxServers = []string{"vanta"}
	assert.True(t, planMux(daemon, acp, RuntimeKindACPStdio).Plant)
}

func TestMuxServersLogValue(t *testing.T) {
	assert.Equal(t, "--only vanta,torque", muxServersLogValue([]string{"mcp", "--proxy", "--only", "vanta,torque"}))
	assert.Equal(t, "--only=vanta", muxServersLogValue([]string{"mcp", "--only=vanta"}))
	assert.Equal(t, "--servers vanta", muxServersLogValue([]string{"mcp", "--servers", "vanta"}))
	assert.Contains(t, muxServersLogValue([]string{"mcp", "--proxy"}), "default servers")
}
