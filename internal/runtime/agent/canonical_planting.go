package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	claudenative "github.com/hollis-labs/substrate/harness/adapters/claude/nativefiles"
	codexnative "github.com/hollis-labs/substrate/harness/adapters/codex/nativefiles"
	layout "github.com/hollis-labs/substrate/harness/adapters/layout/plan"
	native "github.com/hollis-labs/substrate/harness/adapters/nativefiles"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	"github.com/hollis-labs/substrate/harness/adapters/registry"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	providerplant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
	"github.com/hollis-labs/substrate/harness/interception/permission"
	"github.com/hollis-labs/substrate/harness/workspace/materialize"
	"github.com/hollis-labs/substrate/harness/workspace/materialize/artifact"
	"github.com/hollis-labs/substrate/harness/workspace/render"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
)

// plantCanonicalArtifacts retains the supported projection's launch bindings,
// permission posture and declared runtime effects. Its legacy Files are never
// applied: native serializers and the canonical renderer own artifact content.
// Credentials are prepared separately by the existing explicit execution edge.
func plantCanonicalArtifacts(ctx context.Context, prepared *agentlaunch.PreparedLaunch, adapter provider.BootDirProvider, authorize agentlaunch.ArtifactAuthorizer) (*agentlaunch.PreparedExecution, error) {
	execution, err := providerplant.ProjectExecution(ctx, prepared, providerplant.WithAdapter(adapter))
	if err != nil {
		return nil, err
	}
	plan := prepared.Compiled.Plan
	pc := providerplant.PlantContextFor(prepared)
	req := render.Request{Provider: runtimes.ID(plan.Provider.ID), Layer: layout.Boot, Mode: plan.Runtime, Agent: pc.AgentName,
		Roots: map[layout.Root]string{layout.RootBoot: prepared.PlantedBootDir, layout.RootProject: execution.Roots.ProjectRoot}}
	if req.Agent == "" {
		req.Agent = plan.Agent.ID
	}
	settingsField := layout.Settings
	if req.Provider == runtimes.Antigravity {
		settingsField = layout.PlantingPlugin
	}
	for _, field := range []layout.Field{layout.Instructions, layout.Kickoff, settingsField, layout.MCP} {
		resolved, err := layout.Resolve(layout.Request{Key: layout.Key{Provider: req.Provider, Layer: req.Layer, Mode: req.Mode, Field: field}, Requirement: layout.Required, Components: map[string]string{"agent": req.Agent}})
		if err != nil {
			return nil, err
		}
		content := render.Content{}
		switch field {
		case layout.Instructions:
			content.Body = []byte(pc.SystemPrompt)
		case layout.Kickoff:
			content.Body = []byte(pc.BootContent)
		}
		req.Inputs = append(req.Inputs, render.Input{Resolved: resolved, Content: content})
	}
	servers := slices.Clone(pc.MCPServers)
	if pc.MCPLoopbackURL != "" {
		servers = append(servers, provider.MCPServerSpec{Name: "loopback", HTTPURL: pc.MCPLoopbackURL})
	}
	if pc.MuxCommand != "" {
		servers = append(servers, provider.MCPServerSpec{Name: "mux", Command: pc.MuxCommand, Args: slices.Clone(pc.MuxArgs), Env: slices.Clone(pc.MuxEnv)})
	}
	for _, server := range servers {
		binding := native.Server{Name: server.Name, HTTPURL: server.HTTPURL, Command: server.Command, Args: slices.Clone(server.Args)}
		for _, variable := range server.Env {
			key, value, ok := strings.Cut(variable, "=")
			if !ok {
				return nil, fmt.Errorf("invalid MCP environment binding")
			}
			binding.Env = append(binding.Env, native.Variable{Name: key, Value: value})
		}
		req.Native.Servers = append(req.Native.Servers, binding)
	}
	switch a := adapter.(type) {
	case *provider.ClaudeAdapter:
		doc, err := a.SettingsDocument()
		if err != nil {
			return nil, err
		}
		req.Native.Claude = claudenative.SettingsInput{}
		for key, value := range doc {
			if key == "permissions" && plan.Provider.Permission != "" {
				continue
			} // bound posture owns the policy cell
			req.Native.Claude.Slots = append(req.Native.Claude.Slots, native.Slot{Key: key, Value: value})
		}
	case *provider.CodexAdapter:
		// Torque has always used the adapter's explicit headless defaults; it does
		// not bind the Claude-only launch posture onto Codex or grant new authority.
		approval, sandbox := a.ApprovalPolicy, a.SandboxMode
		if approval == "" {
			approval = "never"
		}
		if sandbox == "" {
			sandbox = codexDefaultSandboxMode
		}
		req.Native.Codex = codexnative.ConfigInput{ApprovalPolicy: approval, SandboxMode: sandbox, WritableRoots: slices.Clone(a.WritableRoots)}
	case *provider.OpencodeAdapter, *provider.AntigravityAdapter:
		// Existing MCP and runtime permission bindings remain explicit.
	default:
		return nil, fmt.Errorf("adapter lacks canonical native settings mapping")
	}
	if plan.Provider.Permission != "" {
		resolved, err := layout.Resolve(layout.Request{Key: layout.Key{Provider: req.Provider, Layer: req.Layer, Mode: req.Mode, Field: layout.Permissions}, Requirement: layout.Required, Posture: plan.Provider.Permission,
			LookupPosture: func(id runtimes.ID, posture permission.Mode, mode runtimes.Mode) error {
				desc, ok := registry.Lookup(string(id))
				if !ok {
					return fmt.Errorf("unknown provider posture")
				}
				_, err := desc.PostureFor(posture, mode)
				return err
			}})
		if err != nil {
			return nil, err
		}
		req.Inputs = append(req.Inputs, render.Input{Resolved: resolved})
	}
	for _, file := range plan.Injection.NativeFiles {
		path := file.RelPath
		if file.Kind == agentlaunch.NativeFileSkill {
			path, err = agentlaunch.SkillRelPath(plan.Provider.ID, plan.Runtime, file.ID)
			if err != nil {
				return nil, err
			}
		}
		req.Overlays = append(req.Overlays, artifact.Entry{Path: path, Kind: artifact.EntryFile, Mode: file.Mode, Bytes: []byte(file.Content)})
	}
	for path, body := range plan.Injection.BootDirOverlay {
		req.Overlays = append(req.Overlays, artifact.Entry{Path: path, Kind: artifact.EntryFile, Mode: 0644, Bytes: []byte(body)})
	}
	rendered, err := render.Render(req)
	if err != nil {
		return nil, err
	}
	execution.Artifacts = rendered.Tree
	execution.Bindings.CWD = rendered.Binding.CWD
	execution.Roots.CWD = rendered.Binding.CWD
	for key, value := range rendered.Binding.Environment {
		execution.Bindings.Env[key] = agentlaunch.EnvVar{Value: value, Source: "canonical-render", Precedence: 20}
	}
	handle, err := agentlaunch.MaterializeArtifacts(ctx, agentlaunch.ArtifactMaterializationRequest{
		TargetRoot: execution.Roots.BootRoot, Roots: execution.Roots, Artifacts: execution.Artifacts,
		Operation: materialize.OperationReconcile, Generation: prepared.Compiled.Provenance.PlanHash,
		Authorize: authorize, Reconcile: materialize.ReconcilePolicy{Conflict: materialize.ConflictReport},
	})
	if err != nil {
		return nil, err
	}
	execution.Materialization = handle
	return execution, execution.Validate()
}
