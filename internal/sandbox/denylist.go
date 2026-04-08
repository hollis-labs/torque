package sandbox

import "strings"

type denyEntry struct {
	pattern string
	// allOf lists additional substrings that must ALL be present (AND logic).
	// If empty, pattern alone is sufficient.
	allOf  []string
	reason string
}

var defaultDenyPatterns = []denyEntry{
	{pattern: "rm -rf /", reason: "recursive delete of root filesystem"},
	{pattern: "rm -rf /*", reason: "recursive delete of root filesystem"},
	{pattern: "rm -rf ~", reason: "recursive delete of home directory"},
	{pattern: "rm -rf $home", reason: "recursive delete of home directory"},
	{pattern: "mkfs", reason: "filesystem format command"},
	{pattern: "dd if=", reason: "raw disk write (dd)"},
	{pattern: "dd of=/dev", reason: "raw disk write (dd)"},
	{pattern: "shutdown", reason: "system shutdown"},
	{pattern: "reboot", reason: "system reboot"},
	{pattern: "halt", reason: "system halt"},
	{pattern: "poweroff", reason: "system poweroff"},
	{pattern: "init 0", reason: "system shutdown (init 0)"},
	{pattern: "init 6", reason: "system reboot (init 6)"},
	{pattern: ":(){ :|:& };:", reason: "fork bomb"},
	{pattern: "chmod -r 777 /", reason: "recursive permission change on root"},
	{pattern: "chown -r", reason: "recursive ownership change"},
	// Pipe-to-shell patterns: both parts must be present anywhere in the command
	{pattern: "curl", allOf: []string{"| sh"}, reason: "pipe remote script to shell"},
	{pattern: "curl", allOf: []string{"| bash"}, reason: "pipe remote script to shell"},
	{pattern: "wget", allOf: []string{"| sh"}, reason: "pipe remote script to shell"},
	{pattern: "wget", allOf: []string{"| bash"}, reason: "pipe remote script to shell"},
	{pattern: "> /dev/sda", reason: "raw disk overwrite"},
	{pattern: "> /dev/disk", reason: "raw disk overwrite"},
	{pattern: "mv / ", reason: "move root filesystem"},
	{pattern: "mv /* ", reason: "move root filesystem"},
}

// CheckDenylist checks whether command matches any denied pattern.
// Returns (true, reason) if blocked, (false, "") if allowed.
func CheckDenylist(command string) (blocked bool, reason string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, entry := range defaultDenyPatterns {
		if !strings.Contains(lower, entry.pattern) {
			continue
		}
		match := true
		for _, extra := range entry.allOf {
			if !strings.Contains(lower, extra) {
				match = false
				break
			}
		}
		if match {
			return true, entry.reason
		}
	}
	return false, ""
}
