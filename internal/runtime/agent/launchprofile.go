package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/go-agent-launch/agentlaunch/catalog"
)

// ErrLaunchProfile wraps every failure in the optional launch-profile
// resolution path. Boot wraps the underlying cause (a bad path, a parse
// error, a missing launch id, an unmappable runtime) with this sentinel
// so callers can errors.Is it and so a misconfigured launch profile
// fails cleanly rather than panicking. The no-launch-profile default
// path never produces this error.
var ErrLaunchProfile = errors.New("agent: launch profile")

// launchProfileSource is the resolved result of a launch-profile
// reference. It carries the BASE agentlaunch.LaunchPlan the shared
// go-agent-launch catalog produced plus the provenance Torque threads
// into launcher.Compile via WithSourceCatalog.
//
// The base plan is NOT the plan Boot compiles directly. Torque overlays
// every runtime-critical field (boot prompt / kickoff body, workspace
// dirs, MCP loopback URL, project/agent identity) on top of it in
// buildLaunchPlan — see overlayLaunchProfile. A launch profile only
// contributes provider / runtime / workspace-mode / mode / labels /
// annotations base values; it can never break loopback auth, workspace
// ownership, or boot-prompt planting.
type launchProfileSource struct {
	// BasePlan is the LaunchPlan the catalog port resolved. Provider,
	// Runtime, Mode, Workspace.Mode, Metadata, and MCP allowlist are
	// read from it as base values; everything else is overlaid by
	// Torque.
	BasePlan agentlaunch.LaunchPlan

	// SourceCatalog is the absolute (or caller-supplied) path of the
	// catalog/launch YAML the plan originated from. Threaded into
	// launcher.Compile via WithSourceCatalog for provenance. Empty when
	// the profile was supplied as an inline payload.
	SourceCatalog string

	// SourceCatalogVersion is the catalog's self-reported version, when
	// the source carried one. Empty for single-launch / inline sources.
	SourceCatalogVersion string
}

// resolveLaunchProfileInput bundles the launch-profile inputs Boot
// extracts from the agent profile + Options. All three fields are
// optional; when every field is empty resolveLaunchProfile returns
// (nil, nil) and Boot stays on the pure-inline buildLaunchPlan path.
type resolveLaunchProfileInput struct {
	// ProfileRef is config.AgentProfile.LaunchProfile — the operator's
	// profiles.yaml-declared reference. Lowest precedence.
	ProfileRef string

	// OptionsRef is Options.LaunchProfile — the per-Boot override.
	// Wins over ProfileRef.
	OptionsRef string

	// InlinePayload is Options.LaunchProfileInline — a raw launch-profile
	// YAML body for standalone use (no catalog file on disk). Wins over
	// both reference forms when non-empty.
	InlinePayload []byte
}

// referenced reports whether any launch-profile input was supplied.
func (in resolveLaunchProfileInput) referenced() bool {
	return in.OptionsRef != "" || in.ProfileRef != "" || len(in.InlinePayload) > 0
}

// resolveLaunchProfile turns a launch-profile reference into a
// launchProfileSource, or returns (nil, nil) when no profile is
// referenced (the default, pure-inline path).
//
// Precedence (highest first): InlinePayload, OptionsRef, ProfileRef.
// An inline payload is a fully self-contained launch profile YAML; a
// reference is a filesystem path with an optional "#launchID" suffix.
//
// Reference path forms:
//
//   - "<path>"            — a single-launch YAML file (LoadLaunch), OR a
//     catalog directory / global.yaml with exactly one launch entry.
//   - "<path>#<launchID>" — a catalog directory / global.yaml; the named
//     launch entry is resolved.
//
// Resolution is standalone — it uses go-agent-launch's catalog package
// directly against a local path or the inline bytes. No Tether daemon or
// Tether catalog is required.
func resolveLaunchProfile(in resolveLaunchProfileInput) (*launchProfileSource, error) {
	switch {
	case len(in.InlinePayload) > 0:
		return resolveInlineLaunchProfile(in.InlinePayload)
	case in.OptionsRef != "":
		return resolveLaunchProfileRef(in.OptionsRef)
	case in.ProfileRef != "":
		return resolveLaunchProfileRef(in.ProfileRef)
	default:
		return nil, nil
	}
}

// resolveInlineLaunchProfile parses a self-contained launch-profile YAML
// body and translates it to a base LaunchPlan. The inline payload must
// carry every catalog entry it references — a bare single-launch profile
// (project/agent/provider IDs only) cannot resolve without the sibling
// entries, so the payload is loaded as a self-contained GlobalCatalog
// (inline projects/agents/providers/launches lists).
func resolveInlineLaunchProfile(payload []byte) (*launchProfileSource, error) {
	g, err := catalog.LoadGlobalFromBytes(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: parse inline payload: %v", ErrLaunchProfile, err)
	}
	if len(g.Launches) == 0 {
		return nil, fmt.Errorf("%w: inline payload carries no launch entries (need an inline GlobalCatalog with projects/agents/providers/launches)", ErrLaunchProfile)
	}
	if len(g.Launches) > 1 {
		return nil, fmt.Errorf("%w: inline payload carries %d launch entries — supply exactly one, or use a catalog path with a #launchID suffix", ErrLaunchProfile, len(g.Launches))
	}
	plan, err := g.Resolve(g.Launches[0].ID)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve inline launch %q: %v", ErrLaunchProfile, g.Launches[0].ID, err)
	}
	return &launchProfileSource{
		BasePlan:             *plan,
		SourceCatalogVersion: g.Version,
	}, nil
}

// resolveLaunchProfileRef resolves a "<path>" or "<path>#<launchID>"
// reference against a local catalog directory, a global.yaml file, or a
// single-launch YAML file.
func resolveLaunchProfileRef(ref string) (*launchProfileSource, error) {
	path, launchID := splitLaunchRef(ref)
	if path == "" {
		return nil, fmt.Errorf("%w: empty path in reference %q", ErrLaunchProfile, ref)
	}

	if launchID != "" {
		// Explicit launch id — must resolve through a GlobalCatalog so the
		// project/agent/provider entries are available.
		g, err := catalog.LoadGlobal(path)
		if err != nil {
			return nil, fmt.Errorf("%w: load catalog %q: %v", ErrLaunchProfile, path, err)
		}
		plan, err := g.Resolve(launchID)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve launch %q in %q: %v", ErrLaunchProfile, launchID, path, err)
		}
		return &launchProfileSource{
			BasePlan:             *plan,
			SourceCatalog:        path,
			SourceCatalogVersion: g.Version,
		}, nil
	}

	// No explicit launch id. Try the path as a full catalog first
	// (directory or global.yaml); if it carries exactly one launch we
	// resolve that. If LoadGlobal fails (e.g. the path is a bare
	// single-launch YAML with no sibling entries) fall back to LoadLaunch
	// + a self-contained translate.
	g, gErr := catalog.LoadGlobal(path)
	if gErr == nil && len(g.Launches) > 0 {
		if len(g.Launches) > 1 {
			return nil, fmt.Errorf("%w: catalog %q has %d launch entries — append #<launchID> to pick one", ErrLaunchProfile, path, len(g.Launches))
		}
		plan, err := g.Resolve(g.Launches[0].ID)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve launch %q in %q: %v", ErrLaunchProfile, g.Launches[0].ID, path, err)
		}
		return &launchProfileSource{
			BasePlan:             *plan,
			SourceCatalog:        path,
			SourceCatalogVersion: g.Version,
		}, nil
	}

	// Fall back to a single-launch file. ToLaunchPlan needs a non-nil
	// GlobalCatalog to resolve the project/agent/provider IDs the launch
	// references; a bare single-launch file therefore must carry inline
	// entries or be paired with a catalog. Surface a clear error rather
	// than panicking.
	lp, lErr := catalog.LoadLaunch(path)
	if lErr != nil {
		// Prefer the catalog-load error when it was the more specific
		// failure (a real catalog that failed to parse), else the
		// single-launch error.
		if gErr != nil {
			return nil, fmt.Errorf("%w: %q is neither a resolvable catalog (%v) nor a launch file (%v)", ErrLaunchProfile, path, gErr, lErr)
		}
		return nil, fmt.Errorf("%w: load launch %q: %v", ErrLaunchProfile, path, lErr)
	}
	plan, err := lp.ToLaunchPlan(g)
	if err != nil {
		return nil, fmt.Errorf("%w: translate launch %q (a standalone single-launch file must reference catalog entries reachable from the same path; use a catalog directory or an inline payload): %v", ErrLaunchProfile, path, err)
	}
	return &launchProfileSource{
		BasePlan:      *plan,
		SourceCatalog: path,
	}, nil
}

// splitLaunchRef splits a "<path>#<launchID>" reference into its path and
// launch-id parts. A reference with no '#' returns (ref, ""). Surrounding
// whitespace is trimmed from both halves.
func splitLaunchRef(ref string) (path, launchID string) {
	ref = strings.TrimSpace(ref)
	if i := strings.LastIndex(ref, "#"); i >= 0 {
		return strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:])
	}
	return ref, ""
}
