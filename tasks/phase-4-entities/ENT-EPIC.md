# ENT-EPIC — Epic: merge list+search, settable priority, archive, bulk_update

**Phase:** 4 — Per-entity rollout
**Status:** todo
**Depends on:** FIX-003, FIX-004, PRIM-001, PRIM-002, PRIM-003, PRIM-004,
DEC-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Epic); audit "Per-Entity Findings → Epic"

## Summary

Epic target inventory: `create`, `get`, `list` (merged search, sort,
cursor), `update`, `delete`, `archive`/`unarchive`, `bulk_update`.
FIX-003 (status vocabulary bug) must land first since it touches the same
`update` path this task extends.

## Scope

- **`list`**: apply PRIM-001 (pagination — dead `Limit`/`Offset` in
  `EpicFilter` today) and PRIM-002 (sort). No epic search exists today (no
  analog to Task/Issue's `_search`) — confirm whether a merged
  list+search is actually needed for Epic or whether `list`'s filter/search
  param alone covers it; ADR §5 says "merged search, sort, cursor" so build
  free-text search into `list` directly rather than a separate tool.
- **`priority` settable**: exists on `EpicRecord`, `EpicCreateInput`, and
  `EpicUpdateInput`, even surfaced in `briefEpic` list responses — but
  neither `torque_epic_create` nor `torque_epic_update` expose a `priority`
  param today (`epic_tools.go:12-40`). Wire it on both.
- **Archive/unarchive**: apply PRIM-004's pattern.
- **`bulk_update`**: apply PRIM-003's pattern.
- **Registration**: apply DEC-002's decision.

## Acceptance criteria

- [ ] `torque_epic_list` supports free-text search, pagination (PRIM-001),
      and sort (PRIM-002) in one tool — no separate `_search` tool created.
- [ ] `torque_epic_create` and `torque_epic_update` both accept `priority`;
      round-trips correctly (set on create, changeable on update, visible
      in list/get responses — the last part already works).
- [ ] `archive`/`unarchive` tools exist per PRIM-004.
- [ ] `bulk_update` exists per PRIM-003.
- [ ] Tool registration matches DEC-002.
- [ ] FIX-003's status-vocabulary fix is confirmed still consistent after
      this task's `update` changes (don't reintroduce the mismatch).

## Out of scope

- None beyond what's listed — Epic's target inventory is fully covered by
  this task plus FIX-003/FIX-004.
