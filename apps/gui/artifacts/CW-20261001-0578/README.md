# CW-20261001-0578 browser evidence

Screenshots use a local development GUI at 1440 × 1000 with mocked HTTP responses; they do not show the live database. Fixtures contain 75 collections, 301 tasks, 210 models, 120 templates, 120 runs/messages, 130 comments and 110 artifacts. Every list starts with one bounded page. React StrictMode may cancel and retry the initial request; the assertions distinguish those retries from cursor continuation.

`capture.mjs` intercepts every API request. It checks that collection tasks continue only after an explicit Load more, inbox receiving happens only after an operator click, thread history continues with a cursor, and all activity sources expose their own paging control. [`network.json`](https://github.com/hollis-labs/torque/blob/82a352760898e3db21c5bbc39a4ce87ca7a8fa88/apps/gui/artifacts/CW-20261001-0578/network.json) records requests and browser errors. No model CLI runs.

With the GUI dev server running on loopback, run `node apps/gui/artifacts/CW-20261001-0578/capture.mjs`. Playwright must already be available; `PLAYWRIGHT_MODULE` can point to an installed module and `GUI_BASE_URL` can override the loopback URL.

Screenshots 01–02 show collection task continuation; 03–04 show server numeric sort and search; 05–06 show templates/plans; 07–08 show explicit inbox receive and thread continuation; 09 shows loaded-page widget scope; 10 shows the three independently paged activity sources. These fixture screenshots complement the real API adapter and component tests, rather than proving production database performance.

Raw network capture is archived at the commit linked above; the summary, screenshots and any capture source remain here under the [evidence retention convention](../../../../docs/evidence/README.md).
