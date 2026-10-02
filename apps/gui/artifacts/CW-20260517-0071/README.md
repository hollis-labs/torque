# CW-20260517-0071 browser evidence

These screenshots use a loopback development GUI at 1440 × 1000 with every API request intercepted. Fixtures contain 75 collections, 301 tasks in the large collection and eight in the clear-action collection. They do not show live data.

`capture.mjs` verifies that status/search are sent to the server and find task 300 beyond page one, cursor continuation does not happen automatically, a status action mutates only the two checked IDs, and clearing confirms the exact eight-task count/name. Its simulated membership conflict demonstrates three removed, one failed, four not attempted, with only four DELETE requests. The page then reloads server truth. Screenshot 06 finds a collection beyond the initial collection page through server search.

Run with an existing Playwright installation (`PLAYWRIGHT_MODULE` may point to it) and a local Vite server. `GUI_BASE_URL` defaults to `http://127.0.0.1:5218`. No dependency install, model CLI, deployment or live database is part of the capture. `network.json` records requests and browser errors. Initial StrictMode cancellation/retry may duplicate page-one requests; continuation remains explicit.
