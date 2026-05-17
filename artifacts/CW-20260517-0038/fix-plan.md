# CW-20260517-0038 — Stabilize the agent execution environment for Torque-orchestrated runs

Investigation + fix plan. **No code was modified.** Scope: the `torque` repo
(`github.com/hollis-labs/torque`). Companion repos (`go-providers`,
`go-agent-launch`) are referenced where the root cause lives outside torque.

## TL;DR

The recurring "new failure each run" pattern is not one bug — it is three
distinct gaps, two of which are genuinely fixable inside torque and one of
which is partly an external constraint:

| # | Variation | Classification |
|---|-----------|----------------|
| 1 | Agent-tool / mux subagent dispatch path force-creates worktrees, needs parent IDs | **Mixed** — the multi-agent dispatch path is *unwired dead code* in torque; the Claude Code Agent-tool / mux behaviors are external |
| 2 | `go.mod replace ../../` breaks at wrong worktree depth | **Mostly external** — torque's *live* per-run worktree path already places at sibling depth correctly; the breakage comes from Claude Code's Agent-tool worktrees under `.claude/worktrees/`. Torque can add a guard + docs. |
| 3 | Spawned `claude` agents start in `default` permission mode → unanswerable prompts | **Torque-fixable** — torque controls whether the planted `.claude/settings.json` carries a permission mode, but only via an all-or-nothing dev flag today |

The single highest-value, lowest-risk fix is **Variation 3**.

---

## Variation 1 — Multi-agent dispatch path force-creates worktrees / needs parent IDs

### Root cause

There are **two** worktree code paths in `internal/worktree/`, and they are not
the same path:

1. **`perrun.go` — the LIVE path.** `worktree.Spec.Resolve` →
   `SetupPerRun(opts, workingDir, runID)` (`perrun.go:56`). This is the only
   worktree path actually wired into dispatch — `internal/runtime/scheduler/scheduler.go:518`
   calls `s.worktreeSpec()` → `spec.Resolve(...)`. It is **opt-in**
   (`TORQUE_WORKTREE_PER_RUN`, default `false` — `internal/config/config.go:130`)
   and failure is non-fatal: the scheduler logs and falls back to
   `task.working_dir` (`scheduler.go:526-537`).

2. **`manager.go` — DEAD code.** `worktree.Manager.Create`
   (`manager.go:40`) creates a branch + worktree at
   `<repoPath>/.torque/worktrees/<taskID>` (`manager.go:51`). It is referenced
   *only* by `internal/runtime/scheduler/hooks.go` (`ConcurrencyHooks.OnRunStarted`,
   `hooks.go:22`). Grepping the repo (`grep -rn 'ConcurrencyHooks\|WorktreeManager' --include='*.go'`)
   shows **`ConcurrencyHooks` is never constructed or invoked by the scheduler** —
   `hooks.go` defines the struct and methods, nothing wires them in. So the
   `Manager`/`Merger`/`Resolver` "clean multi-agent dispatch" subsystem
   (merge policies, auto-resolve, conflict-resolution tasks) **has never run
   end-to-end** — matching the task report exactly.

The "force-creates a git worktree, which fails when the session dir isn't a
git repo" symptom has two contributing failure modes:

- `perrun.go:57` — `SetupPerRun` calls `FindRepoRoot(workingDir)` which walks
  up looking for `.git` and **returns an error** when none is found
  (`perrun.go:42-44`). `Spec.Resolve` propagates that error
  (`spec.go:113-116`); the scheduler catches it and falls back — so torque's
  *own* path degrades gracefully. The hard failure the task describes is the
  **Claude Code Agent-tool** path (external), which creates
  `.claude/worktrees/agent-*` unconditionally. Confirmed live in this very
  checkout: `git worktree list` shows two `locked` worktrees at
  `/Users/chrispian/dev/hollis-labs/apps/torque/.claude/worktrees/agent-ac94525a991a08682`
  and `.../agent-ad026572d5f53afb2`. Torque did not create these — the Agent
  tool did.

- mux `subagent_spawn` requiring nanite-substrate parent IDs: there is **no
  `subagent_spawn` / parent-ID code in the torque repo at all** (grep for
  `subagent`, `ParentID`, `parent_id` across `internal/` and `cmd/` returns
  only unrelated `parent` references — context parents, plan-tool parents).
  Torque dispatches agents through `internal/runtime/agent/Boot` →
  go-agent-launch → a single `claude`/`codex`/`opencode` subprocess. Torque
  has no notion of mux subagents.

### Proposed fix

This variation is mostly a **classification + cleanup** exercise, not a deep
bug fix:

1. **Decide the fate of the `Manager`/`hooks.go` subsystem.** It is dead,
   untested-in-integration code that creates worktrees at the *wrong*
   (nested `.torque/worktrees/`) depth (see Variation 2). Two options:
   - **(Recommended)** Delete `internal/worktree/manager.go`,
     `internal/runtime/scheduler/hooks.go`, and the `Merger`/`Resolver`
     `ConcurrencyHooks` wiring fields if nothing else uses them — or
   - Properly wire `ConcurrencyHooks` into the scheduler lifecycle and fix
     its depth bug (much larger effort; see Variation 2).
   Recommendation: delete. The live `perrun.go` path is the supported one;
   keeping a parallel dead path is exactly what produces "a new failure each
   run" when someone accidentally reaches for it.

2. **Document that torque does NOT spawn mux subagents.** Multi-agent
   dispatch in torque = multiple top-level scheduler runs, each its own
   `claude` subprocess via `agent.Boot`. The nanite/mux `subagent_spawn`
   parent-ID requirement is an **external constraint** that does not apply to
   torque-orchestrated runs; agents should not attempt `subagent_spawn` from
   within a torque run. This belongs in an agent execution environment doc
   (see Sequencing).

3. **Make the Claude Code Agent-tool worktree behavior an explicit
   non-goal / constraint.** When an *agent* running inside a torque run uses
   Claude Code's own `Task`/Agent tool, that tool's worktree creation is
   outside torque's control. The fix is to instruct agents (via the planted
   boot prompt / CLAUDE.md) not to use the nested Agent-tool worktree path
   for torque runs, and to rely on torque's per-run worktree instead.

### Affected files + effort

- `internal/worktree/manager.go` — delete (~225 LOC) or wire+fix
- `internal/runtime/scheduler/hooks.go` — delete (~91 LOC) or wire
- `internal/worktree/manager_test.go`, `merge_test.go` review for orphaned tests
- New doc `docs/agent-execution-environment.md` (shared with Var 2 & 3)
- **Effort: S** (delete path) / **L** (wire + fix path). Recommend S.

### Classification

**Mixed.** Dead-code cleanup is **torque-fixable**. The mux `subagent_spawn`
parent-ID requirement and the Claude Code Agent-tool worktree behavior are
**external constraints** — torque can only document them and steer agents away.

---

## Variation 2 — `go.mod replace ../../` breaks at wrong worktree depth

### Root cause

Two different worktree-placement strategies exist, at two different depths:

- **`perrun.go` (LIVE) — sibling depth, CORRECT.** `SetupPerRun`
  (`perrun.go:56-76`) resolves `repoRoot` via `FindRepoRoot`, then defaults
  the worktree root to **`repoRoot + "-worktrees"`** (`perrun.go:62-64`) — a
  *sibling* directory of the repo (`/path/to/repo` → `/path/to/repo-worktrees/run-<id>`).
  A relative `replace ../../libs/...` directive resolves from the worktree's
  `go.mod` location. The repo is at depth N; the worktree is at
  `repo-worktrees/run-<id>` = depth N+1 *under a sibling*, **not** under the
  repo. So `../../` from `repo-worktrees/run-<id>/go.mod` does **not** resolve
  to the same place as `../../` from `repo/go.mod`. This is a real latent
  mismatch even for the "correct" path — but in practice it has worked because
  per-run worktrees are off by default and torque's own `go.mod` has no
  relative replaces.

- **`manager.go` (DEAD) — nested depth, WRONG.** `Manager.Create`
  (`manager.go:51`) places worktrees at
  `<repoPath>/.torque/worktrees/<taskID>` — **two levels deep inside the repo**.
  A `replace ../../libs/...` directive that resolved correctly from
  `repo/go.mod` (→ `libs/`) resolves from
  `repo/.torque/worktrees/<taskID>/go.mod` to `repo/libs/` — i.e. it points
  back *inside the repo* instead of at the sibling `libs/` tree. This is the
  textbook "wrong directory depth" breakage in the task report. `WorktreeBaseDir`
  defaults to `".torque/worktrees"` (`types.go:62`).

- **External: Claude Code Agent-tool worktrees** at `.claude/worktrees/agent-*`
  — also nested *inside* the repo (confirmed live via `git worktree list`).
  Same depth-mismatch class as `manager.go`. Torque does not create these.

So: relative-replace breakage is caused by **nested-depth** worktree
placement. Torque's *live* path already avoids it (sibling depth), but the
dead `manager.go` path and the external Agent-tool path both reintroduce it.

### Proposed fix

1. **Eliminate the nested-depth path** — same as Variation 1, item 1: delete
   `manager.go` (or, if kept, change `WorktreeBaseDir` to a sibling-root
   strategy mirroring `perrun.go`).

2. **Make `perrun.go` worktree placement repo-aware and replace-safe.** The
   correct, predictable rule: a per-run worktree must sit at the **same
   directory depth as the repo root** so any relative `replace` directive
   resolves identically. Options, in order of robustness:
   - **(a) Mirror depth via sibling root (current default is close).** The
     current `repoRoot + "-worktrees"/run-<id>` is *one level deeper* than the
     repo. Change the per-run leaf so the worktree's `go.mod` is at the same
     depth as the repo's `go.mod` — e.g. place worktrees at
     `<repoParent>/<repoName>-worktrees-run-<id>` (a true sibling of the repo,
     same depth) instead of `<repoName>-worktrees/run-<id>` (sibling+1).
   - **(b) Detect relative replaces and refuse/warn.** Add a pre-dispatch
     check in `SetupPerRun` (or `Spec.Resolve`): parse the repo's `go.mod`,
     and if it contains `replace` directives with relative (`../`) targets,
     either (i) place the worktree at a depth that preserves them, or
     (ii) rewrite the worktree's `go.mod` replace targets to absolute paths
     pointing at the original repo's siblings, or (iii) emit a clear blocking
     error instead of a silent mis-resolve.
   - **(c) Document the constraint.** For repos with relative replaces, the
     supported configuration is `TORQUE_WORKTREE_ROOT` set explicitly to a
     path at the correct depth.
   Recommended: **(a) + (b-iii)** — fix the default depth, and add a guard
   that detects relative replaces and fails loudly rather than mis-resolving.

3. **Add a git-repo precheck.** `internal/runtime/scheduler/precheck.go`
   currently checks only context-window and tool-capability. Add an optional
   `Worktree PrecheckMode` gate: when per-run worktrees are enabled, verify
   `FindRepoRoot(task.WorkingDir)` succeeds *before* dispatch, so a non-git
   working dir produces a clean blocked-task reason instead of a mid-dispatch
   fallback log. (`SetupPerRun` already fails gracefully, but a precheck makes
   the failure visible and predictable rather than silent.)

### Affected files + effort

- `internal/worktree/perrun.go` — `SetupPerRun` depth change + relative-replace
  guard (~40-60 LOC + tests)
- `internal/worktree/spec.go` — thread the guard result through `Resolve`
- `internal/worktree/manager.go` — delete (shared with Var 1)
- `internal/runtime/scheduler/precheck.go` — add `Worktree` gate (~30 LOC)
- `internal/runtime/scheduler/scheduler.go` — call the new precheck gate
- `internal/config/config.go` — optional `TORQUE_WORKTREE_PRECHECK` env knob
- New/updated `docs/agent-execution-environment.md`
- **Effort: M** (depth fix + guard + precheck), **S** if only the depth fix.

### Classification

**Mostly external, with a real torque-side improvement available.** The
relative-replace breakage as *experienced* came from the external Claude Code
Agent-tool `.claude/worktrees/` placement and from the dead `manager.go`
path. Torque's live `perrun.go` path is close to correct and can be made
fully predictable (torque-fixable). Torque cannot change Claude Code's
Agent-tool worktree depth — that is an **external constraint** to document
and steer agents away from.

---

## Variation 3 — Spawned `claude` agents start in `default` permission mode

### Root cause

This is the cleanest, most clearly torque-relevant bug.

When torque dispatches a `claude-code` agent, the permission posture of the
spawned `claude` subprocess is determined entirely by **two** things, both
controlled by the *single* `--dangerously-skip-permissions` arg on the torque
agent profile:

1. **The adapter argv** — `provider.ClaudeAdapter.BuildArgs`
   (`go-providers@v0.18.0/provider/pty_claude.go:194`). In streaming-stdio
   mode (`InputMode == "stream-json"`, lines 253-269) the adapter emits
   `--dangerously-skip-permissions` **only when `a.SkipPermissions` is true**
   (line 263-265). `SkipPermissions` is set only by
   `NewClaudeAdapterDevStreamingStdio()` (line 188-190), which torque selects
   only when `profileIsDevMode(profile)` is true — i.e. when the profile's
   `Args` literally contains `--dangerously-skip-permissions`
   (`internal/runtime/agent/factory.go:80-83`, `156-163`).

2. **The planted `.claude/settings.json`** — `claudeSettingsStub`
   (`go-providers@v0.18.0/provider/bootdir_claude.go:174-184`). It emits
   `permissions.defaultMode: "bypassPermissions"` **only when
   `bypassPermissions` (= `a.SkipPermissions`) is true** (lines 179-181).
   When `SkipPermissions` is false the stub is `{}` (or `{apiKeyHelper:...}`)
   — **no `permissions` block at all**.

Net effect: a **non-dev** torque agent profile (the normal, recommended,
production case) produces:
- argv with **no** `--dangerously-skip-permissions`, and
- a planted `.claude/settings.json` with **no `permissions.defaultMode`**.

So the spawned `claude` falls back to its built-in `default` permission mode.
In `default` mode, any tool that needs approval triggers an interactive
"Allow this?" prompt. The torque-spawned `claude` runs as a streaming-stdio
subprocess with **no human at a TTY** to answer — the prompt is unanswerable,
and the run hangs or fails. This is exactly the reported symptom.

There is **no middle ground**. The only two reachable states are:
- profile has `--dangerously-skip-permissions` → fully bypassed (unsafe for
  arbitrary tasks), or
- profile does not → `default` mode → unanswerable prompts.

There is no path today to plant `permissions.defaultMode: "acceptEdits"` (the
sane middle: auto-approve file edits, still gate destructive shell). The
go-providers `claudeSettingsStub` is hard-wired to a boolean and its godoc
explicitly says "Apps that need a richer permissions policy ... can
post-process the planted file before spawn." **Torque does not do that
post-processing today.** Note also `internal/permission/**` (the `Engine`,
`rules.go`, `ModeAcceptEdits`, etc.) is torque's permission engine for tools
routed through torque's *own* `toolrouter`/`toolbroker`
(`internal/toolrouter/router.go:70`, `internal/toolbroker/toolbroker.go:134`)
— it governs MCP tools torque hosts, **not** the spawned `claude`
subprocess's own permission mode. The two systems are entirely disjoint;
`internal/permission` cannot fix this on its own.

### Proposed fix

Give torque first-class control of the spawned agent's permission mode,
independent of the all-or-nothing dev flag:

1. **Add a `PermissionMode` field to the torque agent profile** (`config.AgentProfile`).
   Accepts `default` / `acceptEdits` / `plan` / `bypassPermissions` (the
   Claude Code settings-schema vocabulary; mirrors `internal/permission.Mode`
   constants so the two systems use one vocabulary). Default value:
   `acceptEdits` — the safe, non-interactive middle ground for orchestrated
   runs (auto-approves edits, still gates genuinely destructive operations
   per Claude Code's own rules). Operators who want full bypass set
   `bypassPermissions`; the legacy `--dangerously-skip-permissions` arg
   continues to work and maps to `bypassPermissions`.

2. **Post-process the planted `.claude/settings.json` in torque's boot path.**
   `internal/runtime/agent/boot.go` already owns the planted boot dir
   (`prepared.PlantContext`, the `.claude/settings.json` location is
   `<bootDir>/.claude/settings.json`). After `providerplant.Plant` renders
   the stub, torque should merge in `permissions.defaultMode` from the
   profile's `PermissionMode` when the adapter did not already set it. This
   is exactly the "apps post-process the planted file" hook the go-providers
   godoc points at. This guarantees a deterministic, non-`default` permission
   mode for every torque-spawned `claude`, dev or not.

3. **(Optional, cleaner long-term)** Propose upstream to `go-providers`:
   change `ClaudeAdapter` to carry a `PermissionMode string` field and have
   `claudeSettingsStub` emit `permissions.defaultMode` from it (superseding
   the boolean `bypassPermissions`). This removes the post-processing hack.
   This is an **external** change but torque can drive it; until it lands,
   step 2 is the in-torque fix.

4. **Thread permission mode for non-claude providers** where applicable
   (codex / opencode have their own approval models). Out of scope for the
   immediate fix but note it in the doc so the gap is tracked.

### Affected files + effort

- `internal/config/profiles.go` / `config.go` — add `PermissionMode` to
  `AgentProfile`, parse from `profiles.yaml`, default `acceptEdits` (~20 LOC)
- `internal/runtime/agent/factory.go` — keep dev-flag → `bypassPermissions`
  mapping; surface `PermissionMode` to the boot path (~15 LOC)
- `internal/runtime/agent/boot.go` — post-process planted
  `.claude/settings.json` to inject `permissions.defaultMode` (~40-60 LOC + tests)
- `internal/runtime/agent/boot_test.go` / `factory_test.go` — coverage
- `docs/agent-execution-environment.md` — document the permission contract
- (Optional upstream) `go-providers` `ClaudeAdapter` + `claudeSettingsStub`
- **Effort: M** (in-torque). The upstream change is **S** but on another repo.

### Classification

**Torque-fixable.** Torque owns the planted boot dir and the agent profile;
it can deterministically set the spawned agent's permission mode without any
external change (step 2). The cleanest form (step 3) needs a go-providers
change, but that is a *nice-to-have*, not a blocker — torque can ship the fix
unilaterally.

---

## Recommended sequencing & priority

Ordered by value-to-effort and by unblocking the "predictable environment"
goal fastest:

### P0 — Variation 3 (permission mode). Effort: M. Torque-fixable, unilateral.
This is the most frequent hard-stop ("unanswerable prompts" hangs every
non-dev run) and the cleanest fix entirely inside torque. Ship the
`PermissionMode` profile field + planted-`settings.json` post-processing with
a default of `acceptEdits`. Single biggest stability win. Do this first.

### P1 — Variation 1 cleanup (delete dead `manager.go` / `hooks.go`). Effort: S.
Deleting the unwired `Manager`/`ConcurrencyHooks` subsystem removes the
parallel, nested-depth, never-integration-tested worktree path that is a
standing trap. Low risk, removes a whole class of "reached for the wrong
path" failures. Do this alongside or right after P0.

### P2 — Variation 2 (per-run worktree depth + git precheck). Effort: M.
Fix `SetupPerRun` placement to true sibling depth, add the relative-`replace`
detection guard, and add the `Worktree` precheck gate so a non-git working
dir blocks cleanly pre-dispatch. This makes per-run worktrees safe to turn on
for repos that use relative `go.mod replace` directives.

### P3 — Documentation: `docs/agent-execution-environment.md`. Effort: S.
A single authoritative doc covering: torque's four-root model (repo / work /
workspace / build), the per-run worktree contract and its sibling-depth rule,
the permission-mode contract, and the **explicit external-constraint
fences** — (a) torque does not spawn mux subagents; agents must not call
`subagent_spawn` inside a torque run; (b) Claude Code's Agent-tool
`.claude/worktrees/` placement is outside torque's control and should not be
relied on for torque runs. Write this last so it reflects the shipped P0-P2
behavior, but treat it as part of the deliverable — "documented environment"
is half the task's explicit ask.

### Out of scope / external constraints (tracked, not fixed here)
- Claude Code Agent-tool worktree creation behavior — external; mitigated by
  documentation + steering agents to torque's per-run worktree.
- mux `subagent_spawn` parent-ID requirement — external; does not apply to
  torque runs; documented as a non-goal.
- Non-claude provider permission modes (codex/opencode) — deferred follow-up.
- The upstream `go-providers` `ClaudeAdapter.PermissionMode` field — a
  nice-to-have that would let P0 drop its post-processing step; track as a
  separate cross-repo task.
