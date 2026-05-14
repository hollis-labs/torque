package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/service"
)

func TestResolveVars_Basic(t *testing.T) {
	got, err := service.ResolveVars("hello {{name}}", map[string]string{"name": "world"})
	require.NoError(t, err)
	assert.Equal(t, "hello world", got)
}

func TestResolveVars_Multiple(t *testing.T) {
	got, err := service.ResolveVars("{{a}}-{{b}}-{{a}}", map[string]string{"a": "A", "b": "B"})
	require.NoError(t, err)
	assert.Equal(t, "A-B-A", got)
}

func TestResolveVars_UnresolvedFails(t *testing.T) {
	_, err := service.ResolveVars("hello {{name}}", map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestResolveVars_PartialResolutionFails(t *testing.T) {
	// Mixed: one resolves, one doesn't.
	_, err := service.ResolveVars("{{a}} and {{b}}", map[string]string{"a": "A"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "b")
}

func TestResolveVars_NoPlaceholders(t *testing.T) {
	got, err := service.ResolveVars("plain string", nil)
	require.NoError(t, err)
	assert.Equal(t, "plain string", got)
}

func TestResolveVars_EmptyString(t *testing.T) {
	got, err := service.ResolveVars("", nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestResolveVars_InvalidIdentifierIgnored(t *testing.T) {
	// {{ }} with spaces or digits-first isn't an identifier match — leave as
	// literal text.
	got, err := service.ResolveVars("{{ spaces }} {{1bad}}", nil)
	require.NoError(t, err)
	assert.Equal(t, "{{ spaces }} {{1bad}}", got)
}

func TestResolveVarsInMap_Recursive(t *testing.T) {
	in := map[string]any{
		"k1": "{{a}}",
		"nested": map[string]any{
			"k2": "{{b}}",
			"k3": 42, // non-string leaf untouched
		},
		"list": []any{"{{a}}", "static"},
	}
	out, err := service.ResolveVarsInMap(in, map[string]string{"a": "A", "b": "B"})
	require.NoError(t, err)
	assert.Equal(t, "A", out["k1"])
	nested := out["nested"].(map[string]any)
	assert.Equal(t, "B", nested["k2"])
	assert.Equal(t, 42, nested["k3"])
	list := out["list"].([]any)
	assert.Equal(t, "A", list[0])
	assert.Equal(t, "static", list[1])
}

func TestResolveVarsInMap_UnresolvedPropagates(t *testing.T) {
	in := map[string]any{"k": "{{missing}}"}
	_, err := service.ResolveVarsInMap(in, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}
