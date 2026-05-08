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

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	clockmsg "github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/modelcatalog"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/bootstrap"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
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
	if err := migrations.Run(db); err != nil {
		db.Close()
		return fmt.Errorf("run migrations: %w", err)
	}

	store, err := sqlstore.New(db, driver)
	if err != nil {
		db.Close()
		return fmt.Errorf("create store: %w", err)
	}
	defer store.Close()

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

	// Service constructed early so the agent.Executor / agent.Boot path can
	// claim it for per-task MCP loopback wiring (CW-20260427-0059). Same
	// handle reused by httpserver below.
	svc := service.New(store)

	// Tool-broker (CW-20260503-0015 / Plan 4): go-toolbroker selection +
	// permission engine + audit log, threaded into the unified agent
	// substrate via agent.Dependencies.
	tools := toolbroker.NewDefault()

	// Waitpoll predicate registry (kind=wait dispatch).
	predicates := waitpoll.NewRegistry()
	if err := bootstrap.Waitpoll(predicates, store); err != nil {
		return fmt.Errorf("bootstrap waitpoll: %w", err)
	}

	// Scheduler — constructed before AgentDeps so deps can capture
	// sched.EventBus() for session.state_changed lifecycle SSE.
	sched := scheduler.New(store, q, registry, predicates, &cfg.Scheduler)

	// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
	if len(cfg.Scheduler.ProjectAllowlist) > 0 {
		log.Printf("[serve] scheduler project-scope filter active: %v (stopgap — CW-20260417-0129 replaces this with workspaces)",
			cfg.Scheduler.ProjectAllowlist)
	}

	// Unified agent substrate (CW-20260508-0001 — replaces cliexec + sessionmgr).
	// Constructs Dependencies + Manager, runs the orphan sweep, and is the
	// single root every Boot caller (planstart, scheduler dispatch, end-agent,
	// HTTP/MCP) reaches into.
	agentDeps, err := bootstrap.AgentDeps(store, profiles, svc, tools, sched.EventBus())
	if err != nil {
		return fmt.Errorf("bootstrap agent deps: %w", err)
	}

	// Register executors against the unified deps. agent.NewExecutor occupies
	// the "cli" slot the legacy cliexec.CLIExecutor previously held; the
	// scheduler's worker pool dispatches kind=agent / kind=internal tasks
	// through it transparently.
	if err := bootstrap.Executors(registry, agentDeps); err != nil {
		return fmt.Errorf("bootstrap executors: %w", err)
	}

	// HTTP handler — svc was constructed earlier so the executor path can
	// claim it for per-task MCP loopback wiring.
	handler := httpserver.New(svc, sched)
	handler.WithSessions(agentDeps.Sessions)

	// Durable messaging substrate (CW-20260503-0012, S1.2). Same SQLite DB
	// the rest of the runtime uses; migration 022_messages.sql created the
	// tables. Broker layer (S1.3) sits on top of this Store.
	msgStore := clockmsg.NewStore(db)
	handler.SetMessaging(msgStore)

	// Typed envelope broker (CW-20260503-0013, S1.3) — Clockwork-specific
	// validation + envelope.* SSE publishing on top of the Store. Distinct
	// from internal/toolbroker (S1.5); package layout deliberately split
	// to avoid the name collision flagged in the boot prompt.
	handler.SetBroker(broker.New(msgStore, handler.SSEHub()))

	// Background goroutines share a derived context so cancelling the parent
	// ctx tears down the scheduler loop, the SSE bridge, and the models.dev
	// refresher together. Declared here so the catalog can attach to runCtx
	// before any handler-served request can land.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// models.dev catalog: background-refreshes pricing/limits/capabilities so
	// model-aware code paths (cost backfill, context-window guardrails, etc.)
	// can resolve metadata without threading a client through every layer.
	svc.Models = modelcatalog.New()
	svc.Models.Start(runCtx)

	// Scheduler cost backfill (Phase 2): wire the catalog + profiles so the
	// cost-record block can fall back to models.dev pricing when the executor
	// stream doesn't emit cost_usd. Gated on the scheduler.cost_backfill_enabled
	// setting so we can A/B against executor-reported cost during rollout.
	sched.Models = svc.Models
	sched.Profiles = profiles
	// Cost backfill is on by default — the natural pre-conditions (Models +
	// Profiles wired, executor reports cost=0, profile has provider+model)
	// already gate it. Set scheduler.cost_backfill_disabled=true as an
	// emergency off-switch if the catalog produces wildly wrong numbers.
	if v, _ := svc.Settings.Get("scheduler.cost_backfill_disabled"); v == "true" {
		sched.CostBackfillDisabled = true
		log.Printf("[serve] scheduler cost backfill DISABLED via setting")
	}

	// Phase 4 dispatch guardrails (CW-20260426-0036). Tri-state per gate:
	// off / warn / block. Default is warn-on-both so problems show up in
	// logs against real traffic without active risk; promote to block when
	// the estimator has a track record. Settings:
	//   scheduler.precheck_window         = off|warn|block (default warn)
	//   scheduler.precheck_window_threshold = 0..1         (default 0.8)
	//   scheduler.precheck_capabilities   = off|warn|block (default warn)
	sched.Precheck = scheduler.DefaultPrecheckOptions()
	if raw, _ := svc.Settings.Get("scheduler.precheck_window"); raw != "" {
		sched.Precheck.Window = scheduler.PrecheckMode(raw)
	}
	if raw, _ := svc.Settings.Get("scheduler.precheck_window_threshold"); raw != "" {
		var f float64
		if _, err := fmt.Sscanf(raw, "%f", &f); err == nil && f > 0 && f < 1 {
			sched.Precheck.WindowThreshold = f
		}
	}
	if raw, _ := svc.Settings.Get("scheduler.precheck_capabilities"); raw != "" {
		sched.Precheck.Capabilities = scheduler.PrecheckMode(raw)
	}
	log.Printf("[serve] scheduler precheck: window=%s (threshold %.0f%%) capabilities=%s",
		sched.Precheck.Window, sched.Precheck.WindowThreshold*100, sched.Precheck.Capabilities)

	// SSE bridge: scheduler.EventBus → httpserver.SSEHub
	bridge := httpserver.NewSchedulerBridge(handler.SSEHub(), sched.EventBus())

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

	// Graceful HTTP shutdown is driven by runCtx, which fires when either the
	// parent ctx is cancelled (normal SIGINT/SIGTERM path) OR when we call
	// cancel() ourselves after srv.Serve returns abnormally (e.g. external
	// listener close). Waiting on runCtx.Done() — not ctx.Done() — is what
	// lets the abnormal path unblock without a deadlock. Shutdown uses a 5s
	// grace window; on timeout we fall through to Close() to force the
	// listener closed.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-runCtx.Done()
		log.Println("shutting down...")
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
	log.Printf("Scheduler: workers=%d interval=%ds stale_heartbeat_threshold=%ds enabled=%t",
		cfg.Scheduler.Workers, cfg.Scheduler.IntervalSeconds, cfg.Scheduler.StaleSeconds, cfg.Scheduler.Enabled)

	serveErr := srv.Serve(ln)
	// Cancel FIRST so the shutdown goroutine unblocks on <-runCtx.Done() and
	// runs to completion (closing shutdownDone). If Serve returned an error
	// other than ErrServerClosed (e.g. an external listener close or accept
	// error), the parent ctx may not have been cancelled yet — without this
	// explicit cancel() the shutdown goroutine would block forever and we'd
	// deadlock on <-shutdownDone below.
	cancel()
	// Wait for the shutdown goroutine to finish its Shutdown call before we
	// wait on the worker goroutines — otherwise we may return before the HTTP
	// server has fully quiesced.
	<-shutdownDone
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
