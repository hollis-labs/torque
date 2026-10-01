package config

import (
	"fmt"
	"slices"
	"strings"
)

// KnownMuxServers are the servers the `mux` MCP aggregator proxies, and so
// the names a profile's mux_servers may grant (`mux mcp --proxy --servers
// <names>`). A name outside it is almost certainly a typo, and a typo must
// not silently plant nothing, or something else, so it is a load-time error
// and a lint finding.
//
// mux takes its servers from the catalog's mcp-servers/*.yaml ids, which
// differ by machine; this is the set Torque knows: those ids on the
// operator's machine (cerberus, fragments-engine, hadron, loom, nanite,
// sigil, tangent, tangent-dev, tesseract, torque), and vanta and tether,
// which the daemon's default set and the interactive config name. A server
// mux gains is added here when a profile needs to name it.
var KnownMuxServers = []string{
	"cerberus",
	"fragments-engine",
	"hadron",
	"loom",
	"nanite",
	"sigil",
	"tangent",
	"tangent-dev",
	"tesseract",
	"tether",
	"torque",
	"vanta",
}

// DangerousMuxServers are the mux servers that grant host command execution,
// and why. None is ever part of a Claude worker's defaults; a profile that
// names one gets a warning (the load log, the lint) saying what it grants.
//   - cerberus can deploy to hosts and ssh into them.
//   - nanite serves dev_bash, which runs shell commands on the host.
var DangerousMuxServers = map[string]string{
	"cerberus": "deploy and ssh: its sessions can deploy to and run commands on hosts",
	"nanite":   "shell execution: its dev_bash tool runs shell commands on the host",
}

// validateMuxServers checks a profile's mux_servers: every entry names a
// known mux server, none is empty, and none repeats. It returns the problems
// as messages naming the entry, for the loader and the lint to share.
func validateMuxServers(servers []string) []string {
	var problems []string
	seen := map[string]bool{}
	for i, name := range servers {
		switch {
		case strings.TrimSpace(name) == "":
			problems = append(problems, fmt.Sprintf("mux_servers[%d] is empty", i))
		case !slices.Contains(KnownMuxServers, name):
			problems = append(problems, fmt.Sprintf("mux_servers[%d]: unknown mux server %q (known: %s)", i, name, strings.Join(KnownMuxServers, ", ")))
		case seen[name]:
			problems = append(problems, fmt.Sprintf("mux_servers[%d]: %q is listed twice", i, name))
		}
		seen[name] = true
	}
	return problems
}

// DangerousMuxGrants lists the dangerous mux servers (DangerousMuxServers) the
// profile's mux_servers names, in the order it names them, each with what it
// grants.
func (p AgentProfile) DangerousMuxGrants() []DangerousMuxGrant {
	var out []DangerousMuxGrant
	for _, name := range p.MuxServers {
		if why, ok := DangerousMuxServers[name]; ok {
			out = append(out, DangerousMuxGrant{Server: name, Grants: why})
		}
	}
	return out
}

// DangerousMuxGrant is one dangerous mux server a profile names.
type DangerousMuxGrant struct {
	Server string
	Grants string
}
