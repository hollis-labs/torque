package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/spf13/cobra"
)

// pathCmd prints Torque's resolved on-disk layout — the go-apppaths roots,
// the active workspace, and the main database, plus the Torque-derived extras
// (profiles.yaml, queue database). It is the introspection surface operators
// use to confirm where the daemon will read and write before a deploy.
//
// It resolves through config.Load, so the printed paths reflect every
// override the running daemon would see (TORQUE_DB_PATH, TORQUE_PROFILES_PATH,
// TORQUE_QUEUE_DB_PATH, TORQUE_WORKSPACE, $XDG_*).
func pathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print Torque's resolved on-disk layout (go-apppaths)",
		Long: "Print the data/state/cache/config roots, active workspace, and\n" +
			"main database path Torque resolves via go-apppaths, plus the\n" +
			"derived profiles.yaml and queue database paths.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, e := range cfg.Paths.Describe() {
				fmt.Fprintf(w, "%s\t%s\n", e.Label, e.Value)
			}
			fmt.Fprintf(w, "profiles\t%s\n", cfg.ProfilesPath)
			fmt.Fprintf(w, "queue-db\t%s\n", cfg.Concurrency.QueueDBPath)
			return w.Flush()
		},
	}
}
