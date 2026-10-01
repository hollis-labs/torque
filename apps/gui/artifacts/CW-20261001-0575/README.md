# Dashboard aggregate verification

Screenshots show the real DashboardPage in a temporary browser harness. Network evidence was captured against Torque's HTTP handlers and an isolated in-memory SQLite database; final screenshots replay those recorded responses with the browser clock fixed to the captured window. No operator database, model CLI, scheduler, or production service was used.

Fixture: 4,330 visible tasks plus 50 internal tasks, 43 projects and 4,330 runs spread across 112 days. Run cost deliberately differs from ledger cost; ledger record dates fall outside the run window.

The Activity, Mission Control and Usage screenshots correspond to `network.json`. Mount issued six aggregate requests, one `/runs?limit=12` page and one SSE subscription. No task rows, automatic page exhaustion, or request limit above 50. Clicking Older runs issued exactly one additional cursor page.

The visible task cards equal `/tasks/facets`: total 4,330, active 3,248, done 1,082. The 14-day series has 546 runs, 834,300 prompt tokens, 87,060 completion tokens and $30.03 ledger cost, matching `/runs/facets` for exactly the same since/until (floating point cost compared with tolerance). The activity window and rolling pulse have their own explicitly labelled windows.

Browser timings are recorded in `network.json`; these are local synthetic-fixture observations, not a production latency claim. The harness omits the application shell to focus on this page. The live-handler check held SSE open without generated events; the screenshot replay ends its finite SSE response and therefore shows the connection-error indicator. Burst behavior is covered by the GUI tests.

The heatmap now labels authoritative **Run activity**. Its former task-update contribution came from a sampled task list and is intentionally removed per the lead's decision; an exact task-date aggregate is a separate follow-up.
