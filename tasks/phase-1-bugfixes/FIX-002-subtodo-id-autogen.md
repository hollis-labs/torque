# FIX-002 — Auto-generate Subtodo IDs

**Phase:** 1 — Locked bug fixes
**Status:** todo
**Depends on:** none
**Blocks:** ENT-SUBTODO, ENT-TASK
**Source:** ADR-0004 §1 item 3 (locked)

## Summary

`torque_task_subtodo_add`'s `id` is currently required and caller-invented,
enforced in `TaskService.AddSubtodo` (`internal/service/task.go:637-639`) —
the only entity in the system without a server-generated ID.

## Scope

- Make `id` optional on `torque_task_subtodo_add`.
- When omitted, the server generates a unique id. Scheme is not decided by
  the ADR ("short random slug or sequential, doesn't need deciding here") —
  pick one consistent with how other entity IDs are generated elsewhere in
  the codebase (check existing ID-generation helpers before inventing a new
  one).
- When the caller *does* supply an id, existing uniqueness validation still
  applies — meaningful slugs (e.g. `"check-auth-flow"`) stay possible.

## Acceptance criteria

- [ ] `torque_task_subtodo_add` with `id` omitted succeeds and returns a
      generated id.
- [ ] `torque_task_subtodo_add` with an explicit `id` still works and still
      rejects duplicates within the same task.
- [ ] `TaskService.AddSubtodo` signature/behavior updated accordingly;
      existing callers passing an explicit id are unaffected.
- [ ] Generated IDs don't collide with caller-supplied slug-style ids in
      practice (namespace or format them distinctly if that's the simplest
      way to guarantee this, e.g. a generated-id prefix).

## Out of scope

- Cross-task subtodo query surface (ADR explicitly keeps Subtodo
  task-scoped only, §1 item 4 — not revisited).
- `bulk_add` (that's ENT-SUBTODO, which depends on this task).
