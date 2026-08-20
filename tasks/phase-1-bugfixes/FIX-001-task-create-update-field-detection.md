# FIX-001 — Task create/update field-detection + required fixes

**Phase:** 1 — Locked bug fixes
**Status:** todo
**Depends on:** none
**Blocks:** ENT-TASK
**Source:** ADR-0004 §1 items 1–2 (locked)

## Summary

Two concrete, audited deltas on `torque_task_create`/`torque_task_update`,
locked in ADR-0004 §1 as small and independent of the larger surface work.

## Scope

1. **Drop `mcp.Required()` from `description` on `torque_task_create`.**
   Neither the DB (`description TEXT NOT NULL DEFAULT ''`) nor
   `TaskService.Create` (only checks `Title`) requires it — the MCP schema
   is the only layer over-requiring. File:
   `internal/mcpadapter/task_tools.go`.
2. **Fix `handleTaskUpdate`'s field-detection bug for `title`/`description`/
   `priority`.** These three currently use value-based detection
   (`if v := reqStr(req, "title"); v != ""`), unlike every other field on
   the same tool, which correctly uses presence-based detection
   (`if _, ok := args["executor"]; ok`). Effect: `description` (and
   `title`) can never be cleared via update — `"description": ""` silently
   no-ops, contradicting the tool's own docstring ("empty string clears
   most nullable scalars"). Fix: switch all three to presence-based
   detection, matching the rest of the tool.
3. **Implementation note for the presence-based `title` fix**: once `title`
   is presence-based, an explicit `"title": ""` will reach
   `TaskService.Update`. Add an explicit non-empty check there (a clean
   `ValidationError`, not a raw DB `NOT NULL` failure) — title is the one
   truly-required field and must not be clearable to empty.

## Acceptance criteria

- [ ] `torque_task_create` accepts a create call with `description` omitted
      entirely (not `""`) without a required-field error.
- [ ] `torque_task_update` with `"description": ""` actually clears the
      description (verified against the DB row, not just a 200 response).
- [ ] `torque_task_update` with `"title": ""` returns a clean
      `arg_invalid`/validation error, not a raw SQLite `NOT NULL` failure.
- [ ] `torque_task_update` with `"priority": 0` (or whatever zero-value
      makes sense) is correctly detected as "field present" and applied,
      not silently ignored because the old code treated the zero value as
      "not set."
- [ ] Existing task update tests still pass; add coverage for the three
      presence-based fields if not already covered.

## Out of scope

- The unenforced `priority` range (1–5, documented but not validated
  anywhere) — ADR-0004 explicitly flags this as "not actioned this round,"
  pick up only if it blocks something concrete.
- Any of the create-field expansion in ENT-TASK (this task is the two
  narrow §1 bug fixes only).
