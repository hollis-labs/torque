# Handoff — Torque controlled test runs

**For:** an agent helping the operator stand up real Torque task runs.
**Goal:** dispatch easy / low-risk chores through the live Torque scheduler,
in controlled rounds — manual first, then flip selected things to auto. Build
confidence in the real system before widening scope.

You (the helper agent) drive setup and inspection. The operator decides what
flips to auto and when. **Default to caution: nothing auto-dispatches unless a
human said so.**

---

## 1. The system as it stands

Torque is one binary, one port. `torque serve` hosts the API, the scheduler,
and the GUI together.

- **Daemon:** `torque-api-service`, Cerberus-managed (launchd), repo
  `/Users/chrispian/dev/hollis-labs/apps/torque`.
- **API:** `http://localhost:8990/api/v1`  ·  **GUI:** `http://localhost:8990`
- **Health check:** `curl -s localhost:8990/api/v1/scheduler/status` → JSON
  with `enabled`, `max_workers`, `active_workers`, `queue_depth`.
- **Daemon log:** `~/.cerberus/apps/torque/torque-api-service/logs/stderr.log`
  — scheduler picker ticks + dispatch lines land here; tail it during a run.
- **Restart** (after a `profiles.yaml` edit, see §3): via Cerberus
  (`cerberus` CLI or the `cerberus_resource_*` MCP tools) — resource id
  `torque-api-service`.

**How you interact:** the `torque_*` MCP tools (preferred — self-documenting;
run `mux_discover intent="..."` to find them), the HTTP API, or the GUI for
visual inspection. Key tools: `torque_task_create`, `torque_task_update`,
`torque_task_list`, `torque_task_get`, `torque_scheduler_toggle`,
`torque_comment_add`, `torque_project_list`.

**Reference:** `cmd/torque/serve_live_smoke_test.go` is the canonical
end-to-end example — it creates a task, dispatches it through the scheduler,
and asserts the result. Read it to see every field a real run uses.

---

## 2. Providers — claude & codex

Both providers are **currently working** on this machine (the live smoke is
4/4 green). Setup pointers if that changes:

### claude
- Binary `claude` on `PATH` (currently `~/.local/bin/claude`).
- Auth, in order of preference: an existing `~/.claude.json` session (run
  `claude` once interactively and log in); or `ANTHROPIC_API_KEY` in the
  daemon env; or the `torque-apikey-helper` binary (keychain-backed — Torque
  threads it into the planted `.claude/settings.json`).
- Torque drives claude via the `claude-code` provider, `streaming-stdio`
  runtime (a long-lived `claude -p --input-format stream-json` process).

### codex
- Binary `codex` on `PATH` (currently `/opt/homebrew/bin/codex`).
- Auth: `codex login` (codex manages its own credentials).
- Torque drives codex via the `codex` provider, `jsonrpc-stdio` runtime
  (the `codex app-server`).

### Verify both at once
Run the live smoke — if it passes, both providers **and** the dispatch
pipeline work:
```
cd /Users/chrispian/dev/hollis-labs/apps/torque
go test -tags smoke -run TestServeLiveSmoke ./cmd/torque/ -v -timeout 900s
```
4 subtests (claude+codex × shared+worktree). A provider auth failure shows as
`BLOCKED(environment)` — re-login that provider. A pipeline break is a hard
`FAIL`.

⚠️ The smoke proves providers + the dispatch pipeline — it does **not** prove
the running daemon has profiles loaded. It sets `TORQUE_PROFILES_PATH` to its
own fixture, so it bypasses the daemon's default profiles file entirely. A
green smoke with an empty daemon profile map is possible — check §3 separately.

---

## 3. Agent profiles

A **profile** is the runtime binding a task dispatches against: which provider,
model, runtime, timeout, and permission posture.

### Where the profiles file lives — read this carefully

Torque resolves its on-disk layout via **go-apppaths** (XDG). The profiles
file lives under the config root, so the **canonical path is**:

```
~/.config/torque/profiles.yaml
```

Run `torque path` to print the resolved layout (data/state/config roots, main
DB, `profiles`, `queue-db`) — that is the authoritative answer for the machine
you are on.

This is the file to create and edit. A `TORQUE_PROFILES_PATH` env var would
override the location; the intended home is `~/.config/torque/profiles.yaml`.

**Current state:** `~/.config/torque/profiles.yaml` does not exist yet, so the
daemon runs with an **empty profile map** — every dispatch candidate skips
with `empty_profile` and nothing can run. Creating this file is Round 0 and is
what unblocks all dispatch. (This is expected — Torque's own agent profiles
haven't been authored yet.)

Loaded at daemon startup; **after editing it, restart `torque-api-service`**
via Cerberus. Confirm the load in the daemon log
(`~/.cerberus/apps/torque/torque-api-service/logs/stderr.log`): look for
`loaded N profile(s) from …/torque/profiles.yaml` — a `no profiles file at …`
line means the map is still empty.

### Reference profile shapes — for content guidance only

The repo carries no top-level `profiles.yaml`. Working example profile shapes
live in the smoke fixture `cmd/torque/testdata/serve-smoke-profiles.yaml`
(`default` + `codex-long`). Treat the table below as a starting point for what
to author into `~/.config/torque/profiles.yaml`:

| Profile | Provider | Model | Runtime | Notes |
|---|---|---|---|---|
| `default` | claude-code | claude-sonnet-4-5 | streaming-stdio | carries `--dangerously-skip-permissions` → **full bypass** |
| `codex-long` | codex | gpt-5.4 | jsonrpc-stdio | 3h timeout |
| `opencode-default` | opencode | — | subprocess | — |

### `permission_mode` — the safety knob for claude
Controls what the spawned claude may do without asking (planted into
`.claude/settings.json` as `permissions.defaultMode`):

- `plan` — read-only, no edits. **Safest for first runs.**
- `acceptEdits` — auto-approves file edits, still gates destructive shell ops.
  The safe non-interactive middle ground; the default when the key is omitted.
- `bypassPermissions` — full bypass. This is what the `default` profile gets
  via its `--dangerously-skip-permissions` arg.

⚠️ The reference `default` profile runs claude in **full bypass**. For
controlled low-risk testing, **do not copy it as-is**. Author a test profile
into `~/.config/torque/profiles.yaml`:

```yaml
# ~/.config/torque/profiles.yaml  (create this file — it does not exist yet)
agent_profiles:
  test-claude:
    executor: cli
    provider: claude-code
    command: claude
    model: claude-sonnet-4-5
    output_format: stream-json
    timeout_seconds: 600
    runtime_kind: streaming-stdio
    permission_mode: acceptEdits   # or `plan` for the very first runs
```
codex's posture rides go-providers' non-interactive `never` default — no
codex-side knob in the Torque profile model today; sandbox the working dir
instead (§5).

---

## 4. How a task run works

```
torque_task_create → task (manual=true, todo)
   → you flip manual=false (torque_task_update)
   → scheduler picker picks it  → worker dispatches → agent runs one turn
   → run record + task lifecycle transition (doing → review|done|failed|blocked)
```

Task fields that matter:
- **`description`** — becomes the agent's prompt (the one-shot turn).
- **`agent_profile`** — which profile (§3). Required for dispatch.
- **`working_dir`** — where the agent runs. **Scope your blast radius here.**
- **`on_done`** — `review` (default: `doing → review`, pauses for you to
  inspect) or `close` (`doing → done` on success). Use `review` for controlled
  tests.
- **`kind`** — leave default `agent`. (`parent`/`plan`/`issue` never
  auto-dispatch.)
- Budgets — `cost_budget`, `token_budget`, `max_duration_ms`, `max_retries`.

**The manual gate:** every `torque_task_create` / HTTP create forces
`manual=true` (safety override CW-20260417-0133). A manual task is **never**
auto-dispatched. Flipping `manual=false` is the deliberate "let it run" act.

**The scheduler switch:** `torque_scheduler_toggle enabled=false` pauses ALL
auto-dispatch session-wide; `enabled=true` resumes. This is your master
control for the rounds below.

**Per-run worktree (optional isolation):** with the daemon env
`TORQUE_WORKTREE_PER_RUN=true`, each run executes in an ephemeral git worktree
(a sibling of the repo) instead of the working dir itself — the run can't
touch your checked-out branch. Requires `working_dir` to be a git repo with an
`origin` remote. Off by default.

---

## 5. Safety levers (use them)

| Lever | Effect |
|---|---|
| `manual=true` (default on create) | task won't auto-dispatch — the per-task gate |
| `torque_scheduler_toggle enabled=false` | pauses all auto-dispatch |
| `working_dir` = a sandbox / throwaway dir | contains the blast radius |
| `TORQUE_WORKTREE_PER_RUN=true` | each run isolated in an ephemeral git worktree |
| `permission_mode: plan` / `acceptEdits` | claude can't run wild |
| `cost_budget` / `max_duration_ms` | caps a runaway run |
| per-project concurrency | scheduler runs ≤1 task per `project_id` at once |

**For the first rounds, run tasks in a dedicated sandbox directory** — e.g.
`mkdir -p ~/torque-sandbox && git init` there — not a real project. The
smoke-test approach (ask the agent to create a marker file, then assert it
landed) is the cleanest first proof.

---

## 6. The controlled rounds

Run these with the operator. Don't advance a round until the previous one is
clean and the operator says go.

### Round 0 — Preflight
- Daemon healthy (§1 health check); claude + codex authed (§2 smoke).
- Create a sandbox: `~/torque-sandbox` (a `git init`'d dir for worktree rounds).
- **Create `~/.config/torque/profiles.yaml`** (§3 — confirm the path with
  `torque path`) with the `test-claude` profile — the daemon currently has an
  empty profile map, so until this file exists every dispatch skips with
  `empty_profile`. Restart `torque-api-service`, then confirm
  `loaded N profile(s)` in the daemon log.
- Pick the project_id if you want tasks scoped (`torque_project_list` — Torque
  is `PRJ-20260416-0001`); project scoping is optional for sandbox runs.

### Round 1 — One claude task, hand-dispatched
1. `torque_scheduler_toggle enabled=false` — pause auto-dispatch.
2. `torque_task_create`: title + a trivial chore `description` (see chore list
   below), `agent_profile: test-claude`, `working_dir: ~/torque-sandbox`,
   `on_done: review`.
3. Inspect the created task (`torque_task_get`) — confirm fields.
4. `torque_task_update` → `manual=false`. (It still won't run — scheduler is
   paused.)
5. `torque_scheduler_toggle enabled=true` — the picker dispatches exactly this
   one task. Tail the daemon log.
6. Inspect: `torque_task_get` (status → `review`), the run record
   (`GET /api/v1/runs?task_id=...` — `Status`, `ExitCode`, `Cost`,
   `ErrorMessage`), artifacts (`torque_artifact_list`), and the actual
   filesystem change in the sandbox. Confirm it did exactly the chore.

### Round 2 — Repeat with codex
Same flow, `agent_profile: codex-long`.

### Round 3 — Worktree isolation
Set `TORQUE_WORKTREE_PER_RUN=true` in the daemon env + restart. Use a
git-repo working dir with an `origin`. Run a chore; confirm the change landed
in the per-run worktree sibling, not the main checkout.

### Round 4 — Let a few auto-dispatch
Create 2–3 chore tasks, all `manual=false`, scheduler enabled. Watch the
picker dispatch them (one per project at a time). This is the first real
"auto mode" — still trivial chores, still sandboxed.

### Then widen
Bigger chores, real (non-sandbox) repos with worktree isolation on,
`on_done: close`, higher concurrency — one dimension at a time.

### Low-risk starter chores
- "Create a file `HELLO.md` in the working directory containing one line: OK."
  (the marker-file proof — pipeline only)
- "List the files in this directory and write them to `INVENTORY.md`."
- "Add a one-line doc comment above the function `X` in `Y` — change nothing
  else."
- "Fix the typo on line N of `README.md`."
- "Summarize what `<file>` does in 3 bullet points in `NOTES.md`."

Keep them read-mostly or single-file-write, and explicit ("change nothing
else", name the file) — same discipline as the smoke prompt.

---

## 7. Inspecting a run

- **Task state:** `torque_task_get` → `status` (`todo→doing→review|done|
  failed|blocked`), `blocked_reason`.
- **Run records:** `GET /api/v1/runs?task_id=<id>` → `Status`, `ExitCode`
  (0 = clean), `Cost`, `ErrorMessage`.
- **Artifacts:** `torque_artifact_list`.  **Comments:** `torque_comment_list`.
- **GUI:** `http://localhost:8990` — visual task/run view.
- **Daemon log:** the `stderr.log` — `[picker] tick=... picked=N
  skipped_by_reason=...` tells you why a task did or didn't dispatch.

---

## 8. Gotchas

- **Create always forces `manual=true`.** A task won't dispatch until you flip
  `manual=false` AND it has an `agent_profile`.
- **Picker skip reasons:** if a task isn't picked, the log's
  `skipped_by_reason` map says why — `manual`, `empty_profile`,
  `project_busy`, `dep_unmet`.
- **Profiles file is `~/.config/torque/profiles.yaml`** (the go-apppaths
  config root — run `torque path` to confirm). `TORQUE_PROFILES_PATH`
  overrides it. It loads at startup; restart the daemon after editing it.
- **Provider auth failures** surface as run `ErrorMessage` with
  auth/login/credential/401 signatures — re-login that provider; it's not a
  Torque bug.
- **Worktree mode** needs `working_dir` to be a git repo with a published
  `origin` (the per-run worktree branches from `origin/main`).
- **`torque_session_create` is NOT the task path** — it boots a long-lived
  agent session. For chore runs, always use the task → scheduler path.
- Scheduler toggle is **session-scoped** — it resets to the config default on
  a daemon restart.

---

## 9. First-session checklist

- [ ] Daemon healthy; claude + codex authed (run the smoke).
- [ ] Sandbox dir created (`~/torque-sandbox`, `git init` for worktree rounds).
- [ ] `~/.config/torque/profiles.yaml` created with the `test-claude` profile
      (`permission_mode: acceptEdits` or `plan`), daemon restarted, the daemon
      log shows `loaded N profile(s)` (not `no profiles file at …`).
- [ ] Scheduler paused (`torque_scheduler_toggle enabled=false`).
- [ ] Round 1 task created, inspected, flipped, dispatched, verified — with
      the operator watching.
- [ ] Operator says go before each subsequent round.
