# Plan-execute end-to-end smoke (S2 exit gate)

**Ticket:** [CW-20260503-0021](mux://torque/CW-20260503-0021) (S2.5)
**Epic:** EP-20260503-0002 — Agentic Execution Flow

This runbook validates the full agentic execution loop against a real
LLM provider. Substrate-only coverage lives in
`internal/e2e/sessionmgr_broker/e2e_test.go` (S1.6) and the
trigger-composition test at `internal/e2e/plan_execute/e2e_test.go`
(this ticket); both run unattended in CI. The smoke below requires a
paid API key and is the user-driven gate before declaring the epic
shippable.

## Prerequisites

- `torque` daemon built from the current `feat/agentic-exec-s2-agents`
  tip (or main once merged). `go build ./...` first.
- `ANTHROPIC_API_KEY` (preferred) or `OPENAI_API_KEY` exported in the
  shell that boots `torque serve`.
- `profiles.yaml` includes a `torque-backend` profile (or your
  preferred dogfood profile) wired to `executor=cli` + `provider=opencode`
  for the test plan's children. `reviewer-end-agent`, `planner`, and
  `orchestrator` resolve via builtins (S2.3 / S2.4 / S2.2) — you can
  override them in `profiles.yaml` if you want different timeouts /
  models.
- A scratch worktree the orchestrator can run in (e.g.
  `~/Projects-apps/torque-smoke/`). Don't run against your
  primary repo — the agents will read/write files in the workdir.

## Test plan shape

A 2-phase × 2-task plan, ~5K tokens per child task. Filing template:

```bash
torque mcp torque_plan_create \
  title="S2.5 smoke: tiny Go module" \
  description="2-phase plan that writes hello.go + a test, then formats and lints." \
  phases='[
    {"name":"write","acceptance":"hello.go + hello_test.go committed; go test passes"},
    {"name":"polish","acceptance":"go fmt + go vet clean"}
  ]'
```

Note the returned `plan_id` (call it `<PLAN>`).

Add 4 child tasks (2 per phase). Use small, focused descriptions so
each child stays under ~5K tokens:

```bash
# Phase 1 / write
torque mcp torque_task_create \
  title="hello.go: write a Hello() function" \
  parent_id="<PLAN>" \
  metadata='{"phase_id":"ph-1"}' \
  agent_profile="torque-backend" \
  manual=true

torque mcp torque_task_create \
  title="hello_test.go: cover Hello()" \
  parent_id="<PLAN>" \
  metadata='{"phase_id":"ph-1"}' \
  agent_profile="torque-backend" \
  manual=true

# Phase 2 / polish
torque mcp torque_task_create \
  title="go fmt the package" \
  parent_id="<PLAN>" \
  metadata='{"phase_id":"ph-2"}' \
  agent_profile="torque-backend" \
  manual=true

torque mcp torque_task_create \
  title="go vet the package" \
  parent_id="<PLAN>" \
  metadata='{"phase_id":"ph-2"}' \
  agent_profile="torque-backend" \
  manual=true
```

All 4 children start `manual=true`. The orchestrator promotes each in
order to `manual=false` (orchestrator-as-reviewer per D4) when it's
ready to dispatch. The user does NOT pre-flip them.

## Trigger

```bash
torque mcp torque_plan_start plan_id="<PLAN>" workdir=~/Projects-apps/torque-smoke
```

Or via the GUI:

1. Navigate to `/plans/<PLAN>`.
2. Click **Execute Plan**. Toast shows the orchestrator's session id.

## Observable signals (12-item exit gate)

Watch each of these end-to-end. All must clear before declaring the
epic shippable.

1. **Orchestrator session boots** — `torque_session_list state=running`
   shows a session with `agent_profile=orchestrator` and
   `meta.plan_id=<PLAN>`.
2. **Planner sub-agent invoked** — a `kind=internal` task with title
   `planner: <PLAN>` and `parent_id=<PLAN>` appears and reaches `done`
   within ~2 minutes. The plan task gets a `[system/planner]` comment
   with the V0 schema (per-task hints + plan-level observations).
3. **Plan metadata stamped** — `metadata.plan.planner_refinement` JSON
   block on `<PLAN>` populated; `metadata.plan.planner_refined_at` is
   an RFC3339 timestamp.
4. **Phase 1 transitions todo → doing** — `metadata.plan.phases[0].status`
   on `<PLAN>` flips. Visible in the Plan detail view's column header.
5. **Phase 1 children dispatch sequentially** — child #1 transitions
   `todo → doing → review`; reviewer end-agent (kind=internal,
   parent_id=child#1) appears, reaches `done`; child #1 transitions
   `review → done`. Then child #2 follows the same path. **Exactly one
   child active at a time** — this is V0's sequential contract.
6. **Reviewer V1 comments visible** — each child gets a
   `[system/end-agent]` summary comment naming what was verified /
   patched / needs human follow-up.
7. **Phase 1 transitions to done** — `metadata.plan.phases[0].status =
   "done"` on `<PLAN>`.
8. **Phase 2 begins** — same sequence as Phase 1; phases walk in
   author order.
9. **Plan transitions to review** — when both phases are done, `<PLAN>`
   moves to `review` (the `on_done=review` Plans v1 default). The
   orchestrator does NOT auto-close the plan.
10. **Orchestrator session terminates cleanly** — final
    `session.state_changed` event with state=`done`. `torque_session_list`
    no longer shows it as `running`.
11. **Cost ledger** — every child task has a `cost` row with
    `tokens_in > 0`, `tokens_out > 0`, `cost_source='models_dev'`.
    Inspect via `torque_run_list task_id=<child_id>` and walk the
    run record's cost columns. The orchestrator session itself
    accounts in the same way.
12. **Whole suite green** — back in the daemon's repo:

    ```bash
    go build ./... && go vet ./... && go test -race -count=1 ./...
    ```

    All packages pass.

## Failure modes to surface (for follow-up tickets)

These are NOT pass criteria; they're observations to capture if you
hit them. File new Torque tickets per the boot-prompt backlog
discipline.

- Reviewer crashes on a specific child → `[system/end-agent] failed`
  comment fires (substrate-side), child stays at `review`. Manual
  resolution.
- Orchestrator stops mid-phase → check
  `torque_session_get id=<session>` for `state=failed|crashed`. The
  orphan sweep (S1.4) marks crashed rows on next daemon restart.
- Child task dispatched without an agent_profile → picker rejects it
  with `SkipReasonEmptyProfile`. Plan blocked; surface to user.
- Cost ledger row with `tokens_in=0` or `cost_source=''` → backfill
  bug; file a follow-up against the cost-ledger track.

## Rerun

To rerun against the same plan:

1. If the plan is in `review` with all children `done`: re-trigger
   `torque_plan_start <PLAN>` — the trigger accepts `review` status
   and boots a fresh orchestrator (children that are already done
   stay done; the orchestrator walks them as no-ops).
2. If the plan is in `doing` with a live orchestrator session: the
   trigger returns `409 ErrAlreadyOrchestrating` with the existing
   session id. Either wait for it to finish or cancel via
   `torque_session_stop`.
3. To start fresh: re-open all children to `todo` (manual transition)
   and re-trigger.

## What "shippable" means after this passes

The 12-item gate above clears → the agentic execution flow epic
(EP-20260503-0002) ships. Open follow-ups (cancel UX, parallel
fan-out, on-demand architect, code-review-with-fix, multi-orchestrator
coordination, cost-budget enforcement) are tracked separately per the
boot prompt's "Out of scope" list.
