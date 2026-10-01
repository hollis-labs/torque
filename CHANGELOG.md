# Changelog

All notable changes to Torque are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor. Backfilled from tags and `git log`; good-faith, not exhaustive. Torque was renamed from Clockwork, so some older entries' task IDs (`CW-…`) are historical.

## [Unreleased]

### Added

- `torque serve` serves the MCP tool surface at `/mcp` (Streamable HTTP,
  stateless) behind the same auth as `/api/v1`. `torque mcp --remote[=URL]`
  (or `TORQUE_MCP_REMOTE`) relays stdio to it without opening the database,
  creating any directory or running the orphan sweep, for agents whose
  sandbox write-protects Torque's state. `/mcp` has no inbox-poll registry,
  as stdio has none; the relay forwards requests concurrently, never
  follows a redirect with the bearer, answers requests outstanding when the
  daemon's connection dies, and reads `--remote URL` (without `=`) as an
  error. A request that fails fails only itself (each forwarded request has
  its own connection), an empty `--remote=` or `TORQUE_MCP_REMOTE` is an
  error rather than the local database (a launcher exporting it empty to
  mean "unset" must unset it), a URL value that cannot be validated is never
  echoed in an error, and neither is a query string. A URL with an `@` that
  is not `user:password@host` (a password holding an unencoded `/`, `#` or
  `?` makes it parse as a different host) is refused before anything is
  dialed (CW-20261001-0199).
- A long-lived worker that ends its turn without moving its task out of
  `doing` is reminded once, then routed, instead of holding its project's
  slot until the 30-minute inactivity threshold. After 90 seconds idle
  following a completed turn it gets one reminder turn to call
  `torque_task_review` or `torque_task_blocked`. If it answers and then ends
  that turn without signalling, engine-side verification routes the run 90
  seconds later: to review if it left commits on its branch or comments or
  artifacts on the task, to blocked otherwise, with a `[system/auto-route]`
  comment posted once the task has moved. Every turn Torque sends a session
  (the reminder, a steering message, the stuck probe) counts as in flight
  until it ends, so a slow reply is never routed mid-answer; a worker that
  never answers is left to the inactivity threshold. A task moved by anyone
  else before the route is left as they moved it. The window is task
  metadata `idle_nudge_seconds` (0 to 3600; 0 turns it off). It applies to
  `kind=agent` worker tasks only, never mid-turn, and never while the worker
  waits by design: on a pending checkpoint, on a child task still open, on
  steering messages it has not dismissed, or with its inbox polling active.
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
  plants no boot dir for it. `session/new` carries the run's MCP servers
  under the names native boot dirs use (go-agent-wrapper v0.19.0,
  CW-20261001-0120): `loopback` over HTTP, and the daemon's `mux` over stdio
  only under `permission_mode: bypassPermissions`. Every other posture,
  unset included, offers the loopback alone, as for Codex
  (CW-20261001-0110): whether an ACP agent asks before running an MCP tool
  is unverified, and Torque answers no ACP permission request yet. The task bundle and kickoff are the first prompt, sent
  once the session exists so the kickoff can say whether the loopback's
  tools are there, and `SendTurn` sends each later turn as a
  `session/prompt`. The session's ACP diagnostics (the agent's stderr,
  dropped MCP servers) go to its `session.log`. Profile lint accepts
  `copilot` and `pi`.

  A scheduler-dispatched task run on Pi is refused, at enqueue and in Boot:
  pi-acp passes no MCP server to Pi, so its worker could not comment or
  signal review. Any other agent that does not advertise
  `mcpCapabilities.http` is not sent the loopback; a long-lived task run on
  one is stopped at launch for the same reason. One-shot runs and manual
  sessions launch (CW-20261001-0097).
- An ACP agent's permission requests are answered from the profile's
  `permission_mode` (CW-20261001-0113); they were all declined. `plan`
  grants nothing; `default` and unset grant read-only tool kinds (`read`,
  `search`, `think`); `acceptEdits` also grants `edit`, but not `delete` or
  `move`, which ACP classes apart from edits; `bypassPermissions` grants
  every kind. `execute`, `fetch` and any other or unknown kind are granted
  only under `bypassPermissions`. A grant always takes the agent's
  `allow_once`, never `allow_always`, and is declined when the agent offers
  no `allow_once`; a decline takes `reject_once`. Each decision (kind, tool,
  option) is written to the session log. An agent may run some operations
  without asking, so this is not an execution gate.
- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `TRADEMARK.md`; MIT `LICENSE`.

### Changed

- go-agent-wrapper v0.23.0 (from v0.21.1), agentkit v0.20.3 (from v0.19.1)
  and go-providers v0.40.0 (from v0.39.0); go-sandbox stays v0.5.1
  (CW-20261001-0141). They bring the `ProtectedPaths` Torque now sets, and
  turn interrupts Torque does not use yet. Effects on Torque:
  - OpenCode serve: a failed turn ends once, as a failure, not as a failure
    followed by a completion. An error with no session id (a plugin that
    failed to load) is logged instead of failing the turn, and a context
    overflow OpenCode goes on to compact no longer fails it.
  - OpenCode errors carry the error's name, model and reference after the
    message.
  - A resume that lost its session also emits `session.lost`; Torque does
    not read that event.
  - Launch argv, environment and planted config are unchanged for every
    runtime.
- Resume capabilities come from the go-providers registry, per runtime and
  mode, instead of provider-name switches (CW-20261001-0174).
  `agent.Resume(provider, kind)` reports what the registry declares and
  whether Torque wires it. Wired is an allow-list of the (runtime, mode)
  pairs whose launch takes the stored session id, so a resume a library
  bump declares for a new mode stays off until Torque wires it.
  - **Resumes:** claude-code (streaming-stdio, subprocess) via
    `--resume <id>`; opencode `run` via `--session <id>`; Pi and opencode
    over ACP via `session/load`. opencode `run` booted fresh before.
  - **The session id is stored for subprocess runtimes too.** A
    subprocess-per-turn session (claude-code subprocess, opencode `run`)
    now stores the provider session id its first turn reports, as
    streaming-stdio and ACP sessions already did. Without it, there was
    nothing to resume.
  - **A lost provider session boots fresh once.** If a subprocess resume's
    first turn finds the provider no longer has the session (claude: "No
    conversation found", a `SessionLostError`), `ResumeSession` and
    planstart's redispatch boot fresh once, with the kickoff, rather than
    fail. Not covered yet (CW-20261001-0202): a streaming-stdio resume
    whose id is lost fails its first turn after Boot, and an ACP agent
    without `loadSession` opens a new session without saying so.
  - **Declared but not wired yet:** codex app-server (CW-20261001-0180)
    and agy (CW-20261001-0181). A Codex `ResumeSession` now boots fresh
    instead of passing an id the app-server ignored.
  - **Same runtime required:** `ResumeSession` and planstart's redispatch
    resume only when the session's profile still boots the runtime that
    recorded the id (registry runtime, any case).
  - **Breadcrumb:** the HITL response breadcrumb's `used_resume` is the
    resumed session's `Resumed`, false when no stored id existed or the
    provider had lost the session. It used to claim a resume for a row with
    no stored session id.
- Codex app-server's MCP tool-call approvals keep the run's loopback as the
  only server approved outside `bypassPermissions`, now through agentkit's
  `CodexApprovalResponder.MCPAllow` instead of Torque's own override.
  Decisions are unchanged; the log reason names the allow-list
  (CW-20261001-0124).
- go-agent-wrapper v0.21.1, agentkit v0.19.1, go-providers v0.39.0 and
  go-sandbox v0.5.1 (CW-20261001-0157). A claude-code launch now carries
  `--permission-mode <mode>` (`acceptEdits` when the profile sets none,
  `bypassPermissions` in developer mode), the same posture its planted
  `settings.json` already set: agentkit maps the launch plan's permission,
  now a go-permission Mode (`accept-edits`, `yolo`, ...), onto Claude's
  flag. Codex, OpenCode and agy launches carry no posture flags or
  environment, as before; Codex keeps its `never` / `workspace-write`
  default. Planted MCP config (loopback and mux) is unchanged for every
  runtime. A session's `session.log` is appended to, never truncated, so
  a log path reused by a later session accumulates. A Claude resume whose
  session id Claude no longer has is reported as a lost session.
- Torque's pre-wrapper launch path (bootLegacy) now runs only codex
  app-server. Another runtime kind reaching it fails to boot with a reason
  instead of spawning its command twice, the copy after the turn's
  `-- <prompt>`. Nothing in production routes another kind there: claude,
  opencode and agy launch through go-agent-wrapper, and PTY has no launch.
  The fake-runtime test seam still runs every kind (CW-20261001-0080).
- go-agent-wrapper v0.19.0, for ACP sessions' MCP servers (above). Its
  v0.18.0 change to the wrapper's own `plant` package does not reach Torque,
  which plants through agentkit.
- go-agent-wrapper v0.17.1, agentkit v0.14.2 and go-providers v0.36.0 (with
  go-llm-types v0.5.1 and go-runtime-events v0.2.1). Per-turn runtimes always
  report typed events: a denied tool or a failed sign-in now also appears as
  a `[permission_denied:…]` or `[auth_failed]` line in the session's raw
  output, and the wrapper's `agent.permission_denied`, `session.auth_failed`
  and `session.lost` events are accepted and not yet acted on. Without a
  launch template, a session's extra arguments go before a prompt's `--`
  again, as with agentkit v0.12.3. Torque's ACP launches pick up the
  wrapper's ACP fixes: a child's last frame at exit, such as a
  `session/close` reply, is no longer lost (v0.17.1), and ACP deltas carry
  `block_id`, with `phase` on Copilot's as on the other runtimes (v0.17.0).
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

- A run's cost is one figure, priced once (CW-20260912-0003). The cost a
  runtime reports (Claude's `total_cost_usd`) is taken as given; the tokens
  of turns that reported none are estimated from models.dev with cache
  pricing: cache reads at the cache-read price and cache writes at the
  cache-write price, and for codex, whose input counts its cached tokens,
  only the uncached remainder at the input price. Before, Claude's reported
  cost was discarded and every cache read was priced as input, so a codex
  run like 1079 was recorded at $110.96 where this prices it at $15.38.
  - **One write.** `runs.cost` and the run's `cost_ledger` row are written
    in the same transaction as the run's completion (they were separate
    writes through the telemetry queue), so for a run completed after this
    migration the run, its task's cost and the scheduler's `total_cost`
    agree. A ledger insert that fails is rolled back and logged and no
    longer fails the completion: the run is completed with its cost and only
    its ledger row is missing.
  - **Provenance.** Runs and ledger rows record cache read and write tokens
    and a `cost_source`: `provider`, `estimate`, `mixed` (some turns reported
    a cost, some were estimated) or `none`; ledger rows also keep the
    provider and estimated parts. `torque_scheduler_status` adds
    `total_cost_by_source`. Migration 034.
  - **The profile priced is the one that ran.** A task's profile resolves as
    the executor resolves it, launch_profile first; a task with only a
    launch_profile used to look up no profile and cost 0.
  - **Every way a run ends keeps its usage.** A run killed by daemon
    shutdown, or failed or cancelled after the executor had accumulated
    usage, records that usage and its cost; one that ended without usage
    has the source `none`. The orphan reapers only reclaim a run that is
    still running, so a run that finished a moment earlier is not reset to
    failed at cost 0, and for it they record no orphan event, do not requeue
    its task and leave its worktree alone.
  - **Catalog lookups.** The `claude-code` provider now finds Anthropic's
    prices (CW-20261001-0182), and an opencode model id `<provider>/<model>`
    finds that provider's.
  - **What it does not do.** Estimates use the catalog's cache-write price,
    which for Anthropic is the 5-minute tier; Claude's 1-hour cache writes
    cost more, which is one reason its own figure comes first. A run whose
    model the catalog lacks keeps the provider's figure and is labelled
    `provider` even if some tokens could not be priced. Costs are reported,
    not enforced: `CostBudget` still stops nothing.
  - **A run an operator already cancelled, superseded or killed gets no
    write and no ledger row.** It keeps its row as stamped, so a result that
    arrives late and the usage in it are dropped, where before this the
    ledger took a row for a run whatever its status. That spend is therefore
    absent from `total_cost` and from the global cost ceiling check, which
    read the ledger. It is deliberate: the run row and the ledger never
    disagree. Whether such a run should still be costed is a separate
    decision (a follow-up task).
  - **History is not repriced.** Existing rows keep their figures, and
    `runs.cost` and the ledger were not written together before, so for
    older runs they do not all agree; the all-time `total_cost` still
    includes the old cache-unaware estimates (`models_dev`), which
    `total_cost_by_source` shows apart. Sprint cost and the over-budget
    filter still read `runs.cost` while `total_cost` reads the ledger.
  - **`torque cost-backfill`** prices a legacy row from input and output
    tokens alone, so it now skips Claude runs, which are mostly cache and
    would come out several times too low; only `unknown` rows are
    candidates, so a `none` row is not backfilled.
- A reviewer end-agent that ends `done` without auditing its target no
  longer passes silently (CW-20261001-0195). When its target is still in
  `review`, is not tagged `agent-closed` and has no comment by the end-agent
  since the end-agent was created, Torque posts `[system/end-agent] <id>
  finished without recording an audit — target stays at review; human
  follow-up required.` on the target, once. The end-agent's comments are
  matched by the author prefix `[system/end-agent]`: the author is stored as
  given through a run's loopback and caller-suffixed
  (`[system/end-agent]-<8 hex>`) through mux, and the audit comments posted
  through mux do not carry the prefix in their content.
- The HTTP and MCP session resume (`POST /api/v1/sessions/{id}/resume`,
  `torque_session_resume`, `Manager.Resume`) makes the same decision as
  `ResumeSession` (CW-20261001-0203). It continues the checkpoint's provider
  conversation only when a provider session id was recorded, the profile
  still boots the runtime that recorded it, and Torque wires that runtime's
  resume; otherwise it boots fresh with the kickoff. What changes:
  - **codex app-server:** a resume used to pass the thread id, which the
    app-server ignores, and skip the kickoff, so the new session sat silent.
    It now boots fresh and fires the kickoff on a new thread.
  - **claude-code (and opencode run, pi, the other wired runtimes):**
    `Checkpoint` never recorded the provider session id, so a checkpoint
    resume always started a session with no conversation to continue and, as
    `ModeResume`, no kickoff. A checkpoint now records the session's provider
    session id (a checkpoint written before this falls back to its session's
    own), and a resume launches the CLI with it (`claude --resume <id>`).
  - **Every resume is a long-lived boot that runs the kickoff,** like
    `ResumeSession`'s, as a new session bound to the source session's task,
    project and role, with the task bundle planted. It was an unlinked
    session with no task before. On subprocess-per-turn runtimes (claude
    subprocess, opencode run) the call now returns after the kickoff turn
    ends; streaming-stdio is unaffected.
  - **A lost provider session boots fresh once** when the loss shows before
    Boot returns, which is the case on subprocess-per-turn runtimes. A
    streaming-stdio resume finds out on its first turn, and an ACP agent
    without `loadSession` opens a new session without saying so
    (CW-20261001-0202).
  - The session's `Resumed` field (meta `torque.resumed`) is true when the
    launch carried a provider session id: what Torque asked for, which an
    ACP agent without `loadSession` can still ignore. It reads back on every
    session read.
- A long-lived run on the go-agent-wrapper path (opencode serve, claude-code,
  agy, ACP) ends as soon as a turn fails, blocked with the provider's
  message, as a Codex app-server run already did. An opencode serve worker
  whose model opencode did not know (`session.error`: "Model not found: …")
  left its task in `doing` and its run running with no tokens until the
  30-minute inactivity threshold. The policy is that any genuine failed turn
  blocks the run. That includes the turn the reminder pump sends a worker
  that stopped without moving its task (#166): when that turn fails, the
  run is now blocked rather than taking the unsignalled route. opencode
  serve errors that do not end its turn stay in the session's stream and
  leave the run going: one naming no session (a plugin that fails to load,
  a skill opencode cannot parse), a context overflow (opencode compacts the
  session and continues), and an abort (Torque's own Stop). The reason, and
  the error in the stream, is the provider's message: its first line, at
  most 500 bytes, redacted before it is cut. opencode's raw event and stack
  trace stay in `serve-http.log`. The session stays `failed` when the agent
  exits cleanly once stopped (CW-20261001-0169).
- A Codex app-server turn failure that echoed a launch secret put the
  secret in the run's reason: the stream's copy of the message was
  redacted, the copy that ended the run was not. Both are redacted now,
  before the message is cut to its bound (CW-20261001-0169).
- Torque's tests no longer write session workspaces into the operator's
  `~/.torque/workspaces`. Every test's agent dependencies get a temp root
  (`testenv.WorkspacesRoot(t)`), and under `go test` a workspaces root inside
  the real `~/.torque` is refused with an error naming that helper
  (CW-20261001-0175).
- The reviewer end-agent's task description no longer names a reviewer
  version. It said "V1 reviewer" while the stamped template is V2, and one
  reviewer stopped to ask which protocol to follow instead of auditing. It
  now reads "Disposition audit for <id> (reviewer end-agent)."
  (CW-20261001-0187).
- An ACP agent that exits during launch no longer crashes the Torque daemon
  with "send on closed channel" (go-agent-wrapper v0.21.1,
  CW-20261001-0129).
- A session on the go-agent-wrapper path (claude-code, opencode, agy, ACP)
  is torn down however it ends: its boot dir is removed and its loopback MCP
  listener, stderr and stream sidecars closed when the agent exits on its
  own, as the legacy path does on terminal state. Before, only an explicit
  Stop did it, so a manual session or orchestrator whose agent exited kept
  them until the daemon stopped. Manager.Shutdown now stops and tears down
  these sessions too; it reached only the legacy sessions. A boot that fails
  after its boot dir is allocated (planting, Codex authentication, launch
  conversion) removes the dir instead of leaving it in `$TMPDIR/torque-boot`
  with no session row to name it (CW-20261001-0161). A codex app-server
  session (the legacy path) that ends before Boot has registered its
  resources has them released on arrival rather than kept until the daemon
  stops (CW-20261001-0166).
- OpenCode `serve-http` sessions no longer hang on a permission prompt.
  Torque answers each `permission.asked` through serve's
  `/permission/{id}/reply`, by the profile's `permission_mode`:
  - `bypassPermissions`: once.
  - `default` and `acceptEdits`: read-only tools, and `external_directory`
    asked by one of them; `acceptEdits` also grants `edit`. Commands,
    fetches and writes outside the worktree are declined, with a message
    the model sees.
  - `plan`: nothing.

  Each decision is logged to `session.log`. The sessions also run the
  profile's model and Torque's planted agent: `OPENCODE_CONFIG_CONTENT`
  carries `model` and `default_agent`, which serve never got as flags.
  Before, they ran opencode's default model as its `build` agent.

  serve's raw output (its stdout and every SSE frame) stays in
  `session.log` with those decisions (CW-20261001-0148).
- Lines Torque appends to a session's `session.log` (redacted stderr, a
  failed first turn's capture, OpenCode permission decisions) are no longer
  overwritten by the agent's later output. agentkit v0.19.1 opens the log
  for append (CW-20261001-0158).
- Thinking from Claude, Codex, OpenCode and Pi over ACP is recorded as
  thinking, not as the agent's output. Their ACP thought chunks are marked
  only `phase: "thought"`, which Torque's event sink did not read
  (CW-20261001-0120).
- A Codex app-server session whose output reader fails on a read error now
  reads as not alive (agentkit v0.14.1), so Torque's session poller stops it
  and its session row goes terminal. Before, the reader stopped silently and
  the row stayed running.
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
- An `opencode run` session with a long task description launches. Its
  first turn carries `boot.md`'s content as one argument, which Linux
  refuses past 128 KiB ("argument list too long"); above 100 KiB the turn is
  `Boot @<boot dir>/boot.md`, an absolute path, and Torque logs why
  (CW-20261001-0121).
- A session whose first turn fails during start-up (an opencode, codex exec
  or agy run on the go-agent-wrapper path) reports why. The run's error now
  carries the provider's error line and the stderr tail (at most 2 KiB) after
  "process exited 1", and the turn's output reaches `session.log` and
  `stream.jsonl`, which stayed empty before (CW-20261001-0105).
- A session no longer persists a credential its agent CLI echoes. The
  value of every secret-named variable in the launch env (`*TOKEN*`,
  `*API_KEY*`, `*SECRET*` and the like, from the session's env, the
  daemon's env and the mux env) is replaced with `[redacted]`, matching
  exactly, in the run's error, `session.log`, the per-run stderr log,
  `stream.jsonl` and the events the executor records (CW-20261001-0123).
- A profile's `args` may not contain `--` or start with a non-option: they
  go among the agent CLI's options, ahead of the `--` before the prompt, where
  either would turn flags into prompt text. `profiles.yaml` loading and
  `torque profiles lint` both reject them and name the profile and argument
  (CW-20261001-0121).
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
- Tests no longer fill the shared `$TMPDIR`. Each package's test binary
  runs in a temp root of its own (`testenv.RunWithAgentShims`, through
  `$TMPDIR`), which holds its boot dirs (`torque-boot`), run stderr
  sidecars (`torque/runs`), `t.TempDir()`s and agent CLI shims, and is
  removed when the binary exits. Before, test boot dirs stayed in the host's
  `/tmp/torque-boot` (1.6G on the overnight host's 16G tmpfs), and every
  helper process a test re-executes left a `torque-agent-shims-*` dir behind
  (456 of them). A package whose tests leave a boot dir in their root now
  fails, naming the dir and what was planted in it (CW-20261001-0144).
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

- A Claude worker no longer gets the `mux` MCP aggregator by default
  (CW-20261001-0226). The daemon planted `mux mcp --proxy --servers
  vanta,torque,cerberus` for every Claude session, `cerberus` (deploy and ssh
  on hosts) being the riskiest of those, and recent worker sessions never
  called mux, only their loopback. Claude sessions now get the run's loopback
  alone, on every runtime kind and role. A profile grants mux servers
  deliberately with the new optional `mux_servers` field, which plants `mux
  mcp --proxy … --only <exactly those>` with the daemon's other mux arguments:
  `mux_servers: [vanta, tesseract]`. `--only` is what restricts mux: it is
  mux's curated mode, with those servers' tools and none of mux's own, where
  `--servers` would still leave `mux_call` open to every server in mux's
  catalog and mux's Tether tools (session launch, send input, message send) on
  the planted token and scopes. Names are checked against the known mux servers
  at profile load (an unknown, empty or repeated name is an error), and `torque
  profiles lint` reports the same. `cerberus` (deploy, ssh) and `nanite` (its
  `dev_bash`, `python_run` and `dev_write` run commands and write files on the
  host) are only ever planted when a profile names
  them, and naming either warns at load and in the lint, saying what it grants
  (a warning, which does not fail the lint). While Torque's state is
  write-protected a profile's `mux_servers` lose `torque`, and a profile that
  names servers on a daemon with no mux is told so in the boot log. Codex and
  every ACP runtime still get mux only under `permission_mode:
  bypassPermissions`; OpenCode still gets it by default; `mux_servers` narrows
  their sets. Where a session gets the daemon's default set (OpenCode, Codex and
  ACP under bypass), the same servers are planted with `--only`, so those
  sessions lose `mux_discover`, `mux_call` into the rest of mux's catalog and
  mux's own Tether tools. A Claude session over ACP (`runtime_kind: acp-stdio`) is not
  covered by `--strict-mcp-config`, whose bridge takes no such flag: it gets no
  default mux, and is warned about at launch and in the lint. Planting `--only`
  needs mux v0.6.0 or later. The planted
  kickoff no longer tells a session to prefer its loopback "over
  `mcp__mux__torque_*`" as though it had a mux server: it says so only if the
  session also has one. See docs/agent-execution-environment.md.
- A Claude agent Torque launches loads only the MCP servers Torque plants,
  through `--mcp-config <boot dir>/.mcp.json`. Torque now adds
  `--strict-mcp-config`, which stops Claude also loading the operator's
  user-level `~/.claude.json` `mcpServers`. Before, every launched Claude
  worker also got whatever was configured there: on an operator's machine
  the interactive `mux` aggregator with all its servers (cerberus deploy and
  ssh among them) and any other server, outside the per-server allow-list and
  the planted-only intent of the sandbox work. It applies to every native Claude
  runtime kind (streaming-stdio and subprocess-per-turn) and role, workers,
  planners and reviewers alike, on every turn and on a resume; the planted
  loopback is unchanged. Claude over ACP is not covered (see below). Interim: go-providers' Claude launch is to carry the
  flag itself. `TORQUE_CLAUDE_STRICT_MCP=0` (or `false`, `off`, `no`) turns it
  off, with a WARN naming the value logged at each launch; any other value,
  a typo included, keeps it on (CW-20261001-0226).
- The codex launches Torque leaves to codex's own sandbox are fewer
  (CW-20261001-0256). Three families of codex profile args still skipped
  Torque's write protection while codex ran unsandboxed: an attached short
  option (`-s=danger-full-access`, `-sdanger-full-access`); mixed selectors,
  where `--yolo --sandbox read-only` and `--sandbox danger-full-access -c
  sandbox_mode="read-only"` came out read-only although codex ranks the
  bypass and `--sandbox` flags above `-c`; and an app-server bypass mode
  hard-coded instead of taken from the function the adapter uses. Now a bypass
  flag anywhere wraps the launch, selectors must all agree on a confining mode
  or the launch is wrapped, any single-dash argument with an attached value
  or a selector with no value wraps, and the bypass mode comes from agentkit's
  `ResolveCodexPolicy`. Only a profile that positively selects `read-only` or
  `workspace-write` is left to codex's own sandbox. The orchestrator-class
  kickoff names the task id parameter as the tool's schema does (`id` on some
  tools, `task_id` on others).
- Agents can no longer write Torque's state directories (CW-20261001-0141).
  An agent running as the operator's uid could otherwise rewrite Torque's
  database or profiles to grant itself authority. Every agent launch
  write-protects the following, through go-agent-wrapper's and agentkit's
  `ProtectedPaths` (go-sandbox v0.5.1, which also stops an agent from
  renaming them aside):
  - the data, state and config dirs;
  - the directories of the main and queue databases;
  - the directory of the profiles file the daemon reads;
  - the session workspaces root and `~/.torque`;
  - the agent and end-agent template dirs when overridden.

  A missing one is created 0700 first, and startup logs each as protected
  or skipped. It stops direct writes only: `~/.bashrc`, systemd user units,
  git hooks and `systemd-run --user` remain same-uid routes.
  - It fails closed. Each of these refuses every agent launch, logged at
    startup:
    - a sandbox backend that cannot write-protect;
    - nothing left to protect;
    - a directory reached through a symlink the agent could re-point;
    - a directory that is, or contains, a shared one (`/`, the home
      directory or an ancestor, `/tmp`, `/var/tmp`, the temp dir), such as
      `TORQUE_DB_PATH=/tmp/x.db`, which would make it read-only to every
      agent.

    An ACP launch is refused ("ACP sandbox protect not yet supported
    (CW-20261001-0162)").
  - While it is on, the planted `mux` server proxies no `torque` server.
    Inside the sandbox `torque mcp` cannot write its database. The
    session's loopback carries the task's Torque tools, and the kickoff says
    so. Orchestrator-class roles keep the full surface on their loopback,
    where the tools are `mcp__loopback__torque_*` instead of
    `mcp__mux__torque_*` and take an explicit `task_id`; their kickoff says
    so. Workers lose cross-task reads until CW-20261001-0199.
  - A codex launch that positively selects codex's own sandbox (`read-only`
    or `workspace-write`: the planted default or `--sandbox`, `-c
    sandbox_mode=`, `--full-auto`) is not wrapped: codex's sandbox cannot
    start inside Torque's, and it already confines writes. The skip fails
    closed: `--yolo`, `--dangerously-bypass-approvals-and-sandbox`, any other
    sandbox mode, and any argument bearing on the sandbox or permissions
    that Torque does not read (`default_permissions`,
    `sandbox_workspace_write.*`, `--add-dir`, `--profile`) leave the launch
    wrapped.
  - Under the protection, nested sandboxes (bubblewrap- or `unshare`-based
    tests, Chromium's sandbox) cannot start.
  - The profiles watcher reloads only while the profiles directory is still
    the one protected at startup, by device and inode.
  - `TORQUE_SANDBOX_PROTECT=0` turns it off, with a startup warning that
    shows the value. An unrecognised value (`disable`) leaves it on and
    warns.
- `torque mcp` no longer sweeps orphaned sessions at startup; only
  `torque serve`, which owns them, does. A `torque mcp` in another PID
  namespace saw every live session as dead (CW-20261001-0141).
- The git Torque runs itself, outside any sandbox, in repositories agents
  can write no longer runs commands their config plants (CW-20261001-0141):
  - Every daemon git carries `core.fsmonitor=false`,
    `core.hooksPath=/dev/null`, `protocol.ext.allow=never` and
    `submodule.recurse=false`.
  - The repository's own filter drivers are emptied.
  - The per-run worktree's best-effort `fetch origin` is skipped when the
    repository's config sets a credential helper, `core.sshCommand`,
    `core.gitProxy`, a remote's `uploadpack`, a URL rewrite or a protocol
    policy.
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
