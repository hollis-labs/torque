# Getting Started

## Prerequisites

- Go 1.26.x
- Node/npm for the GUI workflow if you are editing frontend code

## Main Commands

```bash
torque serve
torque mcp
torque version
```

## Local Defaults

Without extra configuration:

- SQLite is used
- The DB path defaults to `torque.db`
- The HTTP server listens on port `8990`
- Runtime data goes under `.torque/`

## Useful Environment Variables

```bash
export TORQUE_DB_PATH=/path/to/torque.db
export TORQUE_HTTP_PORT=8990
export TORQUE_DATA_DIR=.torque
export TORQUE_PROFILES_PATH=/path/to/profiles.yaml
```

To use Postgres instead of SQLite:

```bash
export TORQUE_POSTGRES_DSN=postgres://...
```

## Notes About `serve` vs `mcp`

`torque serve` starts:

- the HTTP API
- the scheduler
- the GUI

`torque mcp` starts:

- the MCP server over stdio

The stdio MCP process does not create or own a live scheduler instance. Scheduler MCP tools can still exist in the adapter, but they return an error when called from that process because there is no in-process scheduler to control.
