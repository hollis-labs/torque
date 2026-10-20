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

- **`torque serve` binds `127.0.0.1` by default** (`127.0.0.1:8990`, or the
  port in `TORQUE_HTTP_PORT`). Anyone who can call the API can read task and
  run data and create and dispatch tasks, and because the `cli` executor
  launches agent processes with your user's permissions, that is equivalent to
  command execution on the host. Treat API access as host access.
- **Without a token, the API is loopback-only.** It requires no
  authentication, answers only requests addressed to a loopback host
  (`127.0.0.1`, `localhost`, `::1`), which refuses DNS rebinding, and refuses
  requests from browser origins that are not loopback, which refuses
  cross-site request forgery from ordinary web pages. Any local process or
  local user who can reach the port is still trusted.
- **Binding any other address requires a token.** `torque serve` refuses to
  start on a non-loopback `--addr` (including `:8990` and `0.0.0.0`) unless
  `TORQUE_API_TOKEN` or `--token` is set. With a token set, every `/api/v1`
  and `/mcp` request, loopback ones included, must send
  `Authorization: Bearer <token>`.
  Other browser origins must be listed in `TORQUE_CORS_ORIGINS` or
  `--cors-origin`. Serve has no TLS, so put a TLS-terminating proxy in front
  of it before the token crosses a network.
- The bundled GUI does not send a token. With a token set, use the API from
  scripts and server-side clients; the GUI works only on the tokenless
  loopback default.
- The admin endpoints (for example the GUI rebuild action) are additionally
  restricted to loopback callers, with an optional `TORQUE_ADMIN_TOKEN`
  (`X-Admin-Token`) on top. "Loopback" is the TCP peer: forwarding headers
  (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`) are never trusted, and a
  request that carries one is treated as proxied and refused.
- **`torque mcp`** speaks MCP over stdio to the process that launched it and can
  create and change tasks; run it only for clients you trust. Write and
  destructive tools are available to that client.
- **`/mcp`**, served by `torque serve`, carries the same write surface as
  `/api/v1` and is gated exactly as it is: the same origin policy and bearer
  token on the same listener, so it is reachable no more widely than the API.
  `torque mcp --remote` is its stdio client. Treat anyone who can call `/mcp`
  as able to do whatever the tools allow. Unlike stdio `torque mcp`, `/mcp`
  is wired to the daemon's live scheduler, so `torque_scheduler_toggle` works
  through it: an agent whose `torque mcp` runs `--remote` can pause and resume
  dispatch, with the authority of the `/api/v1` scheduler toggle.
- New tasks created through MCP start as `manual`, so they do not dispatch until
  someone promotes them; treat that as a safeguard, not an access control.
- Tasks run with whatever tools, permissions and environment the task or launch
  profile grants. Review profiles and task definitions from untrusted sources
  before dispatching them.

## Tether-issued tokens and the posture dial

`torque serve` can verify Tether JWTs offline (go-auth, `libs/security/auth`) next to the static `TORQUE_API_TOKEN`. Torque never calls Tether to authenticate a request; it checks signatures against the issuer's published keys and polls the issuer's deny-list.

| Variable | Meaning |
| --- | --- |
| `HOLLIS_AUTH_TORQUE` | the posture dial for Torque: `off`, `permissive` (alias `observe`) or `enforce`. Falls back to `HOLLIS_AUTH`; the per-app variable wins. Default `off`. An invalid value stops startup. |
| `TORQUE_AUTH_ISSUER` | Tether base URL, also the required `iss`. Setting it turns JWT verification on. |
| `TORQUE_AUTH_AUDIENCE` | required `aud`; default `torque`. |
| `TORQUE_AUTH_JWKS_URL` | default `<issuer>/.well-known/jwks.json`. |
| `TORQUE_AUTH_BOOTSTRAP_KEY_FILE` | PEM public key or JWKS file used until the first JWKS fetch succeeds. |
| `TORQUE_AUTH_DENYLIST_URL` | default `<issuer>/auth/denylist`. Polled every 30s. |

What each mode does:

- `off` (default): the rules above are unchanged. A request that presents a valid JWT with the needed scope (`torque.read` for GET/HEAD, `torque.write` otherwise) is admitted and attributed; every other request is decided by the loopback / `TORQUE_API_TOKEN` rules.
- `permissive`: as `off`, and anything `enforce` would have refused is logged as "would deny".
- `enforce`: only a valid JWT with the needed scope, or the static token (which carries `torque.*`), is admitted. Anonymous callers get 401, an under-scoped token 403. `/mcp` needs a valid token at the transport and each tool call is scoped separately.

**Reloading without a restart.** Put the settings in `auth.yaml` in Torque's config directory (`torque path` shows it) instead of the environment and `torque serve` re-reads them on an edit (checked every 2 seconds, applied once the file is stable) or on `SIGHUP`:

```yaml
mode: enforce          # off | permissive | enforce
jwt:
  issuer: https://tether.example
  audience: torque
  jwks_url: ...          # default <issuer>/.well-known/jwks.json
  bootstrap_key_file: /path/issuer.pub.pem
deny_list:
  url: ...               # default <issuer>/auth/denylist
```

Precedence is per key: a key `auth.yaml` sets explicitly overrides the environment for that key only; anything it leaves out (including an empty file) is inherited from `TORQUE_AUTH_*` and, for the mode, from the `HOLLIS_AUTH_TORQUE` / `HOLLIS_AUTH` dial, else `off`. Removing the file while `torque serve` runs falls back to the environment the same way. The effective mode and where it came from (`auth.yaml`, `env HOLLIS_AUTH_TORQUE`, `default`) are logged at WARN as `auth mode in effect` at start and after every reload. The static token (`TORQUE_API_TOKEN` / `--token`) is a startup input, is not part of the file, and keeps working across reloads. A reload is all or nothing: the next verifier is built and validated before it replaces the running one (a JWKS URL with no bootstrap key needs its first fetch to succeed), and any failure (an unparsable file, an unknown key, an invalid mode, an unreadable key file, an unreachable endpoint) is logged and changes nothing. **A reload never forgets a revocation:** while the deny-list URL is unchanged the known list is kept, so an edit or `SIGHUP` during an outage of the issuer's deny-list cannot un-revoke a token; a new or changed URL needs a validated first fetch or the reload is refused. Each request is judged by the mode and verifier it started under (security/auth v0.2.1). Reloading can relax auth (for example enforce to permissive) only when the listener is loopback (a unix socket, localhost or a loopback IP; a tailnet address is NOT loopback here, unlike the startup bind rule); on any other listener (`--addr 0.0.0.0:...`) a reload that would weaken the mode is refused and the running mode stays (restart to change it), while strengthening or keeping the mode is allowed. An allowed relaxation is logged as a WARN `auth mode changed from=<old> to=<new>`. The file must not be group- or world-writable (checked at startup and on every reload; a refused reload keeps the running configuration), and a startup error from a file-derived configuration names `auth.yaml`. The release-posture note owns the remaining release-time constraints.

**Service tokens.** A dev service token is a Tether JWT with `kind=service`, `aud=torque`, scopes such as `torque.read torque.write` and an `exp` about ten years away (the verifier requires `exp`, so "no expiry" is a distant one). It needs no refresher. Revoke it by putting its `jti` on Tether's deny-list: revocation takes effect within one poll interval (30s by default) after a successful poll. If the issuer or its deny-list is unreachable, the last list stays in force (stale), so a revoked token keeps being accepted for as long as the outage lasts. Keep `TORQUE_API_TOKEN` configured as a fallback until the service token is proven.

## External session launch paths

`torque_session_create` / `torque_session_launch` over stdio or `/mcp`, and
`POST /api/v1/sessions/launch`, share a server-side path policy. Session resumes
use it too. Both `workdir` and the optional `repo_root` must be existing absolute
directories inside an unarchived registered project's `repo_path` or an
operator-set allowed base. Symlinks are resolved before containment is checked;
the canonical paths are used for the boot. A relative path, a sibling prefix,
or a symlink escaping all bases is refused before any runtime starts.

At deploy, the operator must add `TORQUE_SESSION_ALLOWED_ROOTS` for launch
locations beyond registered repositories, such as worktrees. It is a
colon-separated list of absolute directories, read once at manager startup.
For example, `TORQUE_SESSION_ALLOWED_ROOTS=/srv/projects:/srv/worktrees`.
No base is guessed when the list and registered project paths are empty;
external launch fails closed and names this setting. A request's `env`, `meta`,
project or task ID cannot replace the server's configured bases. Scheduler
launches keep their trusted internal path.

The denylist wins even beneath an allowed base. The operator's home itself,
its ancestors, `~/.tether`, `~/tether`, `~/.torque`, `~/.config`,
`~/.local/share`, `~/.local/state`, `~/.claude*`, `~/.codex*`, `~/.gemini*`,
and Torque's configured protected directories cannot become launch roots.
Their ancestors are refused too, because granting that root would grant writes
to the protected subtree. Denied paths are canonicalized as well, so an alias
does not exempt them.

This bounds externally requested launch roots against server-known paths. It
does not authenticate callers or decide which registered project a particular
caller may use. Caller identity (CW-20260930-0253) is still required for
per-caller authorization, and the API's existing host-access boundary applies
to task dispatch and other tools.

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

- one shared bearer token, no per-user accounts or scopes; the GUI cannot use
  it
- no built-in TLS
- no at-rest encryption
- executors run with the operator's uid. Torque write-protects its own state
  directories from the agents it launches (databases, config, session
  workspaces; see
  [docs/agent-execution-environment.md](docs/agent-execution-environment.md#control-plane-write-protection)),
  but that sandbox stops direct writes only: a same-uid agent can still plant
  code that runs outside it later (`~/.bashrc`, systemd user units, git hooks)
  or ask a same-uid service to write for it (`systemd-run --user`). Beyond
  that there is no sandbox other than what the launched tool provides.
- under that protection, nested sandboxes do not work: a process inside it
  cannot create its own user namespace, so a worker running bubblewrap- or
  `unshare`-based tests, or Chromium with its sandbox, fails there.
  `TORQUE_SANDBOX_PROTECT=0` is the escape hatch. A codex launch whose
  profile positively selects codex's own sandbox (`read-only` or
  `workspace-write`) is left to it, unwrapped; any other codex launch,
  `--yolo` and unrecognised sandbox or permission arguments included, is
  wrapped.
- the services behind a planted mux's tools (`cerberus_ssh_exec`, the docker
  daemon, the cerberus daemon) run outside the sandbox, with their own
  authority; an ssh to this host as the operator's uid is a same-uid route to
  the protected directories.
- Torque's own git runs unsandboxed in repositories agents can write. It
  neutralizes fsmonitor, hooks, the repository's filter drivers and `ext::`
  transports, and skips its best-effort fetch when the repository's config
  sets remote commands; config inside a submodule's own git dir is not
  covered.
- pre-1.0 contracts and schema
