package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start Torque MCP server (stdio transport)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, driver, err := appdb.Open(cmd.Context(), cfg.DBPath)
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
			// models.dev catalog: tied to the cobra command context so the
			// background refresher stops cleanly when the stdio session ends.
			svc.Models = modelcatalog.New()
			svc.Models.Start(cmd.Context())

			// Profile config (TORQUE_PROFILES_PATH or <ConfigDir>/profiles.yaml)
			// — required by agent.Boot to resolve provider + adapter selection
			// when session-creating tools (torque_plan_start,
			// torque_session_create) are invoked. Empty map is fine; per-
			// task Validate will surface "profile not found" at dispatch.
			profiles, err := loadProfilesOrEmpty(cfg)
			if err != nil {
				return err
			}

			// Agent substrate (CW-20260509-0013): wire agent.Manager into the
			// stdio MCP adapter so session-manager-dependent tools
			// (torque_plan_start, torque_session_*) work over MCP stdio
			// the same way they do over HTTP. Before this fix, calling
			// torque_plan_start over the stdio transport returned
			// `ErrSessionMgrMissing` because mcpadapter.New(svc, nil) wasn't
			// chained with .WithSessions(...).
			//
			// Lifecycle caveat — sessions spawned via stdio mcp are owned by
			// THIS process. When stdio mcp exits (typical shape: closed by
			// the IDE / orchestration host that invoked it), in-memory
			// session state goes away. The orchestrator subprocess survives
			// (reparented to launchd/init under Unix) and continues running
			// autonomously via its own MCP loopback HTTP server, but the
			// per-session teardown that registerStderrCloser /
			// registerStreamCloser / registerLoopback / registerBootDir
			// drives doesn't fire — workspace artifacts and the ephemeral
			// boot dir persist on disk. The next manager startup's orphan
			// sweep (Sweep() — syscall.Kill(pid, 0)) correctly identifies
			// the still-live process and spares its DB row, but no manager
			// can SendInput to it directly because that requires the
			// in-memory inner runtime registration this process is dropping.
			//
			// Bus is nil — the stdio mcp host has no SSE consumer, so
			// emitting session.state_changed events would just discard them.
			// The agent.Manager honors nil bus per its NewManager contract.
			//
			// Tools is nil — defaults to toolbroker.NewDefault() inside
			// AgentDeps, fine for the no-tool-broker path.
			//
			// Scheduler is intentionally NOT wired here. The stdio mcp does
			// NOT dispatch kind=agent / kind=internal tasks (that's the
			// `torque serve` daemon's role). torque_scheduler_status now
			// proxies read-only state from the local serve process, while
			// torque_scheduler_toggle remains unavailable because this
			// process does not own the scheduler instance.
			agentDeps, agentDepsClose, err := bootstrap.AgentDeps(store, profiles, svc, nil, nil, nil)
			if err != nil {
				return fmt.Errorf("bootstrap agent deps: %w", err)
			}
			defer agentDepsClose()

			// stdio MCP reserves stdout for the JSON-RPC protocol stream;
			// route go-mcp-sanitize warn telemetry to stderr (CW-20260509-0033,
			// mirrors vanta-conduit v0.6.1).
			sanitizeLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			adapter := mcpadapter.New(svc, nil).
				WithSessions(agentDeps.Sessions).
				WithLogger(sanitizeLogger)

			stdio := server.NewStdioServer(adapter.Server())
			return stdio.Listen(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
}
