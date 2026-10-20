package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.1"

// commit is stamped at link time by the Makefile (-X main.commit=<sha>); when
// empty, serve falls back to the vcs.revision in the Go build info.
var commit = ""

func main() {
	root := &cobra.Command{
		Use:   "torque",
		Short: "Torque — task orchestration and execution engine",
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("torque %s\n", version)
		},
	})

	root.AddCommand(mcpCmd())
	root.AddCommand(newPluginCmd())
	root.AddCommand(newProfilesCmd())
	root.AddCommand(pathCmd())
	root.AddCommand(serveCmd())
	root.AddCommand(costBackfillCmd())
	root.AddCommand(aarCmd())
	root.AddCommand(fkOrphanCleanupCmd())
	root.AddCommand(judgeCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
