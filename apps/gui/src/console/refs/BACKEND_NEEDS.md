# Reference registry: backend needs (F2)

The detector is pure and needs no backend. Resolution (hover cards, live
titles, not-found states) goes through `KindResolver.resolveMany(canonicals)`
per kind (see `types.ts`). Until a resolver exists for a kind, the client
reports `unsupported` and the chip still links via `RefKind.open`.

Every resolver is batch-shaped: one call, many ids, a missing id means
not-found, a thrown error means retry later. Each must return a `RefEntity`
(title, optional subtitle/description/status/fields/updatedAt).

| Kind | Resolver status | Backend need |
|---|---|---|
| `torque.task` | Live: `createTorqueTaskResolver(api.taskTitles)` | None. Optional: return `description` and `epic_id` in `/tasks/titles` for richer cards. |
| `torque.epic`, `torque.project`, `torque.sprint`, `torque.plan` | Mock only | Batch brief endpoint like `/tasks/titles` for each (`ids` -> `{id: {title, status, updated_at} \| null}`), or one generic `/tasks/briefs?ids=` keyed by id prefix. |
| `tesseract.item` | Mock only | Batch lookup by ULID: `{item_id: {title/summary, domain, namespace, key, updated_at} \| null}`. Also a GUI-reachable read path (the GUI has no Tesseract client today). |
| `tesseract.key` | Mock only | Batch lookup by `[domain@]namespace#key` with the same shape. Domain defaults to the caller's default when omitted. |
| `workstream` | No Torque route | Portfolio workstreams now belong to Tachyon; no preview resolver is connected. |
| `agent` | Mock only | Batch lookup by agent id/name: `{name, provider, state, cwd, last_activity}`. The fleet snapshot (`/api/v1/activity/agents`) already has most of it; a lookup by name over the registry would cover agents not currently running. R1 confirmed the route is `/agents` (a path-based tab, not `?tab=`); it has no per-agent deep link yet. |
| `path` | No resolver (copy target) | Optional: existence/kind check (`exists`, `isDir`, `size`) for paths under a known root, and repo-relative resolution needs the owning repo/cwd of the surrounding context (not known to the detector). |
| `url` | No resolver | Optional: unfurl service (title, favicon). Must be server-side (CORS, SSRF controls). |
| `forge.*` (PR, commit, repo) | Added by G2 | G2 registers kinds via `registry.register(...)`; resolvers come from G0/G1 ForgeClient. |

## Open points

- Torque collection ids (`COL-...`) are not detected: the GUI has no per-collection route to open.
- Sprint and plan prefixes (`SP-`, `PL-`) are taken from the server's id generators; confirm they are final.
- Tesseract has no GUI-visible id format contract. Detection assumes ULIDs (26 chars Crockford base32) and the `namespace#key` convention with the roots `project|user|agent|shared|global|system`.
