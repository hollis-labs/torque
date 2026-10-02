# RunsPage verification

Screenshots and [`network.json`](https://github.com/hollis-labs/torque/blob/bced88a6cb5071af64af6788cc087ec6cbc3bf85/apps/gui/artifacts/CW-20261001-0576/network.json) come from the real RunsPage rendered in a temporary browser harness against Torque's real HTTP handlers and an isolated in-memory SQLite database. No production database, scheduler, model CLI or operator service/config was used.

Fixture: 12,007 runs across 200 tasks, two executors and current task launch/legacy profiles; starts spaced five minutes apart. Run-row cost deliberately differs from canonical ledger cost. SSE was held open without generated events; GUI tests cover burst handling.

Mount issued exactly three requests: `/runs/facets` with no filters, `/runs?limit=50&sort_by=started_at&sort_dir=desc`, and `/events`. No fetch-all or list limit above 50. One user scroll loaded the next 50 rows by server cursor.

Applying Completed + executor `cli` + current profile `codex` + Last 7 days sent identical status/executor/profile/since/until filters to `/runs` and `/runs/facets`. Matching header count and opt-in page total both equal 136. Duration sort is a server parameter. `include_total` was absent until Show total progress was checked. The fixture header initially reports all 12,007 matching runs, rather than the first 50 displayed records.

The browser harness omits the app shell to focus on this conversion. Timing in `network.json` includes development-module loading on a busy shared host and is not a production performance claim. Screenshots show actual handler responses, without replay or mocked data. The fixture and harness were removed after capture.

Raw network capture is archived at the commit linked above; the summary, screenshots and any capture source remain here under the [evidence retention convention](../../../../docs/evidence/README.md).
