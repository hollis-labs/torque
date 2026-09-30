# Security policy

## Supported versions

Torque is pre-1.0 software. Security fixes are made on `main` and in the newest
tagged release, when one exists. Older releases may not receive backports.

## Report a vulnerability

Do not include an exploit, token, database, or other sensitive material in a
public issue.

Use GitHub's private vulnerability-reporting flow when the repository's
Security tab offers it. If it is unavailable, contact a repository maintainer
privately through a contact channel published on the Hollis Labs organization
or maintainer profile. Include:

- the affected commit or version and operating system
- the surface involved (`torque serve` HTTP API or GUI, `torque mcp`, an executor)
- the listen address and whether the port was reachable beyond the local machine
- reproduction steps and the security impact
- whether credentials, task data or host access may have been exposed
- a safe way to contact you about coordination

Maintainers will acknowledge a private report, investigate it, and coordinate
disclosure; response times are best effort.

## Deployment boundary

Torque is designed as a local-first tool for one operator on one machine.

- **`torque serve` currently listens on all interfaces by default** (`:8990`, or
  the port in `TORQUE_HTTP_PORT`), and its **HTTP API and GUI have no
  authentication and no TLS**. Anyone who can reach the port can read task and
  run data and create and dispatch tasks. Because the `cli` executor launches
  agent processes with your user's permissions, that is equivalent to command
  execution on the host. **Run it with `--addr 127.0.0.1:8990`** (or behind
  firewall rules and an authenticating, TLS-terminating reverse proxy) and do
  not expose the port to a network. Hardening this default is tracked as a known
  limitation.
- The admin endpoints (for example the GUI rebuild action) are restricted to
  loopback callers, with an optional `TORQUE_ADMIN_TOKEN` (`X-Admin-Token`) on
  top. That protects only those endpoints.
- **`torque mcp`** speaks MCP over stdio to the process that launched it and can
  create and change tasks; run it only for clients you trust. Write and
  destructive tools are available to that client.
- New tasks created through MCP start as `manual`, so they do not dispatch until
  someone promotes them; treat that as a safeguard, not an access control.
- Tasks run with whatever tools, permissions and environment the task or launch
  profile grants. Review profiles and task definitions from untrusted sources
  before dispatching them.

## Data at rest

Torque stores tasks, runs, comments, artifacts and audit data in SQLite
(`TORQUE_DB_PATH`, default `torque.db`) or Postgres (`TORQUE_POSTGRES_DSN`), plus
runtime files under `TORQUE_DATA_DIR` (default `.torque`). There is no built-in
encryption. Task descriptions, agent transcripts and environment values you put
into tasks may contain secrets, so protect these paths with filesystem
permissions and do not share a database without reviewing it. Do not put
credentials in task descriptions or committed profiles.

## External data processors

The agents Torque dispatches (Claude, Codex and others) send whatever context
the task and boot files give them to their own providers, under those tools'
own configuration. Webhooks, plugins and other integrations you enable may also
make network calls; review them before enabling.

## Current security limitations

- HTTP API and GUI are unauthenticated, and `serve` binds all interfaces by
  default
- no built-in TLS
- no at-rest encryption
- executors run with the operator's permissions; there is no sandbox beyond what
  the launched tool provides
- pre-1.0 contracts and schema
