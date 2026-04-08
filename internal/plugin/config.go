package plugin

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PluginManifest is the parsed content of a plugin's plugin.yaml file.
type PluginManifest struct {
	Name         string                 `yaml:"name"`
	Version      string                 `yaml:"version"`
	Description  string                 `yaml:"description"`
	Author       string                 `yaml:"author"`
	URL          string                 `yaml:"url"`
	Dependencies []string               `yaml:"dependencies"`
	Config       map[string]ConfigEntry `yaml:"config"`
	Runtime      string                 `yaml:"runtime"`
	Entrypoint   string                 `yaml:"entrypoint"`
}

// ConfigEntry describes a single configuration value in a plugin manifest.
type ConfigEntry struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	EnvVar      string `yaml:"env_var"`
	Default     string `yaml:"default"`
	Description string `yaml:"description"`
}

// PluginConfig holds the resolved configuration for a plugin.
type PluginConfig struct {
	pluginID  string
	schema    map[string]ConfigEntry
	overrides map[string]string
}

// ParseManifest reads and parses a plugin.yaml file at path.
func ParseManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// configOverrides is the shape of config.yaml override files.
type configOverrides struct {
	Config map[string]string `yaml:"config"`
}

// NewPluginConfig builds a PluginConfig from the plugin directory.
// It reads plugin.yaml for the config schema and optionally config.yaml for overrides.
func NewPluginConfig(pluginID, pluginDir string) (*PluginConfig, error) {
	manifestPath := pluginDir + "/plugin.yaml"
	manifest, err := ParseManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", pluginID, err)
	}

	schema := manifest.Config
	if schema == nil {
		schema = map[string]ConfigEntry{}
	}

	overrides := map[string]string{}
	overridePath := pluginDir + "/config.yaml"
	if data, err := os.ReadFile(overridePath); err == nil {
		var co configOverrides
		if err := yaml.Unmarshal(data, &co); err != nil {
			return nil, fmt.Errorf("plugin %s: parse config.yaml: %w", pluginID, err)
		}
		if co.Config != nil {
			overrides = co.Config
		}
	}

	return &PluginConfig{
		pluginID:  pluginID,
		schema:    schema,
		overrides: overrides,
	}, nil
}

// Get resolves a config value for key using the precedence:
// env var → config.yaml override → manifest default → error if required, empty if optional.
func (pc *PluginConfig) Get(key string) (string, error) {
	entry, exists := pc.schema[key]
	if !exists {
		return "", fmt.Errorf("plugin %s: unknown config key %q", pc.pluginID, key)
	}

	// 1. Environment variable override.
	if entry.EnvVar != "" {
		if val, ok := os.LookupEnv(entry.EnvVar); ok {
			return val, nil
		}
	}

	// 2. config.yaml override.
	if val, ok := pc.overrides[key]; ok {
		return val, nil
	}

	// 3. Manifest default.
	if entry.Default != "" {
		return entry.Default, nil
	}

	// 4. Required but missing.
	if entry.Required {
		return "", fmt.Errorf("plugin %s: required config key %q is not set", pc.pluginID, key)
	}

	return "", nil
}
