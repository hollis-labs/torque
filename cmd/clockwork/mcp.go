package main

import (
	"fmt"
	"os"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start Clockwork MCP server (stdio transport)",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, driver, err := appdb.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			if err := migrations.Run(db); err != nil {
				return fmt.Errorf("run migrations: %w", err)
			}

			store, err := sqlstore.New(db, driver)
			if err != nil {
				return fmt.Errorf("create store: %w", err)
			}

			svc := service.New(store)
			adapter := mcpadapter.New(svc)

			stdio := server.NewStdioServer(adapter.Server())
			return stdio.Listen(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
}
