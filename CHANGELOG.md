# Changelog

All notable changes to Torque are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor. Backfilled from tags and `git log`; good-faith, not exhaustive. Torque was renamed from Clockwork, so some older entries' task IDs (`CW-…`) are historical.

## [Unreleased]

### Added

- Pending HITL checkpoints are escalated once when nobody answers them: after
  24h (72h for `message`), the scheduler posts a `[system/checkpoint]`
  comment on the task and publishes `checkpoint.escalated`, and the
  checkpoint stays pending. A payload `escalation` object tunes it
  (`{"after_seconds": N}`) or opts out (`{"disabled": true}`). The existing
  `timeout_at` still resolves the checkpoint and blocks its task later.
- GitHub Actions CI (`.github/workflows/ci.yml`) on pull requests and pushes
  to `main`: `make lint` and `make test` with Go from `go.mod`, and the GUI's
  `npm ci`, build and vitest, with Go and npm caches. The private
  `github.com/hollis-labs/plugin` module is not reachable from CI yet, so
  until access is granted the Go job vets and tests every package except the
  three that need it (`cmd/torque`, `internal/plugin`, `plugins/core`) and
  says so in the run summary.
- `GET /api/v1/tasks/rollup?group_by=project_id|epic_id|sprint_id` counts
  tasks per scope and status in one query, and `GET /api/v1/tasks?fields=summary`
  leaves out each task's `description` and `system_prompt`. The GUI's
  Projects, Epics and Sprints pages use the rollup instead of paging every
  task (17 MB in 78 requests on a 4,097-task store, now one 3 KB request), and
  the scope detail pages use the summary list.
- ACP runtimes launch from Torque: Copilot (`acp-stdio`, `acp-tcp`) and Pi,
  which run only over ACP, and Claude, Codex and OpenCode with
  `runtime_kind: acp-stdio`. go-agent-wrapper owns the ACP session; Torque
  plants no boot dir for it and sends the task bundle and kickoff as the
  first prompt, and `SendTurn` sends each later turn as a `session/prompt`.
  Profile lint accepts `copilot` and `pi`. A scheduler-dispatched task run on
  Pi is refused, at enqueue and in Boot: pi-acp drops the MCP servers it is
  given, so its worker could not reach the loopback to comment or signal
  review. Manual Pi sessions launch. Until the wrapper delivers MCP servers
  (below), a long-lived task run (kind `agent`) on any ACP runtime is refused
  the same way; one-shot runs and manual sessions launch. Known gap: go-agent-wrapper v0.15.0
  sends `session/new` an empty `mcpServers`, so no ACP session gets the
  loopback or mux MCP yet. Torque builds the list (`loopback` over HTTP,
  `mux` over stdio, as planted for native runtimes) and tells the worker its
  MCP tools are unavailable until the wrapper takes it; that field arrives in
  go-agent-wrapper v0.19.0, and the loopback wiring activates with the bump
  to it (CW-20261001-0097).
- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `TRADEMARK.md`; MIT `LICENSE`.

### Changed

- go-agent-wrapper v0.17.1, agentkit v0.14.2 and go-providers v0.36.0 (with
  go-llm-types v0.5.1 and go-runtime-events v0.2.1). Per-turn runtimes always
  report typed events: a denied tool or a failed sign-in now also appears as
  a `[permission_denied:…]` or `[auth_failed]` line in the session's raw
  output, and the wrapper's `agent.permission_denied`, `session.auth_failed`
  and `session.lost` events are accepted and not yet acted on. Without a
  launch template, a session's extra arguments go before a prompt's `--`
  again, as with agentkit v0.12.3.
- Every turn of a launch runs its own argv, resolved from the prepared launch
  template (agentkit v0.13.0, go-agent-wrapper v0.16.0): the turn's prompt,
  last after `--`, and the session the previous turn reported. The profile's
  model is set on each runtime's adapter (Claude and Codex join OpenCode and
  agy), and its args and Claude's `--settings` go to the template's own
  extra-argument slot. Codex app-server now receives `-c model=…` before the
  profile's args.
- Codex sessions get the daemon's `mux` MCP server only under
  `permission_mode: bypassPermissions`; every other posture, unset included,
  plants just the run's own loopback (CW-20261001-0110). Codex runs MCP tools
  marked read-only without asking, so the approval responder could not gate
  mux's tools. Claude and OpenCode planting is unchanged.
- Runtimes are selected through the go-providers registry and
  go-agent-wrapper v0.15.0's `launch.Select` (agentkit v0.12.2, go-providers
  v0.34.1, go-sandbox v0.4.1), with the profile's runtime kind passed as the
  mode explicitly. Torque builds the adapter from go-providers' shared
  constructor table and sets the profile's options on it, so the launch argv
  of existing claude-code, codex and opencode profiles is unchanged.
  Antigravity (`antigravity` or `agy`) can now be launched: one `agy` per
  turn, with the profile's model and permission mode. A mode the wrapper does
  not drive (Claude's PTY) is refused before anything is planted. Profile
  lint accepts every registry name Torque launches, aliases included (`agy`,
  `open-code`).
- Codex app-server approval requests are answered from the profile's
  `permission_mode` instead of refused with -32601. Under `default`,
  `acceptEdits` and an unset mode, MCP tool calls are approved only on the
  run's own loopback server; every other server (including the planted
  `mux`, which reaches cerberus) is declined, as are sandbox escalations
  (`acceptEdits` also approves file changes). `plan` declines everything.
  `bypassPermissions` maps to yolo and approves everything: the
  `orchestrator` and `codex-implementer` profiles, which already run in a
  danger-full-access sandbox, now get unattended mux/cerberus MCP approval
  (flagged to revisit).
- agentkit v0.12.2, go-providers v0.34.1, go-sandbox v0.4.1 and
  agent-contracts-leaf v0.3.0 (Sprint 4 PR1). go-providers v0.34.1 and
  go-sandbox v0.4.1 are security fixes; with v0.34.1 a launch that carries
  the turn's prompt in argv ends `-- <prompt>`, so untrusted turn text is
  never parsed as a flag, and Torque's own argv splices (`--model`,
  `--settings`, codex `-c model=`) now go before that `--`. Runtime kinds
  use the shared vocabulary: `subprocess` is now `subprocess-per-turn` and
  `serve-http` is `http-sse`. A profile still accepts the older
  `subprocess` and `serve-http`; stored session rows are read with every
  older spelling (`subprocess`, `cli`, `serve-http`, `app-server`,
  `pty-debug`) through the new `internal/runtimetoken`. Runtime defaults,
  provider ids and profile lint's cli providers come from the go-providers
  runtime registry; a cli provider the registry does not know (`gemini`) is
  no longer listed, and registered runtimes Torque cannot launch yet
  (Antigravity, Copilot, Pi) lint with the reason.
- The reviewer end-agent no longer emits `message` checkpoints. Advisory
  findings, including a deliverable left unregistered as an artifact (now
  check 7, `Audit advisory (check 7 — artifact_registration)`), are
  `[system/end-agent]` comments only, so they no longer pile up in the
  pending HITL queue.
- `agentkit` v0.11.1: a child that prints its last lines and exits at once
  no longer has them dropped (jsonrpc-stdio, serve-http, PTY).
- `go-agent-wrapper` v0.14.0 and `agentkit` v0.11.0 (adds `go-permission`
  v0.1.0; `go-providers` stays v0.30.0). No behaviour change in Torque: the
  wrapper's new `PermissionPosture` answers Codex app-server approvals, but
  Torque runs codex on its own agentsessions path, not through the wrapper.
- GUI pages refresh on task events at most once per 1.5 s burst instead of
  on every event. The Ops Dashboard patches task status in place from
  transition events and refetches only the tasks named by other events. Scope
  detail pages show their tasks a page at a time, most recently updated
  first, with counts from the rollup. Task lists ask for 200-row pages instead
  of the server's default 50.
- The MCP server adopted `go-mcp` (official SDK), dropping `mark3labs/mcp-go`.
- Completed task tracking and design history archived out of the repository;
  README rewritten as a pre-release identity and stack-fit document.
- `go-queue` dependency moved off its retired `v0.1.1` tag.
- agentkit v0.10.0, go-providers v0.30.0 and go-agent-wrapper v0.13.0 (were
  v0.6.1, v0.26.0, v0.10.1); go-sandbox follows to v0.4.0. OpenCode runs now
  use `opencode run --format json`, so Torque receives its tool calls,
  per-step token usage and a done event per turn instead of plain-text lines.
  The OpenCode boot dir defines the agent in `agents/<name>.md` frontmatter
  and no longer plants `agents.json` or an `opencode.json` agent block.
- go-agent-wrapper v0.13.1: a turn's usage now arrives on its single terminal
  event instead of a second `turn.completed`. The wrapper event sink reads
  usage from `turn.completed` and `turn.failed` and still emits the turn's
  done event; without that, a wrapper-path ModeOneShot run timed out waiting
  for its turn to finish.
- Retired runtime-kind spellings keep working where they are stored. A
  profiles.yaml `runtime_kind` of `subprocess` or `serve-http` loads as
  `subprocess-per-turn` or `http-sse` with one deprecation warning per
  profile; `cli`, `app-server` and `pty-debug` stay boot-time errors in a
  profile, as before. Session rows that hold any older token read back as
  the current mode and are not rewritten; new rows store only current
  tokens.

### Fixed

- Steering a Codex app-server session keeps working after a command prints
  more than 1 MiB on one line. The session reader stopped at that line, so
  later turns timed out while the session still looked alive (#149); lines up
  to 64 MiB are now read whole.
- Later turns of `codex exec` and `opencode run` sessions reach the CLI.
  Every turn re-ran the first turn's argv, so text sent with SendTurn never
  arrived; codex exec also dropped the profile's model and args. Codex exec
  still starts a new thread each turn: go-providers' exec convention has no
  resume argument yet.
- OpenCode sessions get their briefing on the first turn. OpenCode runs in
  the project directory, so the `Boot @./boot.md` kickoff pointed at a file
  that is not there; it now receives `boot.md`'s content instead.
- The boot kickoff, the planted `process.md` and the agent execution docs
  name the per-task MCP server `loopback`, the name go-providers plants it
  under (codex's `[mcp_servers.loopback]`, the `loopback` entry in Claude's
  `.mcp.json` and OpenCode's config). They said `torque_loopback`, a server
  no worker has (CW-20261001-0114).
- An agent CLI installed outside the daemon's PATH launches. Boot pins the
  path go-providers' Detect resolves (its `*_CLI_PATH` override, PATH, then
  install dirs such as `~/.opencode/bin` and `~/.local/bin`) as the planted
  launch's binary. The go-agent-wrapper path spawned the bare name before,
  so `opencode` in `~/.opencode/bin` failed with "executable file not found
  in $PATH" (CW-20261001-0098).
- `agentkit` v0.12.3 (CW-20261001-0102): a planted launch's provider flags
  and injected args go before the `--` that has ended a prompt-carrying argv
  since go-providers v0.34.1. With v0.12.2 they landed after it and reached
  the agent as prompt text.
- OpenCode `serve-http` profiles launch `opencode serve --port 0 --hostname
  127.0.0.1` again. The wrapper launch path trimmed the prepared command to
  the bare executable, so the child started as plain `opencode`.
- A task can no longer be created or updated with an executor this Torque
  process has not registered: HTTP answers 422 and MCP `arg_invalid`,
  naming the registered executors (`api`, `cli`, `mock`). An empty executor
  still means the default, and rows already carrying an unregistered
  executor stay editable. `torque mcp` validates against the same names as
  `torque serve`; a process with no executor registry does not validate.
- Tests can no longer run a real agent CLI. The wrapper-boot e2e fixture was
  found only through `CLAUDE_CLI_PATH`, while the wrapper path resolves a bare
  `claude` through PATH, so `make test` ran the developer's real Claude Code
  (a paid model call per run) and a bootstrap test reached the real
  `opencode`. Every package that can reach a launcher now installs refusing
  shims for the agent CLIs first on PATH (`testenv.RunWithAgentShims`).
- `make test` passes on Linux. The `torque-apikey-helper` resolver tests ran
  against a fake keychain but were refused off macOS before reaching it; the
  macOS-only gate now sits on the real keychain accessor. Tests that slept a
  fixed time and then asserted on asynchronous work (per-run worktree
  dispatch, serve shutdown under `-race`, long-lived task deadlines) wait on
  the condition or allow Boot real headroom instead.
- On the go-agent-wrapper path, `Manager.Wait` after `Manager.Stop` waits for
  the run to end. Stop dropped the session's wrapper handle, so Wait returned
  at once and the session row could still read `running`.
- Dispatched workers are told the repository's configured remote is the
  only push target: never add, guess or repoint a remote, and stop with
  `torque_task_blocked` and the evidence on unrelated history or someone
  else's commits instead of resetting or force-pushing. The engine now
  snapshots the run's remotes and HEAD before the worker boots and parks a
  run in `blocked` when a remote was added, removed or repointed, or HEAD
  shares no history with where it started. A worker had inferred a remote
  from the project name and opened a PR that would have wiped an unrelated
  app.
- A manual task in `doing` no longer holds its project's scheduler slot. The
  picker never dispatches manual tasks, so one being worked outside the
  scheduler kept every dispatchable task in its project at `project_busy`.
- Stuck-task recovery no longer resets manual tasks. A manual task is never
  dispatched, so at `doing` it has no worker heartbeat; the health scan
  reported it as `task_doing_no_worker` every tick and re-queued it to `todo`
  once it aged past `TORQUE_SCHED_STUCK_GRACE`. Manual tasks are now neither
  reported nor recovered.
- `make build-prod` embeds the GUI on Linux: it copied `apps/gui/dist/`, which
  GNU cp nests as `dist/dist`, so the binary 404'd on `/`. `make install` now
  installs that GUI-embedded build, to an overridable `BINDIR`, and `make gui`
  installs with `npm ci` so a build no longer dirties the lockfile.
- Every HTTP messaging body takes an address the same way: `from`/`to` on
  `/broker/send`, `/broker/request` and `/messages`, and `recipient` on
  `/messages/{id}/consume`, accept the `msg://<kind>/<authority>/<id>` string
  or a `{"kind","authority","id"}` object. Any other shape is a 400 that names
  the field and both forms; an absent or null address is a 422.
- `go test` no longer depends on TORQUE_* in the shell it runs in: the
  config, agent and `cmd/torque` tests clear them first, so a dispatched
  worker or a dev shell with Torque settings exported gets the same results.
- MCP write paths reject a non-integer priority.
- Operator pause is recorded as cancellation; task deadlines are enforced for
  long-lived runs; parent-owned task review is allowed.
- Forced single-comment delete; artifact metadata and partial updates;
  initial project status on create; relative artifact paths resolve from the
  task workdir; session env and meta exposed over MCP.
- Scheduler tests updated for `CancelCauseFunc`.
- Long-lived run verification grades every runtime by one rule: commits pass;
  an uncommitted diff (not a Bash call) is "edits but no commits"; a clean
  worktree passes when the worker made tool calls or left comments or
  artifacts on its task. Read-only claude-code runs are no longer graded
  blocked or failed.
- Long-lived claude-code runs record token usage. claude reports usage only
  in a turn's final `result` event, and a worker ends its run with a tool
  call that moves its task to review, so the session was stopped before that
  event and the run recorded 0/0. A streaming-stdio session now gets up to
  30s to finish the open turn before it is stopped. Cost stays 0: no pricing
  is applied to these runs, as for codex.
- A long-lived run graded "edits but no commits" now parks its task in
  `blocked` instead of retrying under `on_fail`. The reason names the
  preserved worktree, how many paths are uncommitted, and the remedy: commit
  or discard them there, then re-queue. A retry used to re-dispatch at once
  into a fresh worktree off `origin/main`, stranding the diff and holding the
  project's slot.

### Security

- A planted OpenCode boot dir's `opencode.json`, which carries the MCP
  servers' environment (the `mux` entry's env included), is written owner-only
  (0600, go-providers v0.36.0). It was 0644. The boot dir itself was already
  0700, so other users could not reach it.

## [0.3.0] - 2026-05-17

### Added

- The GUI is embedded into the binary: one binary, one port.
- Dispatch moved onto the extended launch engine (`go-agent-launch`), with a
  headless dispatch mode.

### Fixed

- Reverted a broken Collections page rewrite in the GUI.

## [0.2.0] - 2026-05-17

### Added

- Torque-controlled permission mode for spawned Claude agents; repo-aware
  per-run worktree placement with a git precheck; an agent execution
  environment contract.
- End-to-end fixes so Codex and Claude runs work under orchestration.

### Changed

- Dead multi-agent worktree subsystem removed; profile lint recognizes
  `agent_profile_aliases`; pre-release developer-experience fixes.

## [0.1.1] - 2026-05-09

### Added

- Orchestrator self-stop, per-session PID polling, role-aware MCP loopback for
  orchestrator-class roles, and MCP output sanitization middleware.

### Changed

- Version bump to 0.1.1.

## Pre-release history

- **April 2026.** Initial task FSM, persistent queue and scheduler, executors,
  SQLite store, HTTP and MCP surfaces, and the first GUI.

[Unreleased]: https://github.com/hollis-labs/torque/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/hollis-labs/torque/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/hollis-labs/torque/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/hollis-labs/torque/releases/tag/v0.1.1
