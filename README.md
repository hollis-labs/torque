# Torque

Torque is a task orchestration engine: a task FSM, a persistent queue and
scheduler, pluggable executors, and HTTP + MCP surfaces over one SQLite or
Postgres store. It dispatches work and records what happened. It is not a
general project-management suite, not a workflow engine for every product in
the portfolio, and not an agent runtime — it does not host the agent it
dispatches to.

> **Pre-release.** Torque has no external users or public API yet, but it is
> in active daily use internally — this repo, and most others in Hollis
> Labs, track their own work through it. It's being built in the open: this
> README describes what's running today, not a pitch for what's planned.
> Interfaces change without notice and there are no compatibility guarantees.

## What it is today

- **A task FSM with permissive transitions.** Any status in
  `CanonicalStatuses` can reach any other in one call — nothing has to walk a
  path to record what already happened. Only an unknown status or leaving a
  terminal state (`done`/`archived`) is refused.
- **A self-healing scheduler.** Dispatches runnable tasks to registered
  executors, and reconciles stuck `doing` tasks and orphaned `running` runs
  on its own — a periodic health-scan agent watches for scheduler anomalies.
- **HTTP + MCP, same contract.** `torque serve` runs the HTTP API and GUI;
  `torque mcp` exposes the same task/project/epic/sprint/plan operations as
  MCP tools. Both share one filter/sort/pagination contract.
- **Human-in-the-loop checkpoints.** A task can carry a typed checkpoint
  schema and pause mid-run for a human decision instead of failing or
  guessing.
- **Supporting structure**: artifacts, comments, subtodos, templates, runs,
  and plans, plus optional projects/sprints/epics/collections behind feature
  flags — deliberately bounded, not a general portfolio-planning tool.

## Where it sits in the stack

```
   humans + agents          reading/writing the same task graph, over
   (Claude, Codex, ...)     HTTP or MCP — no separate "agent view"
         │
    ┌──────────┐
    │  Torque  │    task FSM + queue + scheduler + HITL checkpoints
    └──────────┘
         │
   executors                Nanite or any other CLI agent runtime actually
                             runs the dispatched work; Torque tracks status,
                             cost/token budget, retries, and outcome
```

Torque is the coordination record, not the runtime. It sits beside Tesseract
(knowledge) and Cerberus (deploy) as a peer, not a layer above or below them
— all three are reached the same way, as MCP tools.

## Examples

**Daily use.** This is where Hollis Labs' own work lives: this very session
tracks its tasks, epics, and projects in Torque over MCP. Chrispian and any
agent working a session read and write the same task graph — there's no
separate "agent-facing" copy of the board.

**Composition.** A task with a checkpoint attached pauses mid-run for a human
decision instead of guessing; once resolved, the scheduler redispatches it.
An orchestrator agent walks a plan's phases, dispatches each phase as a task
to an executor (Nanite or another CLI runtime), and lets Torque record
status, retries, and budget — Torque never runs the work itself.

**Multi-agent handoff.** The broker/inbox tools let one agent request or hand
off work into another agent's queue without either one polling the other
directly.

## Roadmap

- **Plans v2.** Promote plan phases from an opaque JSON array inside task
  metadata to first-class, independently-tracked entities with their own
  identity, ordering, and history.
- **Scheduler reliability.** Continued hardening of orphan/stale-run
  reconciliation and the health-scan agent beyond today's baseline.

## Start Here

- Repo docs index: [docs/README.md](docs/README.md)
- Agent/operator notes: [AGENTS.md](AGENTS.md)

## Commands

```bash
torque serve
torque mcp
torque profiles lint
torque version
```

`torque serve` starts the HTTP API, scheduler, and GUI on `127.0.0.1` and the
configured HTTP port. It serves only loopback by default and needs no token
there. To listen on another interface, set a token (`TORQUE_API_TOKEN` or
`--token`); serve refuses a non-loopback `--addr` without one, and clients then
send `Authorization: Bearer <token>`. Browser origins other than loopback ones
must be allowed with `TORQUE_CORS_ORIGINS` or `--cors-origin`. See
[SECURITY.md](SECURITY.md) before exposing the port.

The standalone Vite frontend (`cd apps/gui && npm run dev`, or the Cerberus
resource `torque-frontend-dev`) uses port **5182** and refuses to silently move
to another port if it is occupied. The Cerberus resource proxies API requests
to the dev API on 8992; a direct Vite launch defaults to the API on 8990 unless
`TORQUE_GUI_API_ORIGIN` is set.

`torque mcp` starts the MCP server over stdio. In that mode there is no in-process scheduler instance, so scheduler MCP tools report that the scheduler is not running in that process.

`torque serve` also serves the same MCP tools at `/mcp` (Streamable HTTP, stateless) on its HTTP listener, behind the same auth as `/api/v1`: loopback hosts only without `TORQUE_API_TOKEN`, the bearer token with it. It is wired as stdio `torque mcp` is, with the daemon's own scheduler and session manager, and with no inbox-poll registry: `torque_inbox_poll` answers that polling is not available, as it does over stdio.

`torque mcp --remote` relays stdio to that endpoint instead of opening the database, so a client that must not touch Torque's state (an agent under write protection) still gets the full tool surface. `--remote` alone, or `TORQUE_MCP_REMOTE=1`, uses `http://127.0.0.1:$TORQUE_HTTP_PORT/mcp`; `--remote=URL` (with the equals sign: a separate argument is refused) or `TORQUE_MCP_REMOTE=URL` selects another daemon. An empty `--remote=` or an empty `TORQUE_MCP_REMOTE` is an error, never the local database; `0` or `false` asks for the local database explicitly, so a launcher that exports `TORQUE_MCP_REMOTE=` empty to mean "unset" now stops `torque mcp` and must unset the variable instead. Notes:

- The tool list is the daemon's, registered when it started, so enabling a feature (`features.collections`, below) takes a daemon restart, not a restart of the relay.
- Each forwarded request gets its own connection to the daemon, so a request that fails (a 400, a 500, a refused token) fails only itself. Requests run concurrently, at most 32 at a time. Neither the order of concurrent messages nor a client's cancellation of a request is preserved: the daemon finishes the call and the reply is dropped. That is harmless while the daemon's MCP handler is stateless.
- It sends `TORQUE_API_TOKEN` as the bearer, never follows a redirect (so the token cannot reach another host), and warns when it would send the token over plain http to a non-loopback host.
- The MCP handler's own localhost check ignores the token: a same-host reverse proxy that keeps the original `Host` header is refused by `/mcp` even with a valid bearer. Have the proxy rewrite `Host` to a loopback name, or point `--remote` at the daemon directly.
- `/mcp` is wired to the daemon's live scheduler, which stdio `torque mcp` is not, so `torque_scheduler_toggle` works over `--remote`: a client that can reach `/mcp` can pause and resume dispatch, with the same authority as the `/api/v1` scheduler toggle.

Opt-in MCP tool groups are registered when the MCP process starts, based on persisted `features.*` settings in the backing DB. If you enable a new feature such as `features.collections`, restart the MCP process so `tools/list` picks up the new `torque_collection_*` tools.

`torque profiles lint` validates `profiles.yaml` as an execution-template registry against Torque's current executor/provider catalog. It fails on unknown fields, missing or unsupported providers, and dishonest profile names that omit or misstate the provider binding. Use `make profiles-lint` in CI or pre-commit.

## Configuration

Common environment variables:

- `TORQUE_DB_PATH` — SQLite DB path, default `torque.db`
- `TORQUE_POSTGRES_DSN` — if set, use Postgres instead of SQLite
- `TORQUE_HTTP_PORT` — HTTP port for `serve`, default `8990` (bound on
  `127.0.0.1` unless `--addr` says otherwise)
- `TORQUE_API_TOKEN` — bearer token required on `/api/v1` and `/mcp` requests; required to
  bind beyond loopback
- `TORQUE_CORS_ORIGINS` — comma-separated browser origins allowed besides
  loopback ones
- `TORQUE_DATA_DIR` — runtime data dir, default `.torque`
- `TORQUE_PROFILES_PATH` — optional agent profile YAML path
- `TORQUE_SANDBOX_PROTECT` — `0` (or `false`, `off`, `no`) launches agents
  without write-protecting Torque's state directories; on by default. Under
  the protection nested sandboxes cannot start (bubblewrap- or
  `unshare`-based tests, Chromium's sandbox); this is the escape hatch. See
  [docs/agent-execution-environment.md](docs/agent-execution-environment.md#control-plane-write-protection).

Scheduler-related settings are documented in [docs/runtime.md](docs/runtime.md).
