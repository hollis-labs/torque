package config

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/hollis-labs/substrate/harness/adapters/launch"
	"github.com/hollis-labs/substrate/harness/adapters/registry"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/torque/internal/runtimetoken"
)

// ProfileLintProblem is one lint finding in a profiles.yaml file.
type ProfileLintProblem struct {
	Line    int
	Path    string
	Message string
	// Warning marks a finding that is not an error: the profile loads and
	// runs, and the operator is told what it grants. `torque profiles lint`
	// prints it and does not fail on it.
	Warning bool
}

func (p ProfileLintProblem) String() string {
	prefix := ""
	if p.Warning {
		prefix = "warning: "
	}
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s: %s%s", p.Line, p.Path, prefix, p.Message)
	}
	return fmt.Sprintf("%s: %s%s", p.Path, prefix, p.Message)
}

type profileProviderSpec struct {
	NameTokens []string
	Executors  map[string]string
}

// apiProviderCatalog lists the executor-api vendors. They are API clients,
// not agent CLI runtimes, so they stay Torque's own list.
var apiProviderCatalog = map[string]profileProviderSpec{
	"anthropic": {
		NameTokens: []string{"anthropic"},
		Executors:  map[string]string{"api": ""},
	},
	"gemini": {
		NameTokens: []string{"gemini"},
		Executors:  map[string]string{"api": "executor-api: gemini support deferred until a consumer profile lands"},
	},
	"openai": {
		NameTokens: []string{"openai"},
		Executors:  map[string]string{"api": ""},
	},
}

// LaunchableProviders lists the cli provider names agent.Boot launches:
// every go-providers registry id and alias whose runtime has a mode
// go-agent-wrapper's launch.Select drives (CW-20260930-0134), native or ACP
// (CW-20261001-0097), except the retired bare `claude`.
func LaunchableProviders() []string {
	launchable := map[runtimes.ID]bool{}
	for _, k := range launch.Supported() {
		launchable[k.Runtime] = true
	}
	var out []string
	for _, d := range registry.All() {
		if !launchable[d.ID] {
			continue
		}
		for _, name := range append([]string{string(d.ID)}, d.Aliases...) {
			if name != "claude" {
				out = append(out, name)
			}
		}
	}
	return out
}

// profileProviderCatalog is the executor-api vendors plus, for executor
// cli, every id and alias in the go-providers runtime registry
// (CW-20261001-0064), so a cli provider the registry does not know is an
// unknown provider rather than an entry in a Torque list.
var profileProviderCatalog = buildProfileProviderCatalog()

func buildProfileProviderCatalog() map[string]profileProviderSpec {
	out := make(map[string]profileProviderSpec, len(apiProviderCatalog))
	for name, spec := range apiProviderCatalog {
		out[name] = spec
	}
	for _, desc := range registry.All() {
		for _, name := range append([]string{string(desc.ID)}, desc.Aliases...) {
			spec := out[name]
			spec.NameTokens = []string{name}
			executors := map[string]string{"cli": cliLaunchReason(name, desc)}
			for executor, reason := range spec.Executors {
				executors[executor] = reason
			}
			spec.Executors = executors
			out[name] = spec
		}
	}
	return out
}

// cliLaunchReason is "" when Torque launches the named runtime, else why not.
func cliLaunchReason(name string, desc registry.Descriptor) string {
	switch {
	case slices.Contains(LaunchableProviders(), name):
		return ""
	case name == "claude":
		return "bare claude provider retired 2026-05-16; use provider=claude-code"
	default:
		return fmt.Sprintf("Torque has no launch path for %s's modes", desc.ID)
	}
}

var profileNameRoleExemptions = map[string]struct{}{
	"default":            {},
	"implementer":        {},
	"orchestrator":       {},
	"planner":            {},
	"release-engineer":   {},
	"reviewer-end-agent": {},
	"worker":             {},
}

// LintProfilesFile validates profiles.yaml as an execution-template registry:
// fields must match the schema, providers must exist for the configured
// executor, and non-generic profile names must say which provider they target.
func LintProfilesFile(path string) ([]ProfileLintProblem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profiles %s: %w", path, err)
	}
	return LintProfilesYAML(data)
}

// LintProfilesYAML validates the raw YAML contents of a profiles file.
func LintProfilesYAML(data []byte) ([]ProfileLintProblem, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return []ProfileLintProblem{{
			Line:    doc.Line,
			Path:    "profiles",
			Message: "top level must be a mapping",
		}}, nil
	}

	problems := make([]ProfileLintProblem, 0)
	topAllowed := map[string]struct{}{"agent_profiles": {}, "agent_profile_aliases": {}}
	var profilesNode *yaml.Node
	var aliasesNode *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		keyNode := doc.Content[i]
		valNode := doc.Content[i+1]
		key := strings.TrimSpace(keyNode.Value)
		if _, ok := topAllowed[key]; !ok {
			problems = append(problems, ProfileLintProblem{
				Line:    keyNode.Line,
				Path:    key,
				Message: "unknown top-level field",
			})
			continue
		}
		switch key {
		case "agent_profiles":
			profilesNode = valNode
		case "agent_profile_aliases":
			aliasesNode = valNode
		}
	}
	if profilesNode == nil {
		return problems, nil
	}
	if profilesNode.Kind != yaml.MappingNode {
		problems = append(problems, ProfileLintProblem{
			Line:    profilesNode.Line,
			Path:    "agent_profiles",
			Message: "must be a mapping of profile names to profile definitions",
		})
		return problems, nil
	}

	allowedFields := allowedAgentProfileFields()
	profileNames := make(map[string]struct{})
	for i := 0; i < len(profilesNode.Content); i += 2 {
		nameNode := profilesNode.Content[i]
		bodyNode := profilesNode.Content[i+1]
		name := strings.TrimSpace(nameNode.Value)
		profileNames[name] = struct{}{}
		basePath := "agent_profiles." + name

		if bodyNode.Kind != yaml.MappingNode {
			problems = append(problems, ProfileLintProblem{
				Line:    bodyNode.Line,
				Path:    basePath,
				Message: "profile definition must be a mapping",
			})
			continue
		}

		for j := 0; j < len(bodyNode.Content); j += 2 {
			fieldNode := bodyNode.Content[j]
			field := strings.TrimSpace(fieldNode.Value)
			if _, ok := allowedFields[field]; !ok {
				problems = append(problems, ProfileLintProblem{
					Line:    fieldNode.Line,
					Path:    basePath + "." + field,
					Message: "unknown field",
				})
			}
		}

		var profile AgentProfile
		if err := bodyNode.Decode(&profile); err != nil {
			problems = append(problems, ProfileLintProblem{
				Line:    bodyNode.Line,
				Path:    basePath,
				Message: err.Error(),
			})
			continue
		}
		problems = append(problems, lintProfileDefinition(nameNode.Line, name, profile)...)
	}

	// agent_profile_aliases (EDGE 3, CW-20260517-0011): each alias must
	// map to a defined agent_profiles entry and must not shadow one.
	if aliasesNode != nil {
		if aliasesNode.Kind != yaml.MappingNode {
			problems = append(problems, ProfileLintProblem{
				Line:    aliasesNode.Line,
				Path:    "agent_profile_aliases",
				Message: "must be a mapping of alias names to agent_profiles keys",
			})
		} else {
			for i := 0; i < len(aliasesNode.Content); i += 2 {
				aliasKeyNode := aliasesNode.Content[i]
				aliasValNode := aliasesNode.Content[i+1]
				alias := strings.TrimSpace(aliasKeyNode.Value)
				target := strings.TrimSpace(aliasValNode.Value)
				path := "agent_profile_aliases." + alias
				if _, shadow := profileNames[alias]; shadow {
					problems = append(problems, ProfileLintProblem{
						Line:    aliasKeyNode.Line,
						Path:    path,
						Message: "alias name collides with an agent_profiles entry",
					})
				}
				if _, ok := profileNames[target]; !ok {
					problems = append(problems, ProfileLintProblem{
						Line:    aliasValNode.Line,
						Path:    path,
						Message: fmt.Sprintf("alias target %q is not a defined agent_profiles entry", target),
					})
				}
			}
		}
	}

	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Line == problems[j].Line {
			return problems[i].Path < problems[j].Path
		}
		return problems[i].Line < problems[j].Line
	})
	return problems, nil
}

func lintProfileDefinition(line int, name string, profile AgentProfile) []ProfileLintProblem {
	basePath := "agent_profiles." + name
	problems := make([]ProfileLintProblem, 0)

	executor := strings.ToLower(strings.TrimSpace(profile.Executor))
	provider := strings.ToLower(strings.TrimSpace(profile.Provider))

	if executor == "" {
		problems = append(problems, ProfileLintProblem{
			Line:    line,
			Path:    basePath + ".executor",
			Message: "missing executor",
		})
	} else if executor != "api" && executor != "cli" {
		problems = append(problems, ProfileLintProblem{
			Line:    line,
			Path:    basePath + ".executor",
			Message: fmt.Sprintf("unknown executor %q (supported: api, cli)", profile.Executor),
		})
	}

	if provider == "" {
		problems = append(problems, ProfileLintProblem{
			Line:    line,
			Path:    basePath + ".provider",
			Message: "missing provider",
		})
	} else if spec, ok := profileProviderCatalog[provider]; !ok {
		problems = append(problems, ProfileLintProblem{
			Line:    line,
			Path:    basePath + ".provider",
			Message: fmt.Sprintf("unknown provider %q", profile.Provider),
		})
	} else if executor != "" {
		reason, ok := spec.Executors[executor]
		switch {
		case !ok:
			problems = append(problems, ProfileLintProblem{
				Line:    line,
				Path:    basePath + ".provider",
				Message: fmt.Sprintf("provider %q is not available for executor %q (supported: %s)", profile.Provider, profile.Executor, supportedExecutorsForProvider(provider)),
			})
		case reason != "":
			problems = append(problems, ProfileLintProblem{
				Line:    line,
				Path:    basePath + ".provider",
				Message: fmt.Sprintf("provider %q is not executable for executor %q: %s", profile.Provider, profile.Executor, reason),
			})
		}
	}

	if err := validateProfileArgs(name, profile.Args); err != nil {
		problems = append(problems, ProfileLintProblem{
			Line:    line,
			Path:    basePath + ".args",
			Message: err.Error(),
		})
	}

	problems = append(problems, lintMuxServers(line, basePath, profile)...)
	problems = append(problems, lintClaudeACP(line, basePath, profile)...)

	if prob, ok := dishonestProfileNameProblem(line, name, provider); ok {
		problems = append(problems, prob)
	}
	return problems
}

func dishonestProfileNameProblem(line int, name, provider string) (ProfileLintProblem, bool) {
	if _, ok := profileNameRoleExemptions[name]; ok {
		return ProfileLintProblem{}, false
	}
	if provider == "" {
		return ProfileLintProblem{}, false
	}
	spec, ok := profileProviderCatalog[provider]
	if !ok || len(spec.NameTokens) == 0 {
		return ProfileLintProblem{}, false
	}

	matched := make([]string, 0)
	for token := range allProviderNameTokens() {
		if nameHasProviderToken(name, token) {
			matched = append(matched, token)
		}
	}
	sort.Strings(matched)

	expectedTokens := spec.NameTokens
	for _, token := range matched {
		for _, want := range expectedTokens {
			if token == want {
				return ProfileLintProblem{}, false
			}
		}
	}

	if len(matched) > 0 {
		return ProfileLintProblem{
			Line:    line,
			Path:    "agent_profiles." + name,
			Message: fmt.Sprintf("dishonest profile name: suggests provider %q but config provider is %q", strings.Join(matched, ", "), provider),
		}, true
	}

	suggested := name + "-" + expectedTokens[0]
	return ProfileLintProblem{
		Line:    line,
		Path:    "agent_profiles." + name,
		Message: fmt.Sprintf("dishonest profile name: missing provider binding for %q; rename to include %q (for example %q)", provider, expectedTokens[0], suggested),
	}, true
}

func allProviderNameTokens() map[string]struct{} {
	out := make(map[string]struct{})
	for _, spec := range profileProviderCatalog {
		for _, token := range spec.NameTokens {
			out[token] = struct{}{}
		}
	}
	return out
}

func supportedExecutorsForProvider(provider string) string {
	spec, ok := profileProviderCatalog[provider]
	if !ok {
		return ""
	}
	allowed := make([]string, 0, len(spec.Executors))
	for executor, reason := range spec.Executors {
		if reason == "" {
			allowed = append(allowed, executor)
		}
	}
	sort.Strings(allowed)
	if len(allowed) == 0 {
		return "none"
	}
	return strings.Join(allowed, ", ")
}

func nameHasProviderToken(name, token string) bool {
	for start := 0; start < len(name); {
		idx := strings.Index(name[start:], token)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || !isProfileNameRune(rune(name[idx-1]))
		afterIdx := idx + len(token)
		afterOK := afterIdx == len(name) || !isProfileNameRune(rune(name[afterIdx]))
		if beforeOK && afterOK {
			return true
		}
		start = idx + 1
	}
	return false
}

func isProfileNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func allowedAgentProfileFields() map[string]struct{} {
	out := make(map[string]struct{})
	typ := reflect.TypeOf(AgentProfile{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// lintMuxServers checks a profile's mux_servers (CW-20261001-0226): each name
// must be a known mux server (an error), cerberus is warned about because it
// grants deploy and ssh, and a Codex or ACP profile is told the grant takes
// effect only under bypassPermissions.
func lintMuxServers(line int, basePath string, profile AgentProfile) []ProfileLintProblem {
	if len(profile.MuxServers) == 0 {
		return nil
	}
	path := basePath + ".mux_servers"
	var problems []ProfileLintProblem
	for _, msg := range validateMuxServers(profile.MuxServers) {
		problems = append(problems, ProfileLintProblem{Line: line, Path: path, Message: msg})
	}
	for _, g := range profile.DangerousMuxGrants() {
		problems = append(problems, ProfileLintProblem{
			Line: line, Path: path, Warning: true,
			Message: fmt.Sprintf("%s grants %s", g.Server, g.Grants),
		})
	}
	if muxNeedsBypass(profile) && PermissionMode(profile.PermissionMode) != PermissionModeBypass {
		problems = append(problems, ProfileLintProblem{
			Line: line, Path: path, Warning: true,
			Message: fmt.Sprintf("%s sessions get mux only under permission_mode %s, so these servers are not planted for permission_mode %q", profile.Provider, PermissionModeBypass, profile.ResolvedPermissionMode()),
		})
	}
	return problems
}

// muxNeedsBypass reports whether the profile's runtime gets the mux server
// only under bypassPermissions: Codex, and every ACP runtime, where Torque
// cannot gate mux's tools by posture (agent.plantsMux).
func muxNeedsBypass(profile AgentProfile) bool {
	desc, ok := registry.Lookup(profile.Provider)
	if !ok {
		return false
	}
	if desc.ID == runtimes.Codex {
		return true
	}
	mode := desc.DefaultMode
	if tok, err := runtimetoken.NormalizeProfile(profile.RuntimeKind); err == nil && tok.Mode != "" {
		mode = tok.Mode
	}
	return mode.ACP()
}

// lintClaudeACP warns about a Claude profile on an ACP runtime kind
// (CW-20261001-0226). Native Claude is launched with --strict-mcp-config, so
// it loads only the MCP servers Torque plants; over ACP Claude runs behind a
// third-party bridge that takes no such flag, so Torque cannot confirm that
// it loads only the servers it sends in session/new.
func lintClaudeACP(line int, basePath string, profile AgentProfile) []ProfileLintProblem {
	desc, ok := registry.Lookup(profile.Provider)
	if !ok || desc.ID != runtimes.Claude {
		return nil
	}
	tok, err := runtimetoken.NormalizeProfile(profile.RuntimeKind)
	if err != nil || tok.Mode == "" || !tok.Mode.ACP() {
		return nil
	}
	return []ProfileLintProblem{{
		Line: line, Path: basePath + ".runtime_kind", Warning: true,
		Message: fmt.Sprintf("%s over %s cannot be launched with --strict-mcp-config (the ACP bridge takes no such flag), so Torque cannot confirm it loads only the MCP servers it plants; native claude-code (streaming-stdio) does", profile.Provider, tok.Mode),
	}}
}
