# Task tagging conventions

Established 2026-08-19 during a full tag/status audit of Nanite's Torque tasks.
Portfolio-wide convention, not Nanite-specific — apply it to any project's
tasks. Goal: make `torque_task_list`/`torque_task_search`'s `tags` filter (AND
match) useful for slicing a large backlog by what kind of work it is, what
subsystem it touches, and whether it's actually ready to dispatch.

## Four dimensions

Apply as many as are true. A task should usually carry one **Kind**, zero or
more **Subsystem** tags, a **Layer** tag if it's not full-stack, and a
**Decision-state** tag only if one applies (most tasks carry none — that's
the normal, dispatch-ready case).

### 1. Kind — what type of work this is

| Tag | Meaning |
|---|---|
| `bug` | Something is broken or behaves wrong |
| `feature` | New capability |
| `chore` | Routine, non-urgent maintenance |
| `cleanup` | Dead code / tech-debt removal |
| `audit` | Investigation or research deliverable, not implementation |
| `docs` | Documentation-only work |

`tech-debt` stays as a separate, broader marker ("this is debt," not itself
an action) — a task can be both `cleanup` (the action) and `tech-debt` (the
category), same way `bug` + `tech-debt` can co-occur on a bug that's really a
symptom of accumulated debt.

### 2. Decision-state — is this actually ready to work

| Tag | Meaning |
|---|---|
| `discuss-first` | Genuinely undecided — needs a real decision before it can be scoped. (Pre-existing tag, 15+ uses as of 2026-08-19 — this *is* the "open question" concept; don't introduce a synonym.) |
| `needs-review` | Needs someone to verify it's still valid/current against present-day code or architecture before acting — not a decision question, a freshness question. New. |
| `details-needed` | The spec itself is underspecified — not a decision or a freshness problem, just not enough written down yet to start. New. |

### 3. Subsystem — which part of the system this touches

Reuse these existing, already-established tags as-is:
`reflexes`, `skills`, `messaging`, `settings`, `slots`, `compaction`, `mcp`,
`migrations`.

Add these — currently nothing distinguishes them from the catch-all `chat`
tag, but they map directly to real architecture-doc sections
(`docs/engineering/architecture/0{1-4}-*.md` in the Nanite repo, and the
equivalent split wherever else this applies):

| Tag | Covers |
|---|---|
| `agent-construction` | Roles/agents/consumers composition, agent_tools, agent_skills |
| `agent-launching` | Boot, runtime_kind, context resolvers, CLI vs API dispatch |
| `steering` | Reflex dispatch, promptrouter, broker → reflex migration |
| `harness` | Turn loop, chat engine mechanics, compaction pipeline itself (distinct from `compaction`, which is the feature area; `harness` is the loop that drives it) |

Going forward, standardize on the plural/canonical form where a real project
already has drift:
- `plugins` (not `plugin`, not per-plugin compounds like `plugin-agent-mux` —
  add a second, specific tag for the individual plugin if that level of
  granularity is genuinely needed, don't let it replace the subsystem tag)
- `cards` (not `envelope`/`envelopes` — matches current terminology: Envelope
  is now scoped to the wire/transport format only, Cards is the rendered UI
  system built on top of it)

This is a naming standard for *new* tagging, not a retroactive rename —
existing `plugin`/`envelope`/`envelopes` tags on old tasks are left alone
unless a task is being touched for another reason anyway.

### 4. Layer — already well-established, no changes

`backend` (32 uses on Nanite alone as of 2026-08-19), `frontend` (30 uses).
Keep using as-is. Omit for genuinely full-stack tasks rather than tagging
both.

## Grouping (release, feature initiative, etc.) — use Epics/Sprints, not tags

Torque already has first-class `Epic` and `Sprint` entities for this — many
tasks already carry real `sprint_id`/`epic_id` values. A `release`-style tag
duplicates what those structures already do (and `release` already has 22
uses on Nanite as a broad release-train marker — don't fragment it further
with a narrower synonym like `release-readiness`). If a task needs
release-blocking urgency called out, that's a **priority** question
(`priority` field, 1-5), not a tag question.

## Known existing inconsistencies (informational, not urgent to fix)

- `plugin` (singular) vs `plugin-agent-mux`/`plugin-sdk`/`plugin-host`
  (compounds) — see Subsystem section above for the going-forward standard.
- `envelope` vs `envelopes` — literal duplicate tags for the same concept,
  now doubly stale since the terminology itself moved to "Cards."
- `cleanup-backend-tech-debt` — a compound tag that's really `cleanup` +
  `tech-debt` + `backend` stapled together. New tagging should use the three
  separate tags instead of inventing another compound.

## Querying reliably

As of 2026-08-19, `torque_task_list`'s `status` filter was found to be
unreliable at scale — it silently returned 19 rows for Nanite's `todo` status
when the real count was 174 (confirmed via direct SQLite query against
`~/.local/share/torque/workspaces/default/main.db`). The `tags` filter
wasn't independently stress-tested at the same scale during that audit — if
a `tags`-filtered query looks suspiciously small for what you'd expect, cross
-check with a direct query before trusting it:

```sql
SELECT t.id, t.title FROM tasks t
JOIN task_tags tt ON tt.task_id = t.id
WHERE tt.tag_slug = '<slug>' AND t.project_id = '<PRJ-...>'
  AND t.status IN ('todo','backlog');
```
