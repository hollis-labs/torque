# Getting Started

## Prerequisites

- Go 1.26.x
- Node/npm for the GUI workflow if you are editing frontend code

## Main Commands

```bash
clockwork serve
clockwork mcp
clockwork version
```

## Local Defaults

Without extra configuration:

- SQLite is used
- The DB path defaults to `clockwork.db`
- The HTTP server listens on port `8990`
- Runtime data goes under `.clockwork/`

## Useful Environment Variables

```bash
export CLOCKWORK_DB_PATH=/path/to/clockwork.db
export CLOCKWORK_HTTP_PORT=8990
export CLOCKWORK_DATA_DIR=.clockwork
export CLOCKWORK_PROFILES_PATH=/path/to/profiles.yaml
```

To use Postgres instead of SQLite:

```bash
export CLOCKWORK_POSTGRES_DSN=postgres://...
```

## Notes About `serve` vs `mcp`

`clockwork serve` starts:

- the HTTP API
- the scheduler
- the GUI

`clockwork mcp` starts:

- the MCP server over stdio

The stdio MCP process does not create or own a live scheduler instance. Scheduler MCP tools can still exist in the adapter, but they return an error when called from that process because there is no in-process scheduler to control.
