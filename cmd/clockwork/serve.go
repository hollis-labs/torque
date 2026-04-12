package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/bootstrap"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start Clockwork HTTP API server, scheduler, and GUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if addr == "" {
				addr = fmt.Sprintf(":%d", cfg.HTTPPort)
			}

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen %s: %w", addr, err)
			}

			// Build a context that cancels on SIGINT/SIGTERM, so runServe's
			// own graceful-shutdown path tears everything down cleanly.
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return runServe(ctx, ln)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default :8990 from config)")
	return cmd
}

// runServe is the context-aware core of the serve command. It owns the full
// runtime stack (DB, queue, registry, scheduler, SSE bridge, HTTP server) and
// returns when ctx is cancelled or the HTTP server fails. Both the cobra
// command and integration tests call this entry point.
//
// The caller supplies a net.Listener so tests can bind to 127.0.0.1:0 and
// read the chosen port before handing the listener off.
func runServe(ctx context.Context, ln net.Listener) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// DB + migrations + store
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

	// Queue (hot-write SQLite for scheduler jobs)
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return fmt.Errorf("ensure data dir: %w", err)
	}
	queuePath := filepath.Join(cfg.DataDir, "queue.db")
	q, err := queue.Open(queuePath)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}
	defer q.Close()

	// Executor registry
	registry := executor.NewRegistry()
	registry.Register(executor.NewMockExecutor())

	// Load profiles (optional — missing file is OK for mock-only runs).
	profiles := loadProfilesOrEmpty()

	if err := bootstrap.Executors(registry, profiles, nil); err != nil {
		return fmt.Errorf("bootstrap executors: %w", err)
	}

	// Scheduler
	sched := scheduler.New(store, q, registry, &cfg.Scheduler)

	// Service + HTTP handler
	svc := service.New(store)
	handler := httpserver.New(svc, sched)

	// SSE bridge: scheduler.EventBus → httpserver.SSEHub
	bridge := httpserver.NewSchedulerBridge(handler.SSEHub(), sched.EventBus())

	// Background goroutines share a derived context so cancelling the parent
	// ctx tears down the scheduler loop and the SSE bridge together.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		bridge.Run(runCtx)
	}()
	go func() {
		defer wg.Done()
		if err := sched.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("scheduler: %v", err)
		}
	}()

	srv := &http.Server{Handler: handler}

	// Graceful HTTP shutdown is driven by the same context the goroutines watch.
	// When ctx fires we Shutdown() with a 5s grace window; if Shutdown exceeds
	// that budget we fall through to Close() to force the listener closed.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		log.Println("shutting down...")
		cancel() // belt and suspenders: also cancels runCtx
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
			srv.Close()
		}
	}()

	log.Printf("Clockwork HTTP server listening on %s", ln.Addr().String())
	log.Printf("GUI available at http://%s", ln.Addr().String())
	log.Printf("API available at http://%s/api/v1", ln.Addr().String())
	log.Printf("Scheduler: workers=%d interval=%ds enabled=%t",
		cfg.Scheduler.Workers, cfg.Scheduler.IntervalSeconds, cfg.Scheduler.Enabled)

	serveErr := srv.Serve(ln)
	// Wait for the shutdown goroutine to finish its Shutdown call before we
	// wait on the worker goroutines — otherwise we may return before the HTTP
	// server has fully quiesced.
	<-shutdownDone
	cancel()
	wg.Wait()

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

// loadProfilesOrEmpty loads agent profiles from CLOCKWORK_PROFILES_PATH if
// set, or from "./profiles.yaml" if it exists. Missing files are NOT an
// error: returns an empty ProfileMap so mock-only dev setups work out of the
// box. Real-CLI runs require the file and will fail per-task at Validate time.
func loadProfilesOrEmpty() config.ProfileMap {
	path := os.Getenv("CLOCKWORK_PROFILES_PATH")
	if path == "" {
		path = "profiles.yaml"
	}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		log.Printf("no profiles file at %s (using empty profile map)", path)
		return config.ProfileMap{}
	}

	profiles, err := config.LoadProfiles(path)
	if err != nil {
		log.Printf("failed to load profiles from %s: %v (using empty profile map)", path, err)
		return config.ProfileMap{}
	}
	log.Printf("loaded %d profile(s) from %s", len(profiles), path)
	return profiles
}
