package permission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchGlob(t *testing.T) {
	assert.True(t, matchGlob("", "anything"))
	assert.True(t, matchGlob("*", "anything"))
	assert.True(t, matchGlob("bash", "bash"))
	assert.False(t, matchGlob("bash", "python"))
	assert.True(t, matchGlob("mcp__*", "mcp__dev__edit"))
	assert.False(t, matchGlob("mcp__*", "dev_edit"))
}

func TestRuleMatchesToolName(t *testing.T) {
	r := Rule{Tool: "bash", Behavior: DecisionAllow}
	assert.True(t, r.Matches("bash", nil))
	assert.False(t, r.Matches("python", nil))
}

func TestRuleMatchesToolGlob(t *testing.T) {
	r := Rule{Tool: "mcp__*", Behavior: DecisionAllow}
	assert.True(t, r.Matches("mcp__dev__edit", nil))
	assert.False(t, r.Matches("dev_edit", nil))
}

func TestRuleMatchesInputPath(t *testing.T) {
	r := Rule{Tool: "*", Pattern: "/tmp/**", Behavior: DecisionAllow}
	assert.True(t, r.Matches("bash", map[string]any{"path": "/tmp/foo.txt"}))
	assert.False(t, r.Matches("bash", map[string]any{"path": "/home/foo.txt"}))
}

func TestRuleMatchesInputCommand(t *testing.T) {
	r := Rule{Tool: "*", Pattern: "rm -rf", Behavior: DecisionDeny}
	assert.True(t, r.Matches("bash", map[string]any{"command": "rm -rf /tmp/foo"}))
	assert.False(t, r.Matches("bash", map[string]any{"command": "ls /tmp"}))
}

func TestRuleMatchesFilePath(t *testing.T) {
	r := Rule{Tool: "*", Pattern: "/src/**", Behavior: DecisionAllow}
	assert.True(t, r.Matches("edit", map[string]any{"file_path": "/src/main.go"}))
	assert.False(t, r.Matches("edit", map[string]any{"file_path": "/etc/passwd"}))
}

func TestRuleMatchesWildcard(t *testing.T) {
	r := Rule{Tool: "*", Behavior: DecisionAllow}
	assert.True(t, r.Matches("anything", nil))
	assert.True(t, r.Matches("bash", map[string]any{"path": "/foo"}))
}

func TestRuleMatchesEmptyTool(t *testing.T) {
	r := Rule{Tool: "", Behavior: DecisionAllow}
	assert.True(t, r.Matches("bash", nil))
	assert.True(t, r.Matches("anything", nil))
}

func TestRuleSetEvaluateDenyPriority(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "*", Behavior: DecisionAllow},
			{Tool: "*", Behavior: DecisionDeny},
		},
	}
	result := rs.Evaluate("bash", nil)
	require.NotNil(t, result)
	assert.Equal(t, DecisionDeny, result.Decision)
}

func TestRuleSetEvaluateAskBeforeAllow(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "*", Behavior: DecisionAllow},
			{Tool: "*", Behavior: DecisionAsk},
		},
	}
	result := rs.Evaluate("bash", nil)
	require.NotNil(t, result)
	assert.Equal(t, DecisionAsk, result.Decision)
}

func TestRuleSetEvaluateNoMatch(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "bash", Behavior: DecisionAllow},
		},
	}
	result := rs.Evaluate("python", nil)
	assert.Nil(t, result)
}

func TestRuleSetEvaluateEmpty(t *testing.T) {
	rs := &RuleSet{}
	result := rs.Evaluate("bash", nil)
	assert.Nil(t, result)
}

func TestLoadRulesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	content := `
permissions:
  mode: default
  rules:
    - tool: bash
      behavior: allow
    - tool: rm
      behavior: deny
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	rs, err := LoadRulesFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, ModeDefault, rs.Mode)
	assert.Len(t, rs.Rules, 2)
	assert.Equal(t, path, rs.Rules[0].Source)
	assert.Equal(t, path, rs.Rules[1].Source)
	assert.Equal(t, "bash", rs.Rules[0].Tool)
	assert.Equal(t, DecisionAllow, rs.Rules[0].Behavior)
	assert.Equal(t, "rm", rs.Rules[1].Tool)
	assert.Equal(t, DecisionDeny, rs.Rules[1].Behavior)
}

func TestLoadRulesFromFileNotFound(t *testing.T) {
	_, err := LoadRulesFromFile("/nonexistent/path/permissions.yaml")
	assert.Error(t, err)
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "permissions.yaml")

	rs := &RuleSet{
		Mode: ModeAcceptEdits,
		Rules: []Rule{
			{Tool: "bash", Pattern: "/tmp/**", Behavior: DecisionAllow},
			{Tool: "*", Behavior: DecisionAsk},
		},
	}

	require.NoError(t, SaveRulesToFile(path, rs))

	loaded, err := LoadRulesFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, ModeAcceptEdits, loaded.Mode)
	assert.Len(t, loaded.Rules, 2)
	assert.Equal(t, "bash", loaded.Rules[0].Tool)
	assert.Equal(t, "/tmp/**", loaded.Rules[0].Pattern)
	assert.Equal(t, DecisionAllow, loaded.Rules[0].Behavior)
	assert.Equal(t, DecisionAsk, loaded.Rules[1].Behavior)
}

func TestMergeRuleSets(t *testing.T) {
	a := &RuleSet{
		Mode:  ModeDefault,
		Rules: []Rule{{Tool: "bash", Behavior: DecisionAllow}},
	}
	b := &RuleSet{
		Mode:  ModeYolo,
		Rules: []Rule{{Tool: "rm", Behavior: DecisionDeny}},
	}
	merged := MergeRuleSets(a, b)
	// First non-empty mode wins.
	assert.Equal(t, ModeDefault, merged.Mode)
	assert.Len(t, merged.Rules, 2)
	assert.Equal(t, "bash", merged.Rules[0].Tool)
	assert.Equal(t, "rm", merged.Rules[1].Tool)
}

func TestMergeRuleSetsNilSafe(t *testing.T) {
	a := &RuleSet{
		Mode:  ModeDefault,
		Rules: []Rule{{Tool: "bash", Behavior: DecisionAllow}},
	}
	merged := MergeRuleSets(nil, a, nil)
	assert.Equal(t, ModeDefault, merged.Mode)
	assert.Len(t, merged.Rules, 1)

	// All nil.
	merged2 := MergeRuleSets(nil, nil)
	assert.NotNil(t, merged2)
	assert.Empty(t, merged2.Rules)
}
