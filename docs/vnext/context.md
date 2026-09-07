# vNext context

What was read, what the neighbouring apps solved, and what was ruled in the
2026-09-07 session. A following session should be able to start here rather
than re-deriving any of it.

## Why this session happened

Torque's own agent/launch stack was built when Torque was unstable and nothing
else in the portfolio could be relied on. Since then the portfolio changed
underneath it:

- The shared `hollis-labs` libraries landed and Torque was brought current on
  them (`agentkit v0.6.1`, `go-agent-wrapper v0.10.1`, `go-messaging v0.5.0`,
  `go-providers v0.26.0`, …).
- Tangent solved human-in-the-loop properly.
- Hadron's workflow engine was extracted into `go-workflow` as an embeddable
  library.
- Nanite did the deepest work on steering, harness, alignment, recovery, and
  long-running sessions.
- Cairn became the reference for composition and materialization.
- Tether is landing the messaging system and will be able to host CLI-agent
  launches for anyone, Torque included.
- `agent-setup` demonstrated that leading an agent with prose beats gating it
  with rules, and that hard gates belong only where they must be.

The question this session set out to answer is what Torque should be given all
of that — not what it can be patched into.

## Torque as it stands today

Measured, not remembered:

| Thing | Size |
|---|---|
| Total Go | ~113,500 LOC |
| `internal/runtime` | 35,000 (scheduler 12.5k, agent 12.2k, bootstrap 2.6k, steering 2.0k, stuck 1.9k) |
| `internal/mcpadapter` | 16,500 — **122 distinct MCP tools** |
| `internal/persistence` | 12,300 across 31 migrations |
| `internal/service` | 11,600 |
| `internal/httpserver` | 9,300 |
| `internal/messaging` + `federation` + `broker` | 4,900 |
| `internal/hitl` | 658 |

The `tasks` table carries **49 columns**. One mutable row is simultaneously the
work item (`title`, `description`, `kind`, `status`, `priority`), the execution
contract (`system_prompt`, `tools`, `permissions`, `environment`, `agent_file`,
`launch_profile`, `working_dir`, budgets), the dispatch policy (`manual`,
`on_done`, `on_fail`, `on_review`, `on_done_merge`, `escalation_chain`), the run
state (`retry_count`, `escalation_step`, `last_run_id`), and four JSON blobs
(`deliverables`, `quality_gates`, `metadata`, `subtodos`).

The FSM is seven lines in `internal/service/task.go`:

```go
"todo":    {"doing", "blocked", "paused", "archived"},
"doing":   {"review", "done", "blocked", "paused", "todo", "archived"},
"review":  {"done", "doing", "todo", "blocked", "paused", "archived"},
"blocked": {"todo", "archived"},
"paused":  {"todo", "archived"},
"done":    {"archived"},
```

So `blocked` cannot resume into `doing`, `done` cannot reopen, and `todo` cannot
reach `done`. This is filed as **CW-20260903-0070** — explicitly a placeholder
awaiting this conversation, and explicitly noted as recurring friction across
multiple sessions and dates. `agent-setup`'s stop-disposition hook hardcodes the
same fixed path in its user-facing message and will need revisiting.

## Prior art in the repo

- **[`docs/work-coordination-direction.md`](../work-coordination-direction.md)**
  (2026-08-22, ~1,500 lines, status *draft*). A thorough boundary analysis with
  a portfolio axiom, a responsibility-ownership table, a proposed domain model,
  lifecycle separation, and twenty-eight named architectural tensions. Read in
  full this session. Its Tesseract record (`hollis_labs_torque_direction_draft`)
  is also `status: draft`. It independently reached most of the same
  conclusions this sketch reaches, which is corroboration rather than
  inheritance.
- **CW-20260904-0117** — "Planning brief — Torque standalone and composed agent
  execution", currently `doing`. Sketches a compatible layering and is
  explicit that it is a working hypothesis requiring a reality check and a
  final architecture review before implementation.
- **CW-20260904-0166** — persist the compiled launch as an immutable dispatch
  binding. Directly implemented by this sketch.
- **CW-20260904-0090** — unify the runtime and plugin executor contracts. Also
  directly implemented.
- **CW-20260902-0136** — `quality_gates` is fully modeled in the schema and the
  scheduler never reads it. A live example of the modeled-but-inert problem.

## What each neighbour actually solved

Read in-tree this session, not recalled.

### Cairn — materialization

`~/dev/projects/cairn`. Reads a bundle, resolves a profile through an `extends`
cascade, renders a provider-native boot directory. Explicitly does *not* launch,
monitor, track, control, or have an opinion about how agents behave.

The transferable shape:

- **A directory, not a file.** `spec.trees` copies a whole source directory into
  the boot dir *with its shape intact*, as the bulk counterpart to a single
  `files` entry. `static_dir` is deliberately not the answer — it concatenates,
  which is what a slot wants and the opposite of a copy.
- **Templates own the document.** `AGENTS.md` is one template with slot markers
  (`<!-- cairn:slot role -->`); reordering the rendered document is a one-line
  edit in one file, not a property of the cascade.
- **Settings owned by key, not in full.** In a home the harness also owns, Cairn
  writes the keys the profile declares and leaves every other key alone —
  byte-for-byte, at every depth. `permissions.defaultMode` is Cairn's; a rule
  the client writes beside it is not.
- **A boot report is the JSON contract.** `cairn show --json` nests each key as
  `{value, contributors}` — provenance is per key, never per member.
- **Provider is a render target, not an identity.** `settings` is keyed by
  provider; templates, slots, skills and prompts stay neutral.

### agent-setup — leading rather than gating

`~/dev/projects/agent-setup`. Chrispian's live agent configuration, and Cairn's
catalog. The standing default is ~60 lines of prose.

- **`RUN CONTEXT:`** is the one line that tells an agent whether a human is
  reading. `user-cli` means report briefly and ask on a material blocker;
  `runtime-managed` means do not assume anyone reads each turn, and finish or
  fail through the runtime's own checkpoint and report calls.
- **"A role is not a launch mode."** The same profile boots into a session of
  its own or is dispatched inside one; a planted definition is a whole role
  narrowed to a dispatch, not a different role.
- **Verification fits the work**, judged against maturity, exposure and
  consequence — not a fixed gate.
- Open-ended work succeeds on alignment with intent, not adherence to a rigid
  system. Hard gates exist, and only where they must.

### Nanite — steering, harness, recovery

`~/dev/hollis-labs/apps/nanite`.

- **Reflexes** (`internal/agent/reflexes`): DB-backed, class-bound seeds,
  predicate/event/interval triggers, actions `inject_reminder`, `halt_session`,
  `send_message`, `force_tool_choice`, `add_schedule`. Per-agent opt-out, with a
  `Required` flag that removes opt-out for exactly the three `halt_session`
  kill switches.
- **The conjunction principle**, stated as a hard design constraint: *single
  detectors false-positive.* The canonical example is healthy idle compression
  versus a cache-miss echo attractor — both show "output shorter than prior
  pass", one is fine and one is a runaway. Every predicate must be a
  conjunction over multiple independent signals, and there is a named test
  encoding it.
- **Host runtime feed** (`docs/architecture/host-runtime-feed.md`): bounded,
  allowlisted, with independent cursor / retention / gap / redaction semantics.
  **The source event is never persisted** — prompts, tool payloads, stdout,
  stderr and model deltas may contain secrets. Only metadata and explicitly-safe
  per-kind state.
- **Recovery**: interrupted-turn repair, orphan sweep, recovery packs, boot-dir
  repopulation without re-rolling the path, and a shutdown that closes the
  admission boundary *first* so a concurrent boot cannot escape the stop set.

### Tangent — human-in-the-loop

`~/dev/hollis-labs/apps/tangent`.

- **Idempotent open.** `OpenSurface` takes a caller scope plus an idempotency
  key and returns the same durable handle for a retry of the same canonical
  request.
- **Resumable completion.** A workflow's wait is not the work. Answered within
  45 seconds it returns inline; past that it returns a **successful pending
  receipt carrying a durable handle** — never an error, never a cancellation.
  Transport loss, caller timeout, browser disconnect and process restart stop
  only the waiter.
- **Authority is a session, not a URL.** A room URL is a locator; browser
  authority lives in an `HttpOnly` participant session and caller authority is a
  host-assigned scope over a seven-capability matrix.
- **The surface is a projection.** The interaction is canonical; rooms and
  envelopes are a projection of it and never a terminal-state authority.

### Hadron — the embeddable engine

`~/dev/hollis-labs/apps/hadron` and `~/dev/hollis-labs/libs/go-workflow`.

The engine is already a library: `graph`, `compile`, `runtime`, `wait`, `gate`,
`values`, `verification`, `stepkind`, `conformance`. The host owns durable
storage, scheduling, auth, policy, transport, UI, and the concrete step kinds it
enables.

Hadron uses **no plugin SDK at all**. Extensibility is `stepkind.StepKind`
values constructed and registered at the composition root
(`cmd/hadrond/workflow_runtime.go`), plus a **declared production boundary** —
`productionWorkflowKindBoundary()` / `RequiredKinds` — naming exactly the six
kinds the stock daemon exposes. Other adapters in the repo are "embeddable
contracts, not capabilities advertised by the stock daemon."

### Nanite — the plugin scaffold

`plugin-sdk v0.3.0`. Host-neutral `Plugin` contract plus a `subprocess` package
implementing JSON-RPC 2.0 over stdio with capability interfaces
(`CommandHandler`, `EventHandler`, `CRUDHandler`, `MCPHandler`, `HTTPHandler`,
`Migrator`, `HealthChecker`), config/data/cache helpers, and a stderr JSON-lines
logger with secret redaction.

Nanite's host wraps it: `internal/plugin/builtin/<id>/` is a package with an
embedded `plugin.yaml` and an `init()` calling
`hostplugin.RegisterPlugin(id, constructor)`; `internal/plugin/allplugins` blank
-imports every one; `cmd/nanite` blank-imports `allplugins`. Third-party plugins
are subprocess, fetched from a signed catalog with Ed25519 verification, and a
`devmode` build tag that disables verification and must never ship.

### Tether — the fabric

Messaging vNext landed T01–T12 during the first week of September:
durable participant registry, canonical sessions, leased runtime bindings,
reliable go-messaging delivery, scoped role/participant bindings with durable
fanout, published-local bridges, CLI/MCP/Go-client parity, delivery trace and
operator repair, a bounded A2A interoperability adapter, and a platform-ready
cutover guide. Combined with sessions/launches work, Tether will be able to host
launches for CLI agents so agents outside Torque can work together.

`CW-20260904-0103` is the Torque-side adoption task and is still `todo`.

## Session rulings

Settled with Chrispian on 2026-09-07. These constrain the sketch. They are not
ADRs; a following session may reopen any of them deliberately.

1. **Core identity is coordination and dispatch only.** Even the agent executor
   is a first-party plugin over the executor contract.
2. **Fixed canonical FSM plus a project overlay.** Torque keeps a small
   canonical work FSM with permissive transitions; project configuration adds
   required steps, labels and gates on top. Machinery reads the canonical state.
3. **Boot-directory materialization is selected by launch-provider capability.**
   Torque ships a built-in planter for the default path and defers when a
   provider declares it owns the directory.
4. **Cut the exposed surface hard, and drop the embedded GUI from core.**
5. **Packaging follows Nanite and Hadron's use of the shared plugin scaffold.**
   Nanite's registration mechanics; Hadron's declared stock boundary.
6. **Two plugin tiers.** Hot-path contracts are compiled-in Go constructors for
   first-party and embedders; cold-path contracts are subprocess `plugin-sdk`.
   Same declared contract and capability model on both tiers.
7. **Three records plus an append-only work-event log.** WorkItem /
   ExecutionPolicy / DispatchBinding, with current state as a projection over
   durable work events.
8. **Messaging transport leaves; the decision record stays core.** Federation
   and the envelope broker are deleted as Tether's job. The gate record — who
   was asked, quorum, deadline, authenticated response, how it applied to work
   state — stays core because it is coordination. Notification and mid-run
   steering become adapter contracts with a Tether implementation and a minimal
   loopback default.

Added after a first read of the sketch, correcting or extending the above:

9. **Ruling 1 is narrower than "coordination and dispatch only" first read.**
   "Executor" means the *actor*, not the *vehicle*. `agentkit` and
   `go-agent-wrapper` are the agent's **workspace**, and building and
   supervising that workspace is dispatch mechanics — it stays in core. What
   Torque does not own is the *agent*: identity, role, prose, skills,
   composition. The catalog leaves; the launcher does not. See
   [architecture §1.1](architecture.md#11-vocabulary).
10. **Start clean and import.** The vNext schema is greenfield; the live board
    crosses over through a re-runnable importer that **preserves `CW-*` IDs**.
11. **The GUI moves to its own repository.** The HTTP API becomes a versioned
    contract as a consequence.
12. **`go-workflow` is not the constrained thing an earlier draft implied.**
    Verified in-tree: `ForEachSpec` fan-out with concurrency limits, fail-fast
    and tolerated-failure policies over a 616-line durable `runtime/fanout.go`;
    `If` / `SwitchSpec` branching; six `ReadyRule` join semantics; `Call`
    sub-workflows; `CatchRule` / `FinallySpec` / `CompensationSpec`;
    memoization, verification, five-way timeouts, concurrency claims. Adapters
    ship for `http`, `llm`, `mcp`, `agent`, `script`, `cmd`, `transform`,
    `service`, `gate`, `wait`, `emit`, `checkpoint`, `call`. Hadron's stock
    daemon exposing six frozen kinds is a **host** choice — the library's own
    adoption guide says so in those words. Torque becomes a host and registers
    `torque.work@v1`.

## Operating constraints

- No external consumers. Not released. Backward compatibility is not a
  constraint on the sketch.
- The shared libraries can be changed to suit Torque where that is the better
  seam.
- Preparing for a build-in-public repo. The install story must be solid for
  developers; it does not need to be perfect.
- There is one live board of `CW-*` work items in daily use. Whether vNext
  migrates it or starts clean is an open question, not an assumption.
