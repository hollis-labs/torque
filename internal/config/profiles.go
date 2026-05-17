package config

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// CatalogProviderID returns the models.dev catalog provider id for a
// profile-style provider name. CLI brands ("claude", "codex") don't match
// the upstream catalog's vendor namespacing ("anthropic", "openai") so
// callers that hand the profile's Provider straight to a catalog lookup
// silently miss every time.
//
// Currently used by the scheduler's cost resolver (see
// internal/runtime/scheduler/cost.go). The capability/precheck path
// today passes the raw `Provider` field through without normalization;
// when that gate is wired to use this helper, update this comment.
//
// Returns the input unchanged when no alias is known — callers that want
// strict matching can compare result == input to detect aliasing.
//
// Verified against https://models.dev/api.json on 2026-05-10. Extend
// this map when adding new CLI providers.
func CatalogProviderID(provider string) string {
	switch provider {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	default:
		return provider
	}
}

// AgentProfile defines the configuration for an executor agent.
// It supports both CLI and API executors with a unified structure.
type AgentProfile struct {
	// Common fields
	Executor string `yaml:"executor"` // "cli" or "api"
	Provider string `yaml:"provider"` // claude, codex, copilot, gemini, anthropic, openai, ollama, etc.
	Model    string `yaml:"model"`

	// CLI-specific fields
	Command        string   `yaml:"command,omitempty"`
	Args           []string `yaml:"args,omitempty"`
	OutputFormat   string   `yaml:"output_format,omitempty"` // "stream-json" or "print"
	TimeoutSeconds int      `yaml:"timeout_seconds,omitempty"`
	MaxAgentDepth  int      `yaml:"max_agent_depth,omitempty"`
	// RuntimeKind is the operator-side runtime-kind override. Empty
	// (yaml absent) → the agent substrate's per-provider matrix decides
	// (see runtime/agent/factory.go::selectRuntimeKind). Non-empty values
	// force a specific go-agent-sessions runtime kind for this profile:
	//
	//   "subprocess"      — single-shot subprocess per turn (claude bare,
	//                       opencode, codex print-mode)
	//   "pty"             — long-lived PTY/TUI (escape hatch; no provider
	//                       defaults here today, TUI driving unproven)
	//   "streaming-stdio" — long-lived NDJSON over stdin/stdout (claude-code)
	//   "jsonrpc-stdio"   — long-lived JSON-RPC 2.0 over stdio (codex
	//                       app-server)
	//
	// Replaces the legacy boolean `pty:` field (dropped 2026-05-13 with
	// the codex app-server flip). No compat shim per
	// feedback_no_compat_shims — pre-launch, no operator config relied on
	// the old field name.
	RuntimeKind      string   `yaml:"runtime_kind,omitempty"`
	EnvStripPrefixes []string `yaml:"env_strip_prefixes,omitempty"`

	// LaunchProfile optionally opts this agent profile into a shared
	// go-agent-launch launch profile (CW-20260515-0021). Empty (the
	// default) keeps the pure agent-profile behavior — Boot builds the
	// LaunchPlan inline from the profile fields. When set, Boot resolves
	// the launch profile into a base LaunchPlan and overlays Torque's
	// runtime-critical fields on top (see runtime/agent/launchprofile.go
	// and buildLaunchPlanFromProfile for the precedence rules).
	//
	// Value forms:
	//
	//   - "<path>"            — a single-launch YAML file, or a catalog
	//     directory / global.yaml carrying exactly one launch entry.
	//   - "<path>#<launchID>" — a catalog directory / global.yaml; the
	//     named launch entry is resolved.
	//
	// Resolution is standalone: a local file path is sufficient and no
	// Tether daemon or Tether catalog is required. See
	// docs/launch-profiles.md.
	LaunchProfile string `yaml:"launch_profile,omitempty"`

	// API-specific fields
	Temperature  float64  `yaml:"temperature,omitempty"`
	MaxTokens    int      `yaml:"max_tokens,omitempty"`
	SystemPrompt string   `yaml:"system_prompt,omitempty"`
	Tools        []string `yaml:"tools,omitempty"`
	BaseURL      string   `yaml:"base_url,omitempty"`
	APIKey       string   `yaml:"api_key,omitempty"`
}

// ProfileMap is a named collection of agent profiles.
type ProfileMap map[string]AgentProfile

// CurrentProfiles returns a defensive copy of the map so callers cannot race
// with a future reload by mutating the returned value.
func (p ProfileMap) CurrentProfiles() ProfileMap {
	return cloneProfileMap(p)
}

// ProfileSource exposes the current profiles.yaml snapshot. Plain ProfileMap
// fixtures implement it directly; the serve daemon uses ReloadableProfiles so
// the runtime can swap snapshots in place after startup.
type ProfileSource interface {
	CurrentProfiles() ProfileMap
}

// ReloadableProfiles holds the live profiles.yaml snapshot for long-lived
// processes. Reads return clones so callers never share a mutable map with the
// reloader goroutine.
type ReloadableProfiles struct {
	mu       sync.RWMutex
	path     string
	profiles ProfileMap
}

// NewReloadableProfiles constructs a live source with the provided initial
// snapshot. initial may be nil.
func NewReloadableProfiles(path string, initial ProfileMap) *ReloadableProfiles {
	return &ReloadableProfiles{
		path:     path,
		profiles: cloneProfileMap(initial),
	}
}

// Path returns the profiles.yaml path this source reloads from.
func (r *ReloadableProfiles) Path() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path
}

// CurrentProfiles returns the current snapshot.
func (r *ReloadableProfiles) CurrentProfiles() ProfileMap {
	if r == nil {
		return ProfileMap{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneProfileMap(r.profiles)
}

// Store replaces the live snapshot.
func (r *ReloadableProfiles) Store(profiles ProfileMap) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = cloneProfileMap(profiles)
}

// Reload reads the configured path and swaps the snapshot on success.
func (r *ReloadableProfiles) Reload() error {
	if r == nil {
		return fmt.Errorf("reload profiles: source is nil")
	}
	path := r.Path()
	profiles, err := LoadProfiles(path)
	if err != nil {
		return err
	}
	r.Store(profiles)
	return nil
}

// profilesFile is the top-level YAML structure.
//
// `agent_profile_aliases` (EDGE 3, CW-20260517-0011) maps a legacy or
// shorthand name to a canonical key in `agent_profiles`. It exists so a
// provider-honest rename of a profile (e.g. `torque-backend` →
// `codex-gpt5-long`) does not break in-flight tasks or operator muscle
// memory: the old name keeps resolving via the alias. Aliases are
// resolved at lookup time (GetProfileOrDefault / ResolveProfile), not
// flattened into the map, so `torque profiles` listings show only the
// canonical set.
type profilesFile struct {
	AgentProfiles ProfileMap        `yaml:"agent_profiles"`
	Aliases       map[string]string `yaml:"agent_profile_aliases"`
}

// Profiles is the fully-loaded profile configuration: the canonical
// profile map plus the alias table. Callers that only need the map can
// keep using the bare ProfileMap returned by LoadProfiles; callers that
// want alias-aware resolution use LoadProfilesFile + ResolveProfile.
type Profiles struct {
	Profiles ProfileMap
	Aliases  map[string]string
}

// LoadProfiles reads agent profiles from a YAML file. Aliases declared in
// the file are folded in so a lookup of an alias name against the
// returned map succeeds — this keeps the legacy ProfileMap-only callers
// working after an EDGE 3 rename. Use LoadProfilesFile when you need to
// distinguish a canonical name from an alias (e.g. for listings or lint).
func LoadProfiles(path string) (ProfileMap, error) {
	pf, err := LoadProfilesFile(path)
	if err != nil {
		return nil, err
	}
	return pf.flattened(), nil
}

// LoadProfilesFile reads agent profiles plus the alias table from a YAML
// file, keeping the two separate.
func LoadProfilesFile(path string) (Profiles, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profiles{}, fmt.Errorf("load profiles %s: %w", path, err)
	}

	var f profilesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Profiles{}, fmt.Errorf("parse profiles %s: %w", path, err)
	}

	out := Profiles{Profiles: f.AgentProfiles, Aliases: f.Aliases}
	if out.Profiles == nil {
		out.Profiles = ProfileMap{}
	}

	// An alias pointing at a missing canonical profile is an operator
	// mistake that would otherwise surface much later as a confusing
	// empty-provider error; reject it at load time, naming the file.
	for alias, target := range out.Aliases {
		if _, ok := out.Profiles[target]; !ok {
			return Profiles{}, fmt.Errorf(
				"parse profiles %s: agent_profile_aliases[%q] points at %q, which is not a key in agent_profiles",
				path, alias, target)
		}
	}

	return out, nil
}

// flattened returns a ProfileMap with each alias added as an additional
// key resolving to its canonical profile. A canonical key always wins
// over an alias of the same name.
func (p Profiles) flattened() ProfileMap {
	if len(p.Aliases) == 0 {
		return p.Profiles
	}
	out := make(ProfileMap, len(p.Profiles)+len(p.Aliases))
	for alias, target := range p.Aliases {
		if prof, ok := p.Profiles[target]; ok {
			out[alias] = prof
		}
	}
	for name, prof := range p.Profiles {
		out[name] = prof
	}
	return out
}

// CurrentProfiles returns a defensive-copy snapshot for any profile source.
// Nil sources read as an empty registry.
func CurrentProfiles(source ProfileSource) ProfileMap {
	if source == nil {
		return ProfileMap{}
	}
	return source.CurrentProfiles()
}

// GetProfileOrDefault returns the named profile, falling back to "default",
// substrate-builtin profiles (CW-20260503-0019: reviewer-end-agent), and
// finally a zero-value profile if none of those resolve.
//
// User-supplied profiles.yaml (which the project loads from disk) ALWAYS
// wins over the builtin fallback — operators who want a different model
// or executor for a substrate-spawned internal task can simply add the
// matching name to their config. The fallback is the safety net so the
// scheduler's lifecycle hooks still dispatch on a fresh install where
// the user hasn't yet configured the substrate's automation roles.
func GetProfileOrDefault(source ProfileSource, name string) AgentProfile {
	profiles := CurrentProfiles(source)
	if name != "" {
		if p, ok := profiles[name]; ok {
			return p
		}
		if p, ok := builtinProfiles[name]; ok {
			return p
		}
	}
	if p, ok := profiles["default"]; ok {
		return p
	}
	// EDGE 1 (CW-20260517-0011): the bare zero-value fallback used to be
	// fully silent — a misspelled or wrong-registry agent_profile name
	// sailed through here and only blew up much later as the misleading
	// "adapter not registered for provider: profile has empty provider".
	// We still return the zero value (the substrate's lifecycle hooks
	// depend on a non-erroring resolver), but we now log loudly and name
	// the registry so the failure is traceable. Callers on the
	// operator-facing dispatch path should prefer ResolveProfile, which
	// returns a real error instead of this zero value.
	if name != "" {
		log.Printf("[config] WARNING: agent_profile %q not found in agent_profiles (profiles.yaml) "+
			"and no \"default\" profile is configured; falling back to an empty profile. "+
			"Known agent_profiles: %s", name, profileNameList(profiles))
	}
	return AgentProfile{}
}

// UnknownProfileError reports an agent_profile name that did not resolve
// against the agent_profiles registry in profiles.yaml. It is
// deliberately specific about WHICH registry it consulted — EDGE 1 of
// CW-20260517-0011 is that three unrelated registries all look like "the
// profile", so an unqualified "profile not found" is unhelpful.
type UnknownProfileError struct {
	// Name is the agent_profile value that failed to resolve.
	Name string
	// Known is the sorted list of valid canonical agent_profiles names.
	Known []string
}

func (e *UnknownProfileError) Error() string {
	if len(e.Known) == 0 {
		return fmt.Sprintf("agent_profile %q not found: the agent_profiles registry "+
			"(profiles.yaml) is empty — add a profile, or set TORQUE_PROFILES_PATH", e.Name)
	}
	return fmt.Sprintf("agent_profile %q not found in the agent_profiles registry (profiles.yaml). "+
		"Valid agent_profiles: %s. "+
		"NOTE: this arg resolves ONLY against profiles.yaml agent_profiles — "+
		"it does NOT accept Tether catalog boot-profile ids (e.g. nanite.backend.main) "+
		"or catalog agent/launch names.",
		e.Name, strings.Join(e.Known, ", "))
}

// ResolveProfile looks up an agent_profile name and returns a real error
// when it misses, instead of GetProfileOrDefault's silent zero value.
// This is the resolver the operator-facing dispatch path (session
// launch, executor precheck) should use so a bad name fails fast with a
// registry-named, value-listing message.
//
// An empty name resolves to the "default" profile when one exists;
// substrate builtin profiles (reviewer-end-agent, planner, orchestrator)
// resolve the same way GetProfileOrDefault treats them.
func ResolveProfile(profiles ProfileMap, name string) (AgentProfile, error) {
	if name != "" {
		if p, ok := profiles[name]; ok {
			return p, nil
		}
		if p, ok := builtinProfiles[name]; ok {
			return p, nil
		}
		return AgentProfile{}, &UnknownProfileError{
			Name:  name,
			Known: ProfileNames(profiles),
		}
	}
	if p, ok := profiles["default"]; ok {
		return p, nil
	}
	return AgentProfile{}, &UnknownProfileError{Name: "default", Known: ProfileNames(profiles)}
}

// ProfileNames returns the sorted list of profile keys for error
// messages and listings.
func ProfileNames(profiles ProfileMap) []string {
	names := make([]string, 0, len(profiles))
	for k := range profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ValidateProfileName reports whether name exists in the loaded
// profiles.yaml agent_profiles registry. It intentionally ignores builtin
// substrate profiles: callers use this when a user names a profile from the
// external registry and should get a typo-focused error instead of silently
// falling through to a zero-value profile.
func ValidateProfileName(source ProfileSource, name string) error {
	profiles := CurrentProfiles(source)
	if name == "" {
		return nil
	}
	if _, ok := profiles[name]; ok {
		return nil
	}
	return fmt.Errorf(
		"unknown agent_profile '%s' in profiles.yaml agent_profiles registry — known: %v",
		name,
		ProfileNames(profiles),
	)
}

// profileNameList is the comma-joined form of ProfileNames, or a
// placeholder when the registry is empty.
func profileNameList(profiles ProfileMap) string {
	names := ProfileNames(profiles)
	if len(names) == 0 {
		return "(none configured)"
	}
	return strings.Join(names, ", ")
}

func cloneProfileMap(in ProfileMap) ProfileMap {
	if len(in) == 0 {
		return ProfileMap{}
	}
	out := make(ProfileMap, len(in))
	for name, profile := range in {
		out[name] = profile
	}
	return out
}

// builtinProfiles is the substrate's stock set of internal-task agent
// profiles. The lifecycle hooks stamp these names on tasks they create
// (kind=internal end-agents, future system / project-manager agents);
// without a fallback the picker's empty-profile guard would reject those
// tasks on every dispatch when a fresh install has no profiles.yaml.
//
// Add an entry here when shipping a new substrate-spawned internal-task
// role; users can still override by naming the same key in profiles.yaml.
// builtinProfiles defaults assume opencode provider — the open-question
// "what model id should the cost-backfill resolver see?" only matters when
// the provider is in the models.dev catalog (anthropic, openai, google,
// ...). opencode fronts multiple providers and the catalog has no
// `opencode` entry, so the backfill gate at cost.go:152 short-circuits on
// these regardless of whether Model is populated. Real user installs
// override these in profiles.yaml — those overrides set provider=claude
// AND model=claude-sonnet-4-5 (or similar), which IS in the catalog and
// hits the backfill. CW-20260510-0100 fixed the dogfood profiles.yaml;
// the builtin fallback stays opencode so a fresh-install user without a
// configured profiles.yaml still gets a working substrate-spawned agent
// (cost stays cost_source='unknown' for them, which is honest).
var builtinProfiles = ProfileMap{
	// Reviewer end-agent (CW-20260503-0019, S2.3, V1). Stamped on the
	// kind=internal task the lifecycle hook creates when a kind=agent
	// task transitions to `review`. cli + opencode mirrors the dogfood
	// default; users can override (different model, longer timeout) via
	// profiles.yaml.
	"reviewer-end-agent": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 600,
	},

	// Planner (CW-20260503-0020, S2.4, V0). Spawned by the Orchestrator
	// at plan-execute boot to refine a kind=plan task before phase
	// walk. Short timeout — V0 is a single read-then-write pass over
	// the plan + child tasks, no iterative loops.
	"planner": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 600,
	},

	// Orchestrator (CW-20260503-0018, S2.2, V0). Long-lived session
	// per plan — walks phases sequentially, dispatches children one at
	// a time, waits for the substrate-spawned reviewer to clear each.
	// Larger timeout because the session lifetime spans the entire
	// plan; cliexec / sessionmgr's own per-tick budget enforcement
	// applies inside.
	"orchestrator": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 1800,
	},
}
