package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/go-agent-launch/agentlaunch/launcher"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inlineCatalogYAML returns a self-contained launch-profile catalog YAML
// (an inline GlobalCatalog) for the given provider + runtime kind. The
// catalog carries exactly one launch entry plus the project / agent /
// provider entries it references — the standalone, Tether-not-required
// shape resolveLaunchProfile consumes.
func inlineCatalogYAML(provider, runtimeKind string) []byte {
	return []byte(fmt.Sprintf(`version: "0.1.0"
projects:
  - id: demo-project
    name: Demo Project
    repo_root: /tmp/demo-project
agents:
  - id: demo-agent
    name: Demo Agent
providers:
  - id: %s
    runtime_kind: %s
    command: %s
launches:
  - id: demo-launch
    project: demo-project
    agent: demo-agent
    provider: %s
    workspace:
      mode: persistent
`, provider, runtimeKind, provider, provider))
}

// launchPlanInputFor builds a buildLaunchPlanInput pre-populated with the
// Torque runtime-critical fields a real Boot call would supply, so the
// overlay assertions exercise the same surface buildLaunchPlanFromProfile
// sees in production.
func launchPlanInputFor(t *testing.T, torqueProvider string, rtKind RuntimeKind, src *launchProfileSource) buildLaunchPlanInput {
	t.Helper()
	return buildLaunchPlanInput{
		Profile:       config.AgentProfile{Executor: "cli", Provider: torqueProvider},
		AgentProfile:  "torque-backend",
		Role:          "executor",
		AgentFilePath: "/tmp/demo-project/agent.md",
		RuntimeKind:   rtKind,
		ProjectID:     "torque",
		Workdir:       "/tmp/demo-project",
		WorkspaceDir:  "/tmp/ws/demo",
		BuildDirRoot:  "/tmp/torque-boot",
		SystemPrompt:  "TORQUE-COMPOSED-SYSTEM-PROMPT",
		KickoffMD:     "TORQUE-KICKOFF-MD",
		LoopbackURL:   "http://127.0.0.1:54321/mcp",
		LaunchProfile: src,
	}
}

// TestResolveLaunchProfile_NoReference is the regression guard: when no
// launch profile is referenced, resolveLaunchProfile returns (nil, nil)
// and Boot stays on the pure-inline buildLaunchPlan path.
func TestResolveLaunchProfile_NoReference(t *testing.T) {
	src, err := resolveLaunchProfile(resolveLaunchProfileInput{})
	require.NoError(t, err)
	assert.Nil(t, src, "no launch-profile reference must yield a nil source (default inline path)")
}

// TestBuildLaunchPlan_NoLaunchProfile_MatchesInline is the regression
// guard: with LaunchProfile == nil, buildLaunchPlan produces exactly the
// inline plan — byte-identical to the pre-Stage-3 behavior.
func TestBuildLaunchPlan_NoLaunchProfile_MatchesInline(t *testing.T) {
	in := launchPlanInputFor(t, "claude", RuntimeKindSubprocess, nil)

	viaDispatch, err := buildLaunchPlan(in)
	require.NoError(t, err)

	viaInline, err := buildInlineLaunchPlan(in)
	require.NoError(t, err)

	assert.Equal(t, viaInline, viaDispatch,
		"buildLaunchPlan with LaunchProfile=nil must equal buildInlineLaunchPlan output")

	// Spot-check the inline-path invariants the launch-profile path must
	// not silently change when no profile is referenced.
	assert.Equal(t, "claude", viaDispatch.Provider.ID)
	assert.Equal(t, agentlaunch.RuntimeSubprocess, viaDispatch.Runtime)
	assert.Equal(t, agentlaunch.WorkspacePersistent, viaDispatch.Workspace.Mode)
	require.NotNil(t, viaDispatch.BootProfile.Inline)
	assert.Equal(t, "TORQUE-COMPOSED-SYSTEM-PROMPT", viaDispatch.BootProfile.Inline.BootPrompt)
	assert.Empty(t, viaDispatch.BootProfile.CatalogPath)
}

// TestLaunchProfile_ResolvesAndCompiles_AllProviders covers requirement
// (a): a task running through a launch profile resolves a valid plan for
// every torque-supported provider — claude-code, codex, AND opencode. It
// asserts on the resolved LaunchPlan and the CompiledLaunch (provider id,
// runtime, workspace) without spawning a real provider.
func TestLaunchProfile_ResolvesAndCompiles_AllProviders(t *testing.T) {
	cases := []struct {
		name           string
		providerID     string // launch-profile catalog provider id == resolved plan Provider.ID
		torqueProvider string // torque profile Provider field — claude-code normalizes to the `claude` brand
		runtimeKind    string
		torqueRT       RuntimeKind
		wantRuntime    agentlaunch.RuntimeKind
	}{
		{"claude-code", "claude", "claude-code", "streaming-stdio", RuntimeKindStreamingStdio, agentlaunch.RuntimeStreamingStdio},
		{"codex", "codex", "codex", "jsonrpc-stdio", RuntimeKindJsonRpcStdio, agentlaunch.RuntimeJsonRpcStdio},
		{"opencode", "opencode", "opencode", "subprocess", RuntimeKindSubprocess, agentlaunch.RuntimeSubprocess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Resolve the launch profile from an inline payload (the
			// standalone, no-catalog-file path).
			src, err := resolveLaunchProfile(resolveLaunchProfileInput{
				InlinePayload: inlineCatalogYAML(tc.providerID, tc.runtimeKind),
			})
			require.NoError(t, err)
			require.NotNil(t, src)
			assert.Equal(t, tc.providerID, src.BasePlan.Provider.ID,
				"base plan provider id comes from the launch profile")

			// Overlay Torque's runtime-critical fields.
			plan, err := buildLaunchPlan(launchPlanInputFor(t, tc.torqueProvider, tc.torqueRT, src))
			require.NoError(t, err)

			// Provider id + runtime resolved correctly.
			assert.Equal(t, tc.providerID, plan.Provider.ID)
			assert.Equal(t, tc.wantRuntime, plan.Runtime)

			// Workspace dirs are Torque's (overlay), mode is honored from
			// the profile.
			assert.Equal(t, agentlaunch.WorkspacePersistent, plan.Workspace.Mode)
			assert.Equal(t, "/tmp/demo-project", plan.Workspace.Workdir)
			assert.Equal(t, "/tmp/ws/demo", plan.Workspace.WorkspaceDir)
			assert.Equal(t, "/tmp/torque-boot", plan.Workspace.TempPrefix)

			// Boot prompt + loopback are always Torque's.
			require.NotNil(t, plan.BootProfile.Inline)
			assert.Equal(t, "TORQUE-COMPOSED-SYSTEM-PROMPT", plan.BootProfile.Inline.BootPrompt)
			assert.Equal(t, "TORQUE-KICKOFF-MD", plan.BootProfile.Inline.BootContent)
			assert.Equal(t, "http://127.0.0.1:54321/mcp", plan.MCP.LoopbackURL)

			// Project + agent identity are Torque's.
			assert.Equal(t, "torque", plan.Project.ID)
			assert.Equal(t, "torque-backend", plan.Agent.ID)
			assert.Equal(t, "true", plan.Metadata.Annotations["torque.launch_profile"])

			// The overlaid plan compiles cleanly through the shared
			// launcher (validates provider × runtime, resolves paths).
			compiled, err := launcher.Compile(context.Background(), plan)
			require.NoError(t, err)
			require.NotNil(t, compiled)
			require.NotNil(t, compiled.Plan)
			assert.Equal(t, tc.providerID, compiled.Plan.Provider.ID)
			assert.Equal(t, tc.wantRuntime, compiled.Plan.Runtime)
			assert.Equal(t, agentlaunch.WorkspacePersistent, compiled.Plan.Workspace.Mode)
		})
	}
}

// TestLaunchProfile_TorqueOverlayWinsOverProfile verifies the precedence
// rule: a launch profile contributes base values only — Torque's
// runtime-critical fields are always overlaid. A launch profile must not
// be able to break loopback auth, workspace ownership, or boot-prompt
// planting.
func TestLaunchProfile_TorqueOverlayWinsOverProfile(t *testing.T) {
	// A launch profile that tries to point the workspace + boot profile
	// somewhere of its own choosing.
	payload := []byte(`version: "0.1.0"
projects:
  - id: attacker-project
    name: Attacker Project
    repo_root: /attacker/repo
    workspace:
      session_root: /attacker/sessions
agents:
  - id: attacker-agent
    name: Attacker Agent
providers:
  - id: claude
    runtime_kind: subprocess
launches:
  - id: attacker-launch
    project: attacker-project
    agent: attacker-agent
    provider: claude
    boot_profile: attacker-boot-profile
    workspace:
      mode: shared
`)
	src, err := resolveLaunchProfile(resolveLaunchProfileInput{InlinePayload: payload})
	require.NoError(t, err)

	plan, err := buildLaunchPlan(launchPlanInputFor(t, "claude", RuntimeKindSubprocess, src))
	require.NoError(t, err)

	// Workspace ownership stays with Torque despite the profile's
	// session_root / repo_root.
	assert.Equal(t, "/tmp/demo-project", plan.Workspace.Workdir)
	assert.Equal(t, "/tmp/ws/demo", plan.Workspace.WorkspaceDir)
	assert.Equal(t, "/tmp/torque-boot", plan.Workspace.TempPrefix)
	assert.Equal(t, "/tmp/demo-project", plan.Project.Root)

	// Boot-prompt planting stays with Torque — the profile's boot_profile
	// reference is dropped, replaced by Torque's composed inline body.
	assert.Empty(t, plan.BootProfile.CatalogPath)
	assert.Empty(t, plan.BootProfile.Name)
	require.NotNil(t, plan.BootProfile.Inline)
	assert.Equal(t, "TORQUE-COMPOSED-SYSTEM-PROMPT", plan.BootProfile.Inline.BootPrompt)
	assert.Equal(t, agentlaunch.BootModePlanted, plan.BootProfile.Inline.BootMode)

	// Loopback auth stays with Torque.
	assert.Equal(t, "http://127.0.0.1:54321/mcp", plan.MCP.LoopbackURL)

	// Provider.Flags forced empty (Torque rebuilds argv per turn).
	assert.Empty(t, plan.Provider.Flags)
}

// TestLaunchProfile_PrecedenceOrder verifies the resolution precedence:
// InlinePayload > OptionsRef > ProfileRef.
func TestLaunchProfile_PrecedenceOrder(t *testing.T) {
	dir := t.TempDir()
	profileCatalog := filepath.Join(dir, "profile-catalog.yaml")
	optionsCatalog := filepath.Join(dir, "options-catalog.yaml")
	require.NoError(t, os.WriteFile(profileCatalog, inlineCatalogYAML("opencode", "subprocess"), 0o600))
	require.NoError(t, os.WriteFile(optionsCatalog, inlineCatalogYAML("codex", "jsonrpc-stdio"), 0o600))

	t.Run("inline payload wins over both refs", func(t *testing.T) {
		src, err := resolveLaunchProfile(resolveLaunchProfileInput{
			ProfileRef:    profileCatalog,
			OptionsRef:    optionsCatalog,
			InlinePayload: inlineCatalogYAML("claude", "subprocess"),
		})
		require.NoError(t, err)
		assert.Equal(t, "claude", src.BasePlan.Provider.ID)
	})

	t.Run("options ref wins over profile ref", func(t *testing.T) {
		src, err := resolveLaunchProfile(resolveLaunchProfileInput{
			ProfileRef: profileCatalog,
			OptionsRef: optionsCatalog,
		})
		require.NoError(t, err)
		assert.Equal(t, "codex", src.BasePlan.Provider.ID)
		assert.Equal(t, optionsCatalog, src.SourceCatalog)
	})

	t.Run("profile ref used when no options ref", func(t *testing.T) {
		src, err := resolveLaunchProfile(resolveLaunchProfileInput{
			ProfileRef: profileCatalog,
		})
		require.NoError(t, err)
		assert.Equal(t, "opencode", src.BasePlan.Provider.ID)
	})
}

// TestLaunchProfile_CatalogPathWithLaunchID exercises the "<path>#<id>"
// reference form against an on-disk catalog file.
func TestLaunchProfile_CatalogPathWithLaunchID(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.yaml")
	require.NoError(t, os.WriteFile(catalogPath, inlineCatalogYAML("claude", "subprocess"), 0o600))

	src, err := resolveLaunchProfile(resolveLaunchProfileInput{
		OptionsRef: catalogPath + "#demo-launch",
	})
	require.NoError(t, err)
	require.NotNil(t, src)
	assert.Equal(t, "claude", src.BasePlan.Provider.ID)
	assert.Equal(t, catalogPath, src.SourceCatalog)
}

// TestLaunchProfile_MalformedFailsCleanly covers requirement (c): a
// malformed / missing launch profile fails cleanly with a clear,
// errors.Is-matchable error — not a panic.
func TestLaunchProfile_MalformedFailsCleanly(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		_, err := resolveLaunchProfile(resolveLaunchProfileInput{
			OptionsRef: "/no/such/path/catalog.yaml",
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrLaunchProfile), "want ErrLaunchProfile, got %v", err)
	})

	t.Run("malformed yaml payload", func(t *testing.T) {
		_, err := resolveLaunchProfile(resolveLaunchProfileInput{
			InlinePayload: []byte("this: is: not: valid: yaml: ["),
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrLaunchProfile), "want ErrLaunchProfile, got %v", err)
	})

	t.Run("inline payload with no launch entries", func(t *testing.T) {
		_, err := resolveLaunchProfile(resolveLaunchProfileInput{
			InlinePayload: []byte("version: \"0.1.0\"\n"),
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrLaunchProfile))
		assert.Contains(t, err.Error(), "no launch entries")
	})

	t.Run("catalog with multiple launches and no launch-id suffix", func(t *testing.T) {
		dir := t.TempDir()
		multi := []byte(`version: "0.1.0"
projects:
  - id: p
    repo_root: /tmp/p
agents:
  - id: a
providers:
  - id: claude
    runtime_kind: subprocess
launches:
  - id: launch-one
    project: p
    agent: a
    provider: claude
  - id: launch-two
    project: p
    agent: a
    provider: claude
`)
		catalogPath := filepath.Join(dir, "multi.yaml")
		require.NoError(t, os.WriteFile(catalogPath, multi, 0o600))
		_, err := resolveLaunchProfile(resolveLaunchProfileInput{OptionsRef: catalogPath})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrLaunchProfile))
		assert.Contains(t, err.Error(), "#<launchID>")
	})

	t.Run("catalog-path with unknown launch id", func(t *testing.T) {
		dir := t.TempDir()
		catalogPath := filepath.Join(dir, "catalog.yaml")
		require.NoError(t, os.WriteFile(catalogPath, inlineCatalogYAML("claude", "subprocess"), 0o600))
		_, err := resolveLaunchProfile(resolveLaunchProfileInput{
			OptionsRef: catalogPath + "#does-not-exist",
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrLaunchProfile))
	})
}

// TestSplitLaunchRef covers the "<path>#<launchID>" splitter.
func TestSplitLaunchRef(t *testing.T) {
	cases := []struct {
		in       string
		wantPath string
		wantID   string
	}{
		{"/a/b/catalog.yaml", "/a/b/catalog.yaml", ""},
		{"/a/b/catalog.yaml#demo-launch", "/a/b/catalog.yaml", "demo-launch"},
		{"  /a/b#id  ", "/a/b", "id"},
		{"/a/b#", "/a/b", ""},
	}
	for _, tc := range cases {
		path, id := splitLaunchRef(tc.in)
		assert.Equal(t, tc.wantPath, path, "path for %q", tc.in)
		assert.Equal(t, tc.wantID, id, "id for %q", tc.in)
	}
}
