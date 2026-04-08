package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hollis-labs/clockwork-manifold/internal/plugin"
	"github.com/spf13/cobra"
)

// pluginsDir returns the plugins directory from env or the default.
func pluginsDir() string {
	if dir := os.Getenv("CLOCKWORK_PLUGINS_DIR"); dir != "" {
		return dir
	}
	return "./plugins"
}

// newPluginCmd returns the "plugin" parent command and all subcommands.
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage Clockwork plugins",
	}

	cmd.AddCommand(pluginListCmd())
	cmd.AddCommand(pluginInstallCmd())
	cmd.AddCommand(pluginUninstallCmd())
	cmd.AddCommand(pluginEnableCmd())
	cmd.AddCommand(pluginDisableCmd())

	return cmd
}

func pluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := pluginsDir()

			discovered, err := plugin.DiscoverPlugins(dir)
			if err != nil {
				return fmt.Errorf("discover plugins: %w", err)
			}

			// Also collect disabled plugins by scanning the directory directly.
			type row struct {
				name, version, status, description string
			}

			rows := make([]row, 0)

			// Track names we've seen via DiscoverPlugins (enabled + constructor known).
			seen := map[string]bool{}
			for _, dp := range discovered {
				seen[dp.Manifest.Name] = true
				rows = append(rows, row{
					name:        dp.Manifest.Name,
					version:     dp.Manifest.Version,
					status:      plugin.GetPluginStatus(dir, dp.Manifest.Name),
					description: dp.Manifest.Description,
				})
			}

			// Scan for disabled plugins (plugin.yaml.disabled present).
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if seen[name] {
					continue
				}
				disabledPath := filepath.Join(dir, name, "plugin.yaml.disabled")
				if _, err := os.Stat(disabledPath); err == nil {
					// Try to parse the disabled manifest to get metadata.
					m, parseErr := plugin.ParseManifest(disabledPath)
					if parseErr != nil {
						rows = append(rows, row{
							name:        name,
							version:     "unknown",
							status:      "disabled",
							description: "",
						})
					} else {
						rows = append(rows, row{
							name:        m.Name,
							version:     m.Version,
							status:      "disabled",
							description: m.Description,
						})
					}
				}
			}

			if len(rows) == 0 {
				fmt.Println("No plugins installed.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tSTATUS\tDESCRIPTION")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.name, r.version, r.status, r.description)
			}
			return w.Flush()
		},
	}
}

func pluginInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [name]",
		Short: "Install a plugin from the catalog",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("catalog-based install requires configured sources")
		},
	}
}

func pluginUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir := pluginsDir()
			pluginDir := filepath.Join(dir, name)

			if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
				return fmt.Errorf("plugin %q is not installed", name)
			}

			if err := os.RemoveAll(pluginDir); err != nil {
				return fmt.Errorf("uninstall plugin %q: %w", name, err)
			}

			fmt.Printf("Plugin %q uninstalled.\n", name)
			return nil
		},
	}
}

func pluginEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a disabled plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := plugin.EnablePlugin(pluginsDir(), name); err != nil {
				return err
			}
			fmt.Printf("Plugin %q enabled.\n", name)
			return nil
		},
	}
}

func pluginDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := plugin.DisablePlugin(pluginsDir(), name); err != nil {
				return err
			}
			fmt.Printf("Plugin %q disabled.\n", name)
			return nil
		},
	}
}
