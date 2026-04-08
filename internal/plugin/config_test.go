package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	require.NoError(t, err)
}

func TestParseManifest(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "plugin.yaml", `
name: my-plugin
version: 1.2.3
description: A test plugin
author: Tester
url: https://example.com
dependencies:
  - dep-a
  - dep-b
runtime: go
entrypoint: main
config:
  api_key:
    type: string
    required: true
    env_var: MY_API_KEY
    description: The API key
  timeout:
    type: int
    required: false
    default: "30"
    description: Timeout in seconds
`)
	m, err := ParseManifest(filepath.Join(dir, "plugin.yaml"))
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, "my-plugin", m.Name)
	assert.Equal(t, "1.2.3", m.Version)
	assert.Equal(t, "A test plugin", m.Description)
	assert.Equal(t, "Tester", m.Author)
	assert.Equal(t, "https://example.com", m.URL)
	assert.Equal(t, []string{"dep-a", "dep-b"}, m.Dependencies)
	assert.Equal(t, "go", m.Runtime)
	assert.Equal(t, "main", m.Entrypoint)

	require.Contains(t, m.Config, "api_key")
	assert.True(t, m.Config["api_key"].Required)
	assert.Equal(t, "MY_API_KEY", m.Config["api_key"].EnvVar)

	require.Contains(t, m.Config, "timeout")
	assert.Equal(t, "30", m.Config["timeout"].Default)
}

func TestParseManifest_Missing(t *testing.T) {
	_, err := ParseManifest("/nonexistent/path/plugin.yaml")
	require.Error(t, err)
}

func TestPluginConfigResolution(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "plugin.yaml", `
name: test-plugin
version: 1.0.0
config:
  with_default:
    type: string
    required: false
    default: "fallback"
    description: Has a default
  required_key:
    type: string
    required: true
    description: Required, no default
  optional_no_default:
    type: string
    required: false
    description: Optional, no default
  env_key:
    type: string
    required: false
    env_var: TEST_PLUGIN_ENV_KEY
    default: "from-default"
    description: Has env var
`)

	t.Run("default value", func(t *testing.T) {
		pc, err := NewPluginConfig("test-plugin", dir)
		require.NoError(t, err)
		val, err := pc.Get("with_default")
		require.NoError(t, err)
		assert.Equal(t, "fallback", val)
	})

	t.Run("env var override", func(t *testing.T) {
		t.Setenv("TEST_PLUGIN_ENV_KEY", "from-env")
		pc, err := NewPluginConfig("test-plugin", dir)
		require.NoError(t, err)
		val, err := pc.Get("env_key")
		require.NoError(t, err)
		assert.Equal(t, "from-env", val)
	})

	t.Run("optional missing returns empty", func(t *testing.T) {
		pc, err := NewPluginConfig("test-plugin", dir)
		require.NoError(t, err)
		val, err := pc.Get("optional_no_default")
		require.NoError(t, err)
		assert.Equal(t, "", val)
	})

	t.Run("required missing returns error", func(t *testing.T) {
		pc, err := NewPluginConfig("test-plugin", dir)
		require.NoError(t, err)
		_, err = pc.Get("required_key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "required_key")
	})
}

func TestPluginConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "plugin.yaml", `
name: test-plugin
version: 1.0.0
config:
  api_url:
    type: string
    required: false
    default: "https://default.example.com"
    description: API URL
`)
	writeYAML(t, dir, "config.yaml", `
config:
  api_url: "https://override.example.com"
`)

	pc, err := NewPluginConfig("test-plugin", dir)
	require.NoError(t, err)

	val, err := pc.Get("api_url")
	require.NoError(t, err)
	assert.Equal(t, "https://override.example.com", val)
}
