CW-20261001-0577: real HTTP-handler browser evidence

The fixture used an isolated in-memory SQLite store with real migrations, services and HTTP handlers: 360 projects, 435 epics, 435 sprints and 2,160 tasks. Archive/status variations and an internal-only parent were included. No operator database, real model CLI or deployed service was used. The preview mounted the authored pages with their normal API and ActiveRuns providers. Chromium used temporary downloaded libraries; no system packages changed.

The default visible cohorts are 338 projects and 402 epics/sprints. Exact Scoped Tasks is 2,022; projects show 402 child sprints and epics across the entire cohort. Project P-001 has 65 visible child sprints/epics. These are the recorded facet results, not page-length counts. Task-progress bars keep their prior grouping (excluding archived/abandoned/cancelled statuses), so their denominators can differ from the all-non-internal Scoped Tasks tile.

The [archived `network.json`](https://github.com/hollis-labs/torque/blob/141994289b333e8287ca187add4103a3f7cb7872/apps/gui/artifacts/CW-20261001-0577/network.json) includes requests and real response bodies. Epics mount makes one 50-row page request, two bounded facet requests (totals immediately, then exactly the displayed IDs), two SSE subscriptions from the preview's normal providers, and direct name lookups for only the distinct project IDs on screen. No parent list is fetched for those labels. Observed interactive time was about 2.9 seconds on the shared development host during GUI gates; this is local evidence, not a production benchmark.

Next fetches one cursor page and requests exactly its displayed rollup IDs; Previous uses already loaded rows. Search/status/archive/sort reset the cursor and use the same cohort in facets. The screenshots show an off-page `Project 299` found and selected through server search, the equivalent Operations epic search, and Scope Manager server search. Project P-001's child list requests 50 rows, then 15 on explicit continuation, with an exact total of 65. Every recorded list request uses limit <=50; no option collector or fetch-all traversal runs.

Screenshots 01–03 show epic browsing/filtering; 04–05 show a modal picker search; 06–07 show project/sprint cohort cards; 08 shows child paging; 09 shows Operations; 10 shows Scope Manager. All screenshots are actual browser renders of real responses. The fixture intentionally has no scheduler runtime, so Operations' read-only `/scheduler/status` returns 503; this is unrelated to the parent conversion. There were no browser page errors. The SSE fixture held a live empty stream; automated tests cover burst invalidation without row-list refetches.

Temporary fixture/harness code and processes were removed after capture. GUI typecheck/build and 255 tests pass. Lint: current main 41 errors/0 warnings; branch 37/0, no new diagnostics (comparison by file/rule/message). No lint suppressions added.

The raw snapshot is retained at PR #219's merge commit and listed in the
[evidence archive table](../../../../docs/evidence/README.md#archived-snapshots).
The summary and referenced screenshots remain in the tree.
