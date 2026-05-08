package agent

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// plantCopilotBootDir is the bespoke planter for copilot boots. The lib's
// `BootDirSpec().Notes` is non-empty for copilot in go-providers v0.8.0 —
// meaning the system-prompt loading convention and project-dir flag have
// not yet been probed against the installed CLI version.
//
// Until that research lands, copilot boots fail with a clear message.
// Filling this in is per-adapter follow-up work.
//
// To unstub: probe `copilot --help` (or `gh copilot --help`) for the
// system-prompt loading convention + project-dir flag; either fill in
// this function or upgrade the lib's spec to concrete.
func plantCopilotBootDir(p plantParams) (*bootDirResult, error) {
	bp, ok := p.Adapter.(provider.BootDirProvider)
	notes := ""
	if ok {
		notes = bp.BootDirSpec().Notes
	}
	return nil, fmt.Errorf("%w: provider=copilot notes=%s",
		ErrBootDirNotImplemented, notes)
}
