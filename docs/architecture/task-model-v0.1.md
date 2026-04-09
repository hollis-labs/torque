---
intent: system_doc
audience: humans
---

# Task model — v0.1

## Minimal fields
- id
- title
- status (todo|doing|paused|blocked|done)
- project
- priority (A|B|C)
- tags
- context
- description/body
- acceptance
- verification
- paths
- created_at / updated_at

## Artifact intent (v0.4)
Workflow artifacts (not tasks) carry an `intent` field declaring their class.
See `docs/07_artifact-classes.md` for the 5 classes and `intent` values.
Tasks themselves are internal execution artifacts (not user-facing docs).

## Sprint & iteration fields (v0.4, optional)
- `sprint_id`: string — sprint this task belongs to (e.g. `"sprint-2026-01"`)
- `iteration_id`: integer — volon iteration that created this task (e.g. `24`)

See `docs/09_backlog-model.md` for sprint rules and promotion lifecycle.

## Nanite mapping (initial)
Nanite supports:
- title
- body/description
- tags
- project
- context
- priority (A/B/C todo.txt style)

## Actions
- create_task
- update_task
- list_tasks (filters)
- select_next_tasks (planning heuristic)
