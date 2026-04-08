package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	manifestFile         = "plugin.yaml"
	manifestFileDisabled = "plugin.yaml.disabled"
)

// DisablePlugin disables the named plugin by renaming its plugin.yaml to plugin.yaml.disabled.
// Returns an error if the plugin is already disabled or not installed.
func DisablePlugin(pluginsDir, name string) error {
	active := filepath.Join(pluginsDir, name, manifestFile)
	disabled := filepath.Join(pluginsDir, name, manifestFileDisabled)

	if _, err := os.Stat(disabled); err == nil {
		return fmt.Errorf("plugin %q is already disabled", name)
	}
	if _, err := os.Stat(active); os.IsNotExist(err) {
		return fmt.Errorf("plugin %q is not installed", name)
	}

	if err := os.Rename(active, disabled); err != nil {
		return fmt.Errorf("disable plugin %q: %w", name, err)
	}
	return nil
}

// EnablePlugin re-enables the named plugin by renaming plugin.yaml.disabled back to plugin.yaml.
// Returns an error if the plugin is already enabled or has no disabled manifest.
func EnablePlugin(pluginsDir, name string) error {
	active := filepath.Join(pluginsDir, name, manifestFile)
	disabled := filepath.Join(pluginsDir, name, manifestFileDisabled)

	if _, err := os.Stat(active); err == nil {
		return fmt.Errorf("plugin %q is already enabled", name)
	}
	if _, err := os.Stat(disabled); os.IsNotExist(err) {
		return fmt.Errorf("plugin %q has no disabled manifest", name)
	}

	if err := os.Rename(disabled, active); err != nil {
		return fmt.Errorf("enable plugin %q: %w", name, err)
	}
	return nil
}

// IsDisabled reports whether the named plugin is disabled (has a .disabled manifest).
func IsDisabled(pluginsDir, name string) bool {
	disabled := filepath.Join(pluginsDir, name, manifestFileDisabled)
	_, err := os.Stat(disabled)
	return err == nil
}

// GetPluginStatus returns the current status of the named plugin:
// "disabled", "installed", or "not-installed".
func GetPluginStatus(pluginsDir, name string) string {
	active := filepath.Join(pluginsDir, name, manifestFile)
	disabled := filepath.Join(pluginsDir, name, manifestFileDisabled)

	if _, err := os.Stat(disabled); err == nil {
		return "disabled"
	}
	if _, err := os.Stat(active); err == nil {
		return "installed"
	}
	return "not-installed"
}
