package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start Clockwork HTTP API server + GUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if addr == "" {
				addr = fmt.Sprintf(":%d", cfg.HTTPPort)
			}

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
			handler := httpserver.New(svc, nil)

			srv := &http.Server{
				Addr:    addr,
				Handler: handler,
			}

			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
				<-sigCh
				log.Println("Shutting down HTTP server...")
				srv.Close()
			}()

			log.Printf("Clockwork HTTP server listening on %s", addr)
			log.Printf("GUI available at http://localhost%s", addr)
			log.Printf("API available at http://localhost%s/api/v1", addr)

			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default :8990 from config)")
	return cmd
}
