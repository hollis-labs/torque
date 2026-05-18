package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.1"

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

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
