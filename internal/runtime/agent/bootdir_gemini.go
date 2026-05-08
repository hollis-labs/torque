package agent

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// plantGeminiBootDir is the bespoke planter for gemini boots. The lib's
// `BootDirSpec().Notes` is non-empty for gemini in go-providers v0.8.0
// (per the implementer-report's BootDirSpec coverage table) — meaning
// the cwd-load convention, GEMINI_SYSTEM_MD env var path, config-dir
// support, and project-dir flag have not yet been probed against the
// installed CLI version.
//
// Until that research lands, gemini boots fail with a clear message.
// Filling this in is per-adapter follow-up work tracked by the
// portfolio agent-boot-unification initiative.
//
// To unstub: probe `gemini --help` for cwd-load convention + env var +
// project-dir flag; either fill in this function with a bespoke layout
// or upgrade the lib's spec from stub (Notes != "") to concrete (with
// PlantedFiles). The dispatcher in bootdir.go will route to whichever
// path is filled in first.
func plantGeminiBootDir(p plantParams) (*bootDirResult, error) {
	bp, ok := p.Adapter.(provider.BootDirProvider)
	notes := ""
	if ok {
		notes = bp.BootDirSpec().Notes
	}
	return nil, fmt.Errorf("%w: provider=gemini notes=%s",
		ErrBootDirNotImplemented, notes)
}
