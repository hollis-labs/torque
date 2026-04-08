package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("[clockworkd] starting...")

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[clockworkd] config: %v", err)
	}

	// Open main database
	db, driver, err := appdb.Open()
	if err != nil {
		log.Fatalf("[clockworkd] database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := migrations.Run(db); err != nil {
		log.Fatalf("[clockworkd] migrations: %v", err)
	}

	// Create store
	store, err := sqlstore.New(db, driver)
	if err != nil {
		log.Fatalf("[clockworkd] store: %v", err)
	}
	defer store.Close()

	// Open queue database (separate SQLite file for hot writes)
	queuePath := filepath.Join(cfg.DataDir, "queue.db")
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("[clockworkd] data dir: %v", err)
	}

	q, err := queue.Open(queuePath)
	if err != nil {
		log.Fatalf("[clockworkd] queue: %v", err)
	}
	defer q.Close()

	// Set up executor registry
	registry := executor.NewRegistry()

	// Register mock executor (always available for testing)
	registry.Register(executor.NewMockExecutor())

	// Create scheduler
	sched := scheduler.New(store, q, registry, &cfg.Scheduler)

	// Set up HTTP server with SSE endpoint
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// SSE endpoint for real-time event streaming
	r.Get("/events", sseHandler(sched))

	// Health/status endpoint
	r.Get("/status", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sched.Status())
	})

	// Start HTTP server
	httpAddr := fmt.Sprintf(":%d", cfg.HTTPPort+1) // clockworkd on port+1 (e.g., 8991)
	go func() {
		log.Printf("[clockworkd] SSE server on %s", httpAddr)
		if err := http.ListenAndServe(httpAddr, r); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[clockworkd] http: %v", err)
		}
	}()

	// Handle shutdown signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("[clockworkd] received %s, shutting down...", sig)
		cancel()
	}()

	// Run scheduler (blocks until context cancelled)
	if err := sched.Run(ctx); err != nil {
		log.Printf("[clockworkd] scheduler stopped: %v", err)
	}

	log.Println("[clockworkd] stopped")
}

// sseHandler returns an HTTP handler that streams scheduler events as SSE.
func sseHandler(sched *scheduler.Scheduler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		sub := sched.EventBus().Subscribe()
		defer sched.EventBus().Unsubscribe(sub)

		// Send initial status
		status, _ := json.Marshal(sched.Status())
		fmt.Fprintf(w, "event: status\ndata: %s\n\n", status)
		flusher.Flush()

		for {
			select {
			case event, ok := <-sub:
				if !ok {
					return
				}
				data, err := json.Marshal(event)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
