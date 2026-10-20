# Agent directory: backend needs

The agent directory (`/agents/directory`) is read-only and mock-backed behind
`AgentsClient` (`client.ts`). The mock lives in `mock.ts`; swap it by passing a
real client to `AgentsClientProvider` (or changing the default in `context.tsx`).
Shapes in `types.ts` follow the Tether catalog agent entry as understood today;
fields marked optional there are the ones we expect the API may not yet carry.

## Endpoints needed (names are suggestions)

1. `GET /api/v1/agents` -> `AgentRecord[]`
   - Backed by `tether_catalog_list_agents` / `tether_agent_list`, joined with
     launch/provider data. Server-side filter params: `search`, `provider`, `role`
     (the view currently filters client-side, so these can follow).
2. `GET /api/v1/agents/{id}` -> `AgentRecord` (404 when unknown). Backed by `tether_agent_show`.
3. Live join: `AgentRecord.live` (`state`, `sessions`, `lastActivity`) should be
   derived from the activity monitor feed (the Fleet Agents tab data) matched to
   a catalog agent. Open question: what key links a fleet process to a catalog
   agent (session id? launch id? cwd+provider heuristic?). Today Fleet rows only
   carry `id`, `provider`, `pid`, `sessionId`, `cwd`.

## Field-by-field gaps

| Field | Need |
|---|---|
| `identity.urn` | Messaging URN, present only after registration. |
| `identity.enabled` | Catalog enable flag. |
| `identity.summary`, `roles` | Free-text blurb and role list. |
| `runtime.model` | Not in the catalog today; confirm where the model is chosen (agent, launch or provider). |
| `runtime.project/worker/workspaceMode` | From the launch (agent x provider x project). |
| `policy.permissionMode` | Only `bypass` is confirmed live; `default` is a guess. |
| `policy.sandbox` | Field name unconfirmed. |
| `policy.network`, `scopes`, `rules` | Confirm names and whether rules are ids or inline text. |
| `tools` | Resolved from the project's `mcp.servers` (a non-empty list replaces the default of torque only). Server should return the resolved list so the GUI does not copy project config. |
| `skills` | Equipped skill ids. |
| `teams` | Team membership (id, name, role). No team API is known yet. |
| `source` | Definition file path, revision/sha and updated time. Needed for the "source definition" panel. |

## Out of scope here (later tasks)

- Create and delete agents (B3 lifecycle). Edit/plan/approve/apply is B2, below.
- Live push of directory changes; the directory currently loads once per mount.

## B2: edit, plan, approve, apply (exact needs)

The editor is mock-backed behind the write half of `AgentsClient`
(`planChange`, `getPlan`, `approvePlan`, `applyPlan`, `rollbackApply`,
`listAudit`). Types are in `plan.ts`. `plan.ts` also holds the reference diff,
risk and validation logic; the server must recompute all three authoritatively
(the GUI preview is advisory). Approve, apply and rollback are ActionBus actions
(`agent.plan.approve|apply|rollback`, see `actions.ts`) that carry a `confirm`
requirement, so IdentityConfirm runs first and the handler forwards the
`ConfirmResult` (including `token`) to the client.

### Endpoints (names are suggestions)

1. `POST /api/v1/agents/{id}/plans` body `PlanRequest {draft, baseRevision, note}` -> `AgentPlan`.
   Stores the plan (status `proposed` or `invalid`). Does not touch the definition.
   Returns `changes[]`, `risks[]`, `issues[]`, `risk`, `requiredScope`.
2. `GET /api/v1/agent-plans/{planId}` -> `AgentPlan` (404 when unknown).
3. `POST /api/v1/agent-plans/{planId}/approve` body `{confirmation}` -> `AgentPlan` (status `approved`, `approval {by, at, expiresAt, scope}`).
4. `POST /api/v1/agent-plans/{planId}/apply` body `{confirmation}` -> `ApplyOutcome`
   (`applied | failed | conflict | expired`, `agent`, `revisionBefore/After`, `auditId`).
5. `POST /api/v1/agent-plans/{planId}/rollback` body `{confirmation}` -> `ApplyOutcome` (`rolled_back | conflict`).
6. `GET /api/v1/agents/{id}/audit` -> `AuditEntry[]` newest first (cursor pagination later).

### Step-up verification (IdentityConfirm)

- Each of approve, apply and rollback needs its own fresh grant. The server
  verifies `confirmation.token` against the auth issuer (the Hollis auth / Tether
  issuer JWT work), not the client-reported `outcome`.
- The grant must bind: `who` (the authenticated subject), `what.action`
  (`agent.plan.approve|apply|rollback`), `what.subject` (agent id), the plan id,
  `scope` covering `plan.requiredScope`, and an unexpired `expiresAt`. A token
  minted for approve must not work for apply. Tokens are single-use.
- `requiredScope` rules the mock uses: always `agent.write` and `agent:<id>`;
  add `agent.write.privileged` when any risk is `high`; add `policy.grant` when a
  privileged scope is added. Scope names are placeholders to align with the
  Security/Auth project (PRJ-20260414-0001) and the policy-plane epic (EP-20261010-0002).
- Open: should approver differ from plan author for high risk (four eyes)?
  The GUI shows the approver but does not enforce it; the server should.

### Plan semantics and policy checks

- `baseRevision` pins the definition version. Apply must fail with `conflict`
  if the current revision differs; never merge silently.
- Approval is time-boxed (mock: 15 minutes). Apply after expiry returns `expired`.
- Apply must be atomic: write the catalog file/entry, validate it by loading,
  and report `failed` with the definition unchanged on any error.
- Server-side validation beyond the GUI's: provider and model exist, project
  exists, tool servers resolve, scope and rule ids exist in the policy
  vocabulary, guard rules cannot be removed without the matching privilege, and
  organisation policy can veto (return `issues[]` with `level: error`).
- Rollback restores the pre-apply snapshot as a new revision (history is never rewritten).
- Open: where `prompt`, `bootProfile`, `tools` allowlist and `model` live in the
  catalog. `AgentRuntime.bootProfile/prompt` were added to `types.ts` as optional
  placeholders. Tools are resolved from the project today, so a per-agent
  allowlist may need a new catalog field.
- Live effect: say whether a change needs a session restart (the GUI warns for
  provider/model changes on running agents; the server should return it per change).

### Audit trail

Append-only record per event: `plan`, `approve`, `apply`, `apply_failed`,
`rollback`. Fields: actor (authenticated subject, not client-supplied),
plan id, summary, risk, change count, revisions before/after, and the step-up
metadata (outcome, scope, issuedAt, expiresAt). Never persist the token. Should
also surface on the Console event feed so alerts can link to it.

## B3: lifecycle, teams, assignments

GUI side: `AgentsClient.planLifecycle` plus read methods `getRetireImpact`,
`listTemplates`, `listTeams`, `getTeam`, `listAssignments` (mock in `mock.ts`,
fixtures in `lifecycle-fixtures.ts`, reference planner in `lifecycle.ts`).
Every lifecycle change is an `AgentPlan` with a `lifecycle` block and reuses the
B2 routes for approve, apply, rollback and audit. Nothing writes without a plan.

### New routes

1. `POST /api/v1/agent-plans/lifecycle` body `LifecyclePlanRequest`
   (`{op, ...fields, note?, baseRevision?}`) -> `AgentPlan` with
   `lifecycle {request, summary, items[], impact?}`. Ops: `agent.create`
   (`agentId, name, draft, cloneOf?, templateId?`), `agent.retire`
   (`agentId, sessions: drain|stop`), `agent.restore`, `team.create|update`
   (`teamId, team: {name, summary, members[{agentId, role}], policy{permissionCeiling,
   scopesCeiling[], rules[], reportTo?}}`), `team.delete`, `assignment.update`
   (`add[{agentId, target{type: workstream|task, id, label?}, role?}], remove[assignmentId]`).
   Approve, apply, rollback and `GET /agent-plans/{id}` are the B2 routes; they must
   branch on `plan.lifecycle.request.op`. For team plans `plan.agentId` is the team id;
   for assignment plans it is the single agent touched or `"assignments"`.
2. `GET /api/v1/agents/{id}/retire-impact` -> `RetireImpact`
   `{sessions{count,state}, tasks[], workstreams[], teams[{teamId,teamName,role,soleCoordinator}]}`.
   Must come from live Fleet state and Torque assignments, not the catalog file.
3. `GET /api/v1/agent-templates` -> `AgentTemplate[]` `{id, name, summary, draft: Partial<AgentDraft>}`.
4. `GET /api/v1/teams`, `GET /api/v1/teams/{id}` -> `TeamRecord` (adds `revision`, `updatedAt`).
   Write path wraps `tether_team_form`, `tether_team_member_add/remove`, `tether_team_dissolve`;
   the shared policy (`permissionCeiling`, `scopesCeiling`, `rules`, `reportTo`) has no
   Tether home today and needs a team-level policy field.
5. `GET /api/v1/assignments?agent_id=&target_id=` -> `AssignmentRecord[]`
   `{id, agentId, target, role?, since}`. Writes wrap `tether_team_assign` and
   `tether_workstream_assign`; Torque task assignment needs a first-class record with
   a stable id (the GUI releases by assignment id).
6. `GET /api/v1/agents/{id}/audit` and the plan audit should accept team ids and
   `"assignments"` as subjects (the mock stores lifecycle audit under `plan.agentId`).

### Agent record additions

`identity.retired: boolean`, `identity.retiredAt: epoch ms | null`. Retired
agents stay in the catalog for history (never deleted), are excluded from launch
pickers, team membership and assignment, and can be restored.

### Semantics the server must enforce

- Risk. Retire is always `high`; so is a create or clone that carries bypass,
  unrestricted sandbox, a high scope or a dropped default guard rule (the GUI diffs
  against the safe blank draft), restoring a privileged agent, deleting a team,
  raising a team ceiling to bypass, and removing a team guard rule. `high` needs a
  non-empty `note`; reject the plan (`invalid`) without it. The GUI only mirrors this.
- Required step-up scope: `agent.create | agent.retire | agent.restore | team.write |
  assignment.write`, plus `agent:<id>` / `team:<id>` per subject (assignment plans: one
  `agent:<id>` per touched agent), `agent.write.privileged` when risk is high, and
  `policy.grant` when a privileged scope is granted. Same per-step, single-use token
  rules as B2 (approve, apply, rollback each need their own grant).
- Retire is one atomic transaction: set `enabled=false, retired=true`, remove the
  agent from every team (bumping team revisions), release its assignments, and per
  `sessions`: `drain` blocks new launches and lets sessions end, `stop` stops them.
  Report `failed` with nothing changed on any error. Restore returns the agent
  disabled with no teams or assignments. Rollback must restore agents, teams and
  assignments together as new revisions (history is never rewritten).
- Conflicts. `baseRevision` for retire/restore is the agent revision; for team
  update/delete it is the team revision; for create it is empty and apply fails with
  `conflict` if the id now exists; assignment removals conflict if the assignment is
  already gone.
- Validation beyond the GUI: id format and uniqueness, provider/model/project
  exist, members exist and are not retired, no duplicate members, a `reportTo` that
  resolves, and a warning when a member exceeds the team permission or scope ceiling.
  Org policy may veto any of it.
- Open: four-eyes for retire and privileged create (approver differs from author);
  whether a clone may copy privileged scopes at all or must re-request them; whether
  retire should be blocked while a task assigned to the agent is in progress.

### Installing the step-up in the shell

Agent-changing actions (approve, apply, rollback for edits, creates, retires, teams
and assignments) always go through `IdentityConfirm`. `AgentEditProvider` resolves it:

1. Explicit `bus` + `identity` props are used as given.
2. Under `ConsoleProvider` the installed `seams.identity` is used, EXCEPT the stock
   `MockIdentityConfirm` in `auto-grant` mode, which `ConsoleProvider` creates when the
   shell passes no `identity`. That is not a step-up, so the agent views substitute a
   local manual mock (dialog) and a bus bound to it. Pass `allowAutoGrant` only for
   fixtures and demos.
3. With no seams a local manual mock is created.

To install it in the shell:
- Manual dialog (until auth lands): `<ConsoleProvider identity={new MockIdentityConfirm({ mode: 'manual' })}>`.
  The agent views render `StepUpHost`, which draws the dialog for a manual mock. Mount
  at most one `StepUpHost` per identity, otherwise two dialogs stack.
- Real step-up: implement `IdentityConfirm.confirm(req) -> ConfirmResult` against the
  auth issuer and pass it as `ConsoleProvider identity={...}`. Any non-mock
  implementation is used untouched and brings its own UI; `StepUpHost` renders nothing
  for it. The result must carry `token`, `scope`, `expiresAt` and `what.action`, which
  the client forwards to the server for verification.
