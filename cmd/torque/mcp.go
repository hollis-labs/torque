package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/mcpbridge"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start Torque MCP server (stdio transport)",
		Long: `Start Torque MCP server (stdio transport).

With --remote (or TORQUE_MCP_REMOTE), relay to the running daemon's /mcp
endpoint instead of opening the database: the same tool surface, for
clients that must not open main.db, such as an agent under ProtectedPaths.
--remote alone, or TORQUE_MCP_REMOTE=1, uses
http://127.0.0.1:$TORQUE_HTTP_PORT/mcp; --remote=URL (with the equals sign) or
TORQUE_MCP_REMOTE=URL selects another endpoint. The tools are those the daemon
registered when it started, so enabling a feature takes a daemon restart, not a
new torque mcp process.`,
		// stdout carries the MCP protocol: an error must not print usage
		// onto it.
		SilenceUsage: true,
		// --remote's value is optional, so cobra reads `--remote URL` as
		// --remote plus a positional URL and the URL would be ignored: the
		// bridge would dial the default daemon, possibly the wrong one.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument %s: give --remote's URL as --remote=URL (a separate argument is not read as its value)", shownValue(args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before config.Load: resolving app paths materializes the data
			// dir, which --remote must never create or touch. And an
			// explicit but empty selection (--remote=, TORQUE_MCP_REMOTE=)
			// is an error, never the local database: a launcher passing
			// --remote=$UNSET would otherwise create main.db and the data
			// dirs, which is what --remote exists to avoid.
			env, envSet := os.LookupEnv(mcpbridge.RemoteEnv)
			endpoint, err := remoteMCPEndpoint(cmd.Flags().Changed("remote"), remote, envSet, env, config.HTTPPortFromEnv())
			if err != nil {
				return err
			}
			if endpoint != "" {
				return runRemoteMCP(cmd, endpoint, config.APITokenFromEnv())
			}
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
			// This process does not run that sweep itself: `torque serve`
			// owns the sessions and sweeps at its startup. A `torque mcp`
			// sweeping a daemon's sessions would race it, and one that mux
			// spawned inside an agent's sandbox sees no other process (its
			// own PID namespace) and would mark every live session crashed
			// (CW-20261001-0141).
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
			// pollReg / reminderReg are nil: the opt-in inbox-poll registry
			// (CW-20260518-0042) and the turn-boundary reminder registry
			// (CW-20260519-0065) are only meaningful in the same process as
			// the steering bridge, which runs under `torque serve` — not in
			// this stdio MCP process. torque_inbox_poll / torque_steering_
			// dismiss therefore report unavailable here; agents reach the
			// live registries via the in-process orchestrator loopback
			// adapter instead.
			agentDeps, agentDepsClose, err := bootstrap.AgentDeps(store, profiles, svc, nil, nil, nil, nil, nil)
			if err != nil {
				return fmt.Errorf("bootstrap agent deps: %w", err)
			}
			defer agentDepsClose()
			bootstrap.ProtectControlPlane(agentDeps, cfg)

			// No scheduler runs here, but agents create and update most
			// tasks through this process, so task writes validate against
			// the same executor names torque serve registers
			// (CW-20260910-0087). The registry is built only to read its
			// names; if that fails the check stays off rather than
			// refusing every executor.
			executors := bootstrap.NewExecutorRegistry()
			if err := bootstrap.Executors(executors, agentDeps); err != nil {
				fmt.Fprintf(os.Stderr, "torque mcp: executor registry unavailable, task executors are not validated: %v\n", err)
			} else {
				svc.Task.SetRegisteredExecutors(executors.List)
			}

			// stdio MCP reserves stdout for the JSON-RPC protocol stream;
			// route go-mcp-sanitize warn telemetry to stderr (CW-20260509-0033,
			// mirrors vanta-conduit v0.6.1).
			sanitizeLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			adapter := bootstrap.StdioMCPAdapter(svc, agentDeps.Sessions, sanitizeLogger)

			return adapter.Server().Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "relay to the daemon's /mcp endpoint (default http://127.0.0.1:$TORQUE_HTTP_PORT/mcp) instead of opening the database")
	cmd.Flags().Lookup("remote").NoOptDefVal = remoteDefault
	return cmd
}

// shownValue is v as it may appear in an error. A value that cannot be
// validated is not echoed when it could carry credentials: a URL with the
// scheme forgotten, a bad port or a stray bracket fails to parse, and what
// url.Parse cannot split, a scrubber cannot either. Anything containing '@'
// is hidden whole.
func shownValue(v string) string {
	if strings.Contains(v, "@") {
		return "the value (hidden: it contains '@', so it may carry credentials)"
	}
	return strconv.Quote(v)
}

// remoteDefault is --remote given without a URL.
const remoteDefault = "default"

// remoteMCPEndpoint returns the daemon MCP URL `torque mcp` relays to, or
// "" to serve from the local database. The flag wins over TORQUE_MCP_REMOTE;
// "default", "1" and "true" mean the daemon on this host's TORQUE_HTTP_PORT,
// and "0" and "false" ask for the local database explicitly. Only an absent
// flag and variable mean local by default: one that was given with an empty
// value is an error, so it can never fall into local mode unnoticed.
func remoteMCPEndpoint(flagSet bool, flag string, envSet bool, env string, port int) (string, error) {
	var v string
	switch {
	case flagSet:
		if v = strings.TrimSpace(flag); v == "" {
			return "", errors.New("torque mcp: --remote was given an empty value: use --remote for the daemon on this host, --remote=URL for another, or leave it out to serve from the local database")
		}
	case envSet:
		if v = strings.TrimSpace(env); v == "" {
			return "", fmt.Errorf("torque mcp: %s is set but empty: unset it to serve from the local database, or set it to 1 (the daemon on this host) or a URL", mcpbridge.RemoteEnv)
		}
	default:
		return "", nil
	}
	switch strings.ToLower(v) {
	case "0", "false":
		return "", nil
	case remoteDefault, "1", "true":
		return fmt.Sprintf("http://127.0.0.1:%d/mcp", port), nil
	}
	return v, nil
}

// runRemoteMCP relays stdio to the daemon (mcpbridge). It loads no config
// paths, opens no database, runs no migrations or orphan sweep, and writes
// nothing (CW-20261001-0199).
func runRemoteMCP(cmd *cobra.Command, endpoint, token string) error {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("torque mcp --remote: %s is not an http(s) URL (want http://host:port/mcp)", shownValue(endpoint))
	}
	local := &mcp.IOTransport{Reader: io.NopCloser(cmd.InOrStdin()), Writer: nopWriteCloser{cmd.OutOrStdout()}}
	return mcpbridge.Run(cmd.Context(), local, mcpbridge.Options{
		Endpoint: endpoint,
		Token:    token,
		Log:      cmd.ErrOrStderr(),
	})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
