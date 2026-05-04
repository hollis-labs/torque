package bootstrap

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/cliexec"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// SessionMgr constructs the long-lived session Manager and runs the
// startup orphan sweep. The Manager is wired against the existing
// scheduler EventBus so session.state_changed transitions ride the same
// SSE machinery as run_events.
//
// Returns a constructed-but-not-bound Manager so callsites (cmd/clockwork
// serve, future tests) decide where to register the HTTP/MCP surfaces.
//
// Safe to call with a nil scheduler bus — events are silently dropped in
// that mode (matches the rest of the runtime stack's nil-sink convention).
func SessionMgr(store *sqlstore.Store, profiles config.ProfileMap, bus *scheduler.EventBus) (*sessionmgr.Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("sessionmgr bootstrap: store is nil")
	}
	emitter := sessionmgr.NewSchedulerEmitter(bus)
	registry := &cliAdapterRegistry{profiles: profiles}
	mgr := sessionmgr.New(store, registry, emitter)

	swept, err := mgr.Sweep()
	if err != nil {
		return nil, fmt.Errorf("sessionmgr orphan sweep: %w", err)
	}
	if swept > 0 {
		// Surface the sweep count via a top-level lifecycle event so
		// dashboards know a daemon restart marked sessions crashed. No log
		// line — emitter handles dispatch; nil-emitter case silently drops.
		emitter.EmitSessionEvent("session.sweep", map[string]interface{}{
			"swept": swept,
		})
	}
	return mgr, nil
}

// cliAdapterRegistry resolves clockwork agent profiles to go-providers
// CLIAdapters wrapped in agentsessions.Runtime. Mirrors cliexec's
// adapterFor (kept private to that package) but composes a Runtime via
// agentsessions.NewFromAdapter so the Manager has something to launch.
type cliAdapterRegistry struct {
	profiles config.ProfileMap
}

// RuntimeFor satisfies sessionmgr.AdapterRegistry. resumeHint is plumbed
// through SessionIDPreset for adapters that understand it (claude); for
// others the hint is currently dropped — adapter-specific resume wiring
// is deferred to per-provider follow-ups.
func (r *cliAdapterRegistry) RuntimeFor(profileName string, resumeHint []byte) (agentsessions.Runtime, error) {
	profile := config.GetProfileOrDefault(r.profiles, profileName)
	cliAdapter, caps, err := resolveAdapter(profile, profileName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", sessionmgr.ErrAdapterNotFound, err)
	}

	resumeID := ""
	if len(resumeHint) > 0 && caps.ProviderSessionID {
		// Adapters that store their provider session id as the hint can
		// re-resume by passing it as SessionIDPreset. claude is the only
		// such adapter today; the bytes match the upstream id encoding.
		resumeID = string(resumeHint)
	}

	buildArgs := func(turnPrompt, sessionID string) []string {
		args := cliAdapter.BuildArgs(turnPrompt, "", sessionID)
		if profile.Model != "" {
			args = append(args, "--model", profile.Model)
		}
		return args
	}

	rt, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{
		ID:        "clockwork-cli/" + cliAdapter.Name(),
		Kind:      "cli",
		Adapter:   cliAdapter,
		Caps:      caps,
		BuildArgs: buildArgs,
	})
	if err != nil {
		return nil, err
	}

	if resumeID != "" {
		// Wrap the runtime so Start passes SessionIDPreset on the first
		// turn. Subsequent turns ignore the preset (adapter remembers).
		rt = &resumeRuntime{Runtime: rt, sessionIDPreset: resumeID}
	}

	if err := rt.Prepare(context.Background()); err != nil {
		return nil, fmt.Errorf("prepare runtime for %s: %w", profileName, err)
	}
	return rt, nil
}

// resolveAdapter is the lib-internal mirror of cliexec.adapterFor. The
// cliexec helper is private; we re-derive here so sessionmgr boots without
// changing cliexec's surface. Provider list MUST stay in sync with cliexec
// — divergence would mean a session can launch under a provider that the
// per-task executor wouldn't accept.
func resolveAdapter(profile config.AgentProfile, profileName string) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	switch profile.Provider {
	case "claude":
		caps := agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}
		if cliexec.ProfileIsDevMode(profile) {
			return provider.NewClaudeAdapterDev(), caps, nil
		}
		return provider.NewClaudeAdapter(), caps, nil
	case "codex":
		return provider.NewCodexAdapter(), agentsessions.Capabilities{BinaryRequired: true}, nil
	case "gemini":
		return provider.NewGeminiAdapter(), agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}, nil
	case "copilot":
		return provider.NewCopilotAdapter(), agentsessions.Capabilities{BinaryRequired: true}, nil
	case "opencode":
		if profileName == "" {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider requires a profile name (used as --agent)")
		}
		adapter := provider.NewOpencodeAdapter()
		adapter.Agent = profileName
		return adapter, agentsessions.Capabilities{BinaryRequired: true}, nil
	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile %q has empty provider", profileName)
	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q for profile %q", profile.Provider, profileName)
	}
}

// resumeRuntime wraps an agentsessions.Runtime so the first Start call
// injects SessionIDPreset. The preset is one-shot — the wrapped runtime
// hands subsequent Starts (e.g. a second resume) through unchanged.
type resumeRuntime struct {
	agentsessions.Runtime
	sessionIDPreset string
	consumed        bool
}

// Start applies the preset on the first invocation, then unwraps for
// subsequent ones.
func (r *resumeRuntime) Start(ctx context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	if !r.consumed && r.sessionIDPreset != "" {
		opts.SessionIDPreset = r.sessionIDPreset
		r.consumed = true
	}
	return r.Runtime.Start(ctx, opts)
}
