package permission

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decision represents the outcome of a permission check.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionAsk   Decision = "ask"
)

// Mode represents the permission mode the engine operates in.
type Mode string

const (
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "accept-edits"
	ModePlan        Mode = "plan"
	ModeYolo        Mode = "yolo"
)

// Rule defines a single permission rule.
type Rule struct {
	Tool     string   `yaml:"tool"`
	Pattern  string   `yaml:"pattern"`
	Behavior Decision `yaml:"behavior"`
	Source   string   `yaml:"-"`
}

// RuleSet is a collection of rules with an associated mode.
type RuleSet struct {
	Mode  Mode   `yaml:"mode"`
	Rules []Rule `yaml:"rules"`
}

// PermissionsFile is the top-level YAML structure.
type PermissionsFile struct {
	Permissions RuleSet `yaml:"permissions"`
}

// CheckResult holds the outcome of a permission check.
type CheckResult struct {
	Decision    Decision
	RequestID   string
	MatchedRule *Rule
	Reason      string
}

// Matches returns true if the rule applies to the given tool name and input.
func (r *Rule) Matches(toolName string, input map[string]any) bool {
	if !matchGlob(r.Tool, toolName) {
		return false
	}
	if r.Pattern == "" {
		return true
	}
	return matchInputPattern(r.Pattern, input)
}

// Evaluate checks all rules and returns the highest-priority matching result.
// Priority order: deny > ask > allow. Returns nil if no rule matches.
func (rs *RuleSet) Evaluate(toolName string, input map[string]any) *CheckResult {
	var denyResult, askResult, allowResult *CheckResult

	for i := range rs.Rules {
		rule := &rs.Rules[i]
		if !rule.Matches(toolName, input) {
			continue
		}
		result := &CheckResult{
			Decision:    rule.Behavior,
			MatchedRule: rule,
			Reason:      "matched rule: " + rule.Tool,
		}
		switch rule.Behavior {
		case DecisionDeny:
			if denyResult == nil {
				denyResult = result
			}
		case DecisionAsk:
			if askResult == nil {
				askResult = result
			}
		case DecisionAllow:
			if allowResult == nil {
				allowResult = result
			}
		}
	}

	if denyResult != nil {
		return denyResult
	}
	if askResult != nil {
		return askResult
	}
	return allowResult
}

// matchGlob matches a tool name against a pattern using filepath.Match.
// Empty string or "*" matches everything.
func matchGlob(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// matchInputPattern checks a pattern against common input fields.
func matchInputPattern(pattern string, input map[string]any) bool {
	// Check path fields with glob matching.
	pathFields := []string{"path", "file", "directory", "file_path"}
	for _, field := range pathFields {
		if val, ok := input[field]; ok {
			if s, ok := val.(string); ok {
				if matchPathGlob(pattern, s) {
					return true
				}
			}
		}
	}
	// Check command field with string contains.
	if val, ok := input["command"]; ok {
		if s, ok := val.(string); ok {
			if strings.Contains(s, pattern) {
				return true
			}
		}
	}
	return false
}

// matchPathGlob matches a path against a glob pattern, including /** prefix matching.
func matchPathGlob(pattern, path string) bool {
	matched, err := filepath.Match(pattern, path)
	if err == nil && matched {
		return true
	}
	// Support /** suffix: if pattern ends with /**, check if path starts with the prefix.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if strings.HasPrefix(path, prefix+"/") || path == prefix {
			return true
		}
	}
	// Support ** prefix: try matching the base.
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		base := filepath.Base(path)
		matched, err = filepath.Match(suffix, base)
		if err == nil && matched {
			return true
		}
		// Also try matching any path component.
		matched, err = filepath.Match(suffix, path)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// LoadRulesFromFile reads a YAML permissions file and returns the RuleSet.
// Each rule is tagged with the file path as its Source.
func LoadRulesFromFile(path string) (*RuleSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf PermissionsFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, err
	}
	rs := pf.Permissions
	for i := range rs.Rules {
		rs.Rules[i].Source = path
	}
	return &rs, nil
}

// SaveRulesToFile writes a RuleSet to a YAML file, creating directories as needed.
func SaveRulesToFile(path string, rs *RuleSet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	pf := PermissionsFile{Permissions: *rs}
	data, err := yaml.Marshal(&pf)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// MergeRuleSets merges multiple RuleSets into one.
// The first non-empty mode wins; all rules are appended in order.
// Nil sets are safely skipped.
func MergeRuleSets(sets ...*RuleSet) *RuleSet {
	result := &RuleSet{}
	for _, rs := range sets {
		if rs == nil {
			continue
		}
		if result.Mode == "" && rs.Mode != "" {
			result.Mode = rs.Mode
		}
		result.Rules = append(result.Rules, rs.Rules...)
	}
	return result
}
