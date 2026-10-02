package bootstrap

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/mcpbridge"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

func TestDaemonMCPURL(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8990}, "http://127.0.0.1:8990/mcp"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 8990}, "http://127.0.0.1:8990/mcp"},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 8990}, "http://127.0.0.1:8990/mcp"},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 8991}, "http://[::1]:8991/mcp"},
		{&net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 8990}, "http://10.0.0.5:8990/mcp"},
		{&net.UnixAddr{Name: "/tmp/torque.sock", Net: "unix"}, ""},
	} {
		assert.Equal(t, tc.want, DaemonMCPURL(tc.addr), "%v", tc.addr)
	}
}

// Under protection, with a mux, the planted mux env points mux's
// `torque mcp` at the daemon's /mcp; otherwise nothing is planted.
func TestPlantRemoteMCP(t *testing.T) {
	listen := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8990}
	want := mcpbridge.RemoteEnv + "=http://127.0.0.1:8990/mcp"

	deps := &agent.Dependencies{ProtectedPaths: []string{"/data/torque"}, MuxCommand: "/usr/bin/mux", MuxEnv: []string{"KEEP=1"}}
	PlantRemoteMCP(deps, listen, false)
	assert.Equal(t, []string{"KEEP=1", want}, deps.MuxEnv)

	for name, d := range map[string]*agent.Dependencies{
		"protection off": {MuxCommand: "/usr/bin/mux"},
		"no mux":         {ProtectedPaths: []string{"/data/torque"}},
	} {
		PlantRemoteMCP(d, listen, false)
		assert.Empty(t, d.MuxEnv, name)
	}
	tokened := &agent.Dependencies{ProtectedPaths: []string{"/data/torque"}, MuxCommand: "/usr/bin/mux"}
	PlantRemoteMCP(tokened, listen, true)
	assert.Empty(t, tokened.MuxEnv, "agents never get TORQUE_API_TOKEN, so a tokened API is not planted")
}
