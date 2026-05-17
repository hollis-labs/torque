package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// ProfileLintProblem is one lint finding in a profiles.yaml file.
type ProfileLintProblem struct {
	Line    int
	Path    string
	Message string
}

func (p ProfileLintProblem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s: %s", p.Line, p.Path, p.Message)
	}
	return fmt.Sprintf("%s: %s", p.Path, p.Message)
}

type profileProviderSpec struct {
	NameTokens []string
	Executors  map[string]string
}

var profileProviderCatalog = map[string]profileProviderSpec{
	"anthropic": {
		NameTokens: []string{"anthropic"},
		Executors: map[string]string{
			"api": "",
		},
	},
	"claude": {
		NameTokens: []string{"claude"},
		Executors: map[string]string{
			"cli": "bare claude provider retired 2026-05-16; use provider=claude-code",
		},
	},
	"claude-code": {
		NameTokens: []string{"claude-code"},
		Executors: map[string]string{
			"cli": "",
		},
	},
	"codex": {
		NameTokens: []string{"codex"},
		Executors: map[string]string{
			"cli": "",
		},
	},
	"copilot": {
		NameTokens: []string{"copilot"},
		Executors: map[string]string{
			"cli": "copilot provider not supported (PTY adapter removed in go-providers v0.12.0)",
		},
	},
	"gemini": {
		NameTokens: []string{"gemini"},
		Executors: map[string]string{
			"api": "executor-api: gemini support deferred until a consumer profile lands",
			"cli": "gemini provider not supported (PTY adapter removed in go-providers v0.12.0)",
		},
	},
	"openai": {
		NameTokens: []string{"openai"},
		Executors: map[string]string{
			"api": "",
		},
	},
	"opencode": {
		NameTokens: []string{"opencode"},
		Executors: map[string]string{
			"cli": "",
		},
	},
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
	topAllowed := map[string]struct{}{"agent_profiles": {}}
	var profilesNode *yaml.Node
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
		if key == "agent_profiles" {
			profilesNode = valNode
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
	for i := 0; i < len(profilesNode.Content); i += 2 {
		nameNode := profilesNode.Content[i]
		bodyNode := profilesNode.Content[i+1]
		name := strings.TrimSpace(nameNode.Value)
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
