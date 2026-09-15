package mcpadapter_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateBudgetArgs_RegisteredInCreateAndUpdateSchemas(t *testing.T) {
	a := setupAdapter(t)

	for _, tool := range []string{"torque_template_create", "torque_template_update"} {
		props := listToolSchemaProperties(t, a, tool)
		for _, name := range []string{"cost_budget", "max_retries", "max_duration_ms", "token_budget"} {
			_, ok := props[name]
			assert.True(t, ok, "%s should declare %s", tool, name)
		}
	}
}

func TestTemplateBudgetArgs_MCPRoundTripAndInstantiate(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_template_create", map[string]interface{}{
		"id":              "budgeted",
		"name":            "budgeted",
		"description":     "x",
		"kind":            "agent",
		"executor":        "cli",
		"cost_budget":     "0",
		"max_retries":     "0",
		"max_duration_ms": "-1",
		"token_budget":    "1200",
	})
	require.False(t, isErr, "create should not error: %s", text)
	assertTemplateBudgets(t, text, 0, 0, -1, 1200)

	text, isErr = callTool(t, a, "torque_template_update", map[string]interface{}{
		"id":   "budgeted",
		"name": "budgeted v2",
	})
	require.False(t, isErr, "budget-omitting update should not error: %s", text)
	assertTemplateVersionAndBudgets(t, text, 2, 0, 0, -1, 1200)

	text, isErr = callTool(t, a, "torque_task_create_from_template", map[string]interface{}{
		"template_id":      "budgeted",
		"template_version": "1",
		"title":            "from budgeted v1",
	})
	require.False(t, isErr, "versioned instantiate should not error: %s", text)
	assertTaskBudgets(t, text, 0, 0, -1, 1200)

	text, isErr = callTool(t, a, "torque_task_create_from_template", map[string]interface{}{
		"template_id": "budgeted",
		"title":       "from inherited latest",
	})
	require.False(t, isErr, "latest instantiate should not error: %s", text)
	assertTaskBudgets(t, text, 0, 0, -1, 1200)

	text, isErr = callTool(t, a, "torque_template_update", map[string]interface{}{
		"id":              "budgeted",
		"cost_budget":     "-1",
		"max_retries":     "2",
		"max_duration_ms": "5000",
		"token_budget":    "-1",
	})
	require.False(t, isErr, "update should not error: %s", text)
	assertTemplateVersionAndBudgets(t, text, 3, -1, 2, 5000, -1)

	text, isErr = callTool(t, a, "torque_task_create_from_template", map[string]interface{}{
		"template_id": "budgeted",
		"title":       "from budgeted",
	})
	require.False(t, isErr, "instantiate should not error: %s", text)
	assertTaskBudgets(t, text, -1, 2, 5000, -1)

	text, isErr = callTool(t, a, "torque_template_update", map[string]interface{}{
		"id":          "budgeted",
		"max_retries": "-1",
	})
	require.True(t, isErr, "negative update should fail: %s", text)

	text, isErr = callTool(t, a, "torque_template_update", map[string]interface{}{
		"id":           "budgeted",
		"token_budget": "1.5",
	})
	require.True(t, isErr, "fractional update should fail: %s", text)

	text, isErr = callTool(t, a, "torque_template_get", map[string]interface{}{
		"id": "budgeted",
	})
	require.False(t, isErr, "latest get should not error: %s", text)
	assertTemplateVersionAndBudgets(t, text, 3, -1, 2, 5000, -1)
}

func TestTemplateBudgetArgs_OmittedRetriesDefaultToThree(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_template_create", map[string]interface{}{
		"id":          "defaulted",
		"name":        "defaulted",
		"description": "x",
		"kind":        "agent",
		"executor":    "cli",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var tpl map[string]interface{}
	parseData(t, text, &tpl)
	assert.Equal(t, float64(3), tpl["MaxRetries"])
	cost := tpl["CostBudget"].(map[string]interface{})
	assert.Equal(t, false, cost["Valid"])
	duration := tpl["MaxDurationMs"].(map[string]interface{})
	assert.Equal(t, false, duration["Valid"])
	tokens := tpl["TokenBudget"].(map[string]interface{})
	assert.Equal(t, false, tokens["Valid"])
}

func TestTemplateBudgetArgs_StrictMCPParsing(t *testing.T) {
	a := setupAdapter(t)

	for _, tc := range []struct {
		name  string
		args  map[string]interface{}
		field string
	}{
		{
			name: "fractional retries", field: "max_retries",
			args: map[string]interface{}{"max_retries": "1.5"},
		},
		{
			name: "fractional duration", field: "max_duration_ms",
			args: map[string]interface{}{"max_duration_ms": "1.5"},
		},
		{
			name: "overflow tokens", field: "token_budget",
			args: map[string]interface{}{"token_budget": "9223372036854775808"},
		},
		{
			name: "malformed cost", field: "cost_budget",
			args: map[string]interface{}{"cost_budget": "lots"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{
				"id":          "bad-" + strings.ReplaceAll(tc.name, " ", "-"),
				"name":        "bad",
				"description": "x",
				"kind":        "agent",
				"executor":    "cli",
			}
			for k, v := range tc.args {
				args[k] = v
			}

			text, isErr := callTool(t, a, "torque_template_create", args)
			require.True(t, isErr, "create should reject invalid input: %s", text)
			code, _, field := parseError(t, text)
			assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
			assert.Equal(t, tc.field, field)
		})
	}
}

func assertTemplateBudgets(t *testing.T, text string, cost float64, retries int, duration int64, tokens int64) {
	t.Helper()
	assertTemplateVersionAndBudgets(t, text, 0, cost, retries, duration, tokens)
}

func assertTemplateVersionAndBudgets(t *testing.T, text string, version int, cost float64, retries int, duration int64, tokens int64) {
	t.Helper()
	var tpl map[string]interface{}
	parseData(t, text, &tpl)
	if version != 0 {
		assert.Equal(t, float64(version), tpl["Version"])
	}
	costBudget := tpl["CostBudget"].(map[string]interface{})
	assert.Equal(t, true, costBudget["Valid"])
	assert.Equal(t, cost, costBudget["Float64"])
	assert.Equal(t, float64(retries), tpl["MaxRetries"])
	maxDuration := tpl["MaxDurationMs"].(map[string]interface{})
	assert.Equal(t, true, maxDuration["Valid"])
	assert.Equal(t, float64(duration), maxDuration["Int64"])
	tokenBudget := tpl["TokenBudget"].(map[string]interface{})
	assert.Equal(t, true, tokenBudget["Valid"])
	assert.Equal(t, float64(tokens), tokenBudget["Int64"])
}

func assertTaskBudgets(t *testing.T, text string, cost float64, retries int, duration int64, tokens int64) {
	t.Helper()
	var task map[string]interface{}
	parseData(t, text, &task)
	costBudget := task["CostBudget"].(map[string]interface{})
	assert.Equal(t, true, costBudget["Valid"])
	assert.Equal(t, cost, costBudget["Float64"])
	assert.Equal(t, float64(retries), task["MaxRetries"])
	maxDuration := task["MaxDurationMs"].(map[string]interface{})
	assert.Equal(t, true, maxDuration["Valid"])
	assert.Equal(t, float64(duration), maxDuration["Int64"])
	tokenBudget := task["TokenBudget"].(map[string]interface{})
	assert.Equal(t, true, tokenBudget["Valid"])
	assert.Equal(t, float64(tokens), tokenBudget["Int64"])
}
