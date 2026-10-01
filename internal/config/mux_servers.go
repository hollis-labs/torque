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

// MuxServerCerberus is the mux server that can deploy to and ssh into hosts.
// It is never part of what a Claude worker gets by default, and a profile
// that names it is warned (load log, lint) that it grants that.
const MuxServerCerberus = "cerberus"

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

// GrantsCerberus reports whether the profile's mux_servers names cerberus.
func (p AgentProfile) GrantsCerberus() bool {
	return slices.Contains(p.MuxServers, MuxServerCerberus)
}
