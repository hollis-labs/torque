# Board pagination review

Captured with headless Chromium against this branch's loopback Vite server. Every API request was intercepted with isolated fixtures; no live Torque data, database, model CLI or service was used.

The fixture contains 4,001 task summaries across todo (1,001), doing (1,000), review (1,000), and blocked (1,000). Screenshot counts come from the full filtered fixture cohort; the row list initially returns 50 summaries. [`network.json`](https://github.com/hollis-labs/torque/blob/073b36974c2837963556fb41af195323e220dda0/apps/gui/artifacts/CW-20261001-0574/network.json) records query parameters and response metadata only, including cursor continuation after scrolling. No list request asks for totals or exceeds 50 rows.

- `board-default.png`: full cohort counts, compact scope selectors, Manual and Auto both enabled.
- `board-manual.png`: Auto disabled, manual-only query and facet counts.
- `board-eligible.png`: static eligibility query, other controls dimmed, runtime exclusion help text.
- `board-saved-view.png`: explicitly saved browser-local view, Apply / Reset / Save as / Save over controls.

Status, priority and date headers sort through the server. Title is not in the server sort allowlist, so its header does not sort. Search is debounced for 300 ms and is passed to both the list and facets. Saved snapshots change only through Save as or Save over; Clear changes the working filters.

Loaded-row SSE updates patch or refresh those rows through `usePagedList`. Under search/eligibility filters, an event touching a loaded row shows the inline “Updated — refresh” action because those cohort predicates must be reconciled by the server. Unknown row IDs invalidate facets only. Parent selector search upgrades and the bounded tags envelope are separate tasks (0577/0677).

Raw network capture is archived at the commit linked above; the summary, screenshots and any capture source remain here under the [evidence retention convention](../../../../docs/evidence/README.md).
