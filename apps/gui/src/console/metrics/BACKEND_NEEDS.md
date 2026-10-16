# Metrics kit: backend needs

The kit (`ProgressBar`, `StackedBar`, `Burndown`/burnup, `Sparkline`, `StatTile`) is pure
presentational and takes typed props. Data enters through `MetricsSource` (`types.ts`);
`FixtureMetricsSource` is the only implementation today. P2 supplies a Torque-backed one.

## What a real source needs

1. Status counts for a scope (epic, sprint, project, collection): `{status, count}[]`.
   Torque has task facets; confirm one call can return counts grouped by status for an
   epic/sprint id (otherwise the GUI would page all tasks).
2. Burn history: per scope, the time each task entered `done` (completion events) plus
   scope changes (task added to / removed from the scope, with timestamps). Today a task
   only carries current status and `updated_at`; completion time and membership history
   are not reliably recorded. Needed: either a status-transition log queryable by scope or
   a daily snapshot series `{t, total, done}`.
3. Sprint/epic start and end dates (`startTs`, `endTs`) for the ideal line. Epics may have
   no end date; the chart then needs a caller-chosen window.
4. Period-over-period values for tiles (value and previous period) per metric, or the raw
   series so the GUI derives both. Metrics wanted first: tasks done, active agents, failed
   runs, tokens/min, queue depth.
5. Bus feed: `metric.tick` events (`payload {metric, value, unit?}`) already fold into a
   `MetricSeries` via `seriesFromTicks`. Need a stable metric-name vocabulary and a
   retention window so a fresh client can backfill (snapshot), not only receive new ticks.
6. ETA: computed client-side from throughput (`estimateEta`). A server estimate that
   accounts for blocked work and agent capacity would be better; optional.

## Open questions

- `MetricSeries` lives in `console/metrics/types.ts`, not `events/types.ts`, to keep P1
  inside its own directory. Move it into the F1 contract if the data agent wants it
  there; it is structurally independent of the event envelope.
- Time zones: charts label dates in UTC (`MM-DD`). Decide whether to follow the user locale.

## P2: what the Torque-backed views degrade on (exact needs)

`TorqueMetricsSource` (scopes `epic:|sprint:|project:|tag:`) uses only existing read endpoints:
`GET /tasks/rollup`, `GET /tasks?fields=summary` (cursor-paged, 200 x 5 pages max), `GET /sprints/{id}`, `GET /epics/{id}`.

Derived or missing today, each flagged to the user in the panel notes:

1. **Task completion time.** Burn uses `updated_at` of `done` tasks. Any later edit (comment, tag) moves a task's apparent
   completion. Need `completed_at` (or a `status_changed_at` + transition log) on the task summary.
2. **Scope-change history.** Added work is inferred from `created_at > startTs`; tasks moved into or out of a sprint/epic, and
   removals, are invisible. Need a membership log `{task_id, scope_id, action, ts}` or a daily `{t, total, done}` snapshot per scope.
3. **Tag scope rollup.** `/tasks/rollup` groups only by project/epic/sprint, so `tag:` scopes page the task list. Need
   `group_by=tag` (or a tags facet with status counts).
4. **Workstream scope.** No workstream id on tasks; `tag:ws-...` is the stand-in. Plans (`kind=plan` children via `parent_id`) are
   not wired as a scope yet.
5. **Truncation.** Scopes over 1000 tasks show a "first N tasks" note. A server-side burn series removes the cap (and the paging).
6. **Tiles.** "done 7d" and its previous period come from the same `updated_at` proxy; in-progress/review/blocked are snapshots.
   Failed-run, tokens and cost tiles for a scope need run rows joined by scope (`/runs` has tokens and cost, no scope filter).
7. **Fleet metrics** (working/idle/waiting over time, tokens, cost) fold `agent.state` and `metric.tick` events from the console
   store. The window only reaches as far back as the snapshot/retention the bus gives a fresh client. Needed from the stream:
   - `agent.state` carrying a stable agent id on `subjects[type=agent]` for every transition (including to `unknown`/gone), and an
     agent departure event so the fleet count does not keep dead agents;
   - `metric.tick` with metrics `tokens_per_min` and `cost` (unit `usd`), emitted per fleet or per agent with a `scope` dimension;
   - snapshot backfill of at least 1h of state events (or a `fleet.counts` series) so charts are not empty on first paint.
8. **Metric cards.** `metric.progress`, `metric.burndown`, `metric.fleet` event kinds (payload `{scope, title?}`) are placeholders
   (`METRIC_CARD_RULES`); the data team should either adopt them or supply the real kind to remap in the rule data.
