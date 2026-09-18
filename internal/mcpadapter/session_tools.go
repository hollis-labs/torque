package mcpadapter

import (
	"context"
	"errors"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/sessioninput"
)

// registerSessionTools surfaces the unified agent session manager
// (CW-20260508-0001) over MCP. Tools are no-ops when the adapter has no
// agent.Manager handle wired (mcp-only stdio path); each handler returns a
// `domain` envelope explaining the missing wiring rather than panicking.
func (a *Adapter) registerSessionTools() {
	a.addTool(newTool("torque_session_create",
		withDescription(`Boot a long-lived agent session via the unified agent.Manager.Boot path.
Use to spawn an agent (Reviewer end-agent, Orchestrator, planner, etc.) whose lifetime exceeds a single task — Mode=ModeLongLived. Per-task scheduler-dispatched (one-turn) executions go through the kind=agent task path, not this tool.
Pair with torque_session_checkpoint mid-run and torque_session_resume to seed a fresh session from prior checkpoint state.
Response shape: data = <Session> singleton — ID, status, runtime descriptors, project/task soft-FKs, Mode, BootDir, WorkspaceDir, ParentSessionID.
Example: {"agent_profile":"default","workdir":"/tmp/sess","task_id":"T-123"}`),
		withString("launch_profile", desc("Torque launch_profile id (preferred). When set, drives the stable launch family resolution.")),
		withString("agent_profile", desc("Legacy agent_profile name. Honored when launch_profile is empty.")),
		withString("workdir", required(), desc("Spawned process working directory (boot dir for claude)")),
		withString("project_id", desc("Optional project soft-FK")),
		withString("task_id", desc("Optional task soft-FK")),
		withString("system_prompt", desc("Boot/system prompt for the agent")),
		sessionStringMapParam("env", "Environment additions as a native object with string values or a JSON-object string. MCP object form maps to agent.Options.Env; HTTP legacy /sessions/launch maps env [\"K=V\"] to the same Options.Env shape. Null, arrays, scalars, and non-string values are rejected."),
		sessionStringMapParam("meta", "Session metadata as a native object with string values or a JSON-object string. Maps to HTTP /sessions/launch meta and agent.Options.SessionMeta. Null, arrays, scalars, and non-string values are rejected; Torque-owned torque.* session keys are protected by Boot."),
	), a.handleSessionCreate)

	// _launch is an alias for _create — the ticket lists both. Kept as
	// distinct registrations so MCP descriptions can diverge later if
	// create-then-launch splits into two phases.
	a.addTool(newTool("torque_session_launch",
		withDescription(`Alias for torque_session_create — boots a Mode=ModeLongLived agent session via agent.Manager.Boot. Same args, same response.
Kept distinct in the registry so create-then-launch can split into two phases without a breaking rename. Today both names route to the same handler.
Response shape: data = <Session> singleton.
Example: {"agent_profile":"default","workdir":"/tmp/sess"}`),
		withString("launch_profile"),
		withString("agent_profile"),
		withString("workdir", required()),
		withString("project_id"),
		withString("task_id"),
		withString("system_prompt"),
		sessionStringMapParam("env", "Environment additions as a native object with string values or a JSON-object string. MCP object form maps to agent.Options.Env; HTTP legacy /sessions/launch maps env [\"K=V\"] to the same Options.Env shape. Null, arrays, scalars, and non-string values are rejected."),
		sessionStringMapParam("meta", "Session metadata as a native object with string values or a JSON-object string. Maps to HTTP /sessions/launch meta and agent.Options.SessionMeta. Null, arrays, scalars, and non-string values are rejected; Torque-owned torque.* session keys are protected by Boot."),
	), a.handleSessionCreate)

	a.addTool(newTool("torque_session_get",
		withDescription(`Fetch one session record by ID.
Use to inspect lifecycle status, exit code, runtime descriptors, and resume hint after launch or before resume.
Pair with torque_session_list for cohort discovery; torque_session_get is the singleton accessor.
Response shape: data = <Session> singleton.
Example: {"id":"SES-..."}`),
		withString("id", required()),
	), a.handleSessionGet)

	a.addTool(newTool("torque_session_list",
		withDescription(`List long-lived agent sessions, newest-first. Optional state/task/project filters.
Use to find live or recently-terminated sessions, audit orphan-sweep results (state=crashed), or build dashboards over running cohorts.
state filter values: launching|running|done|failed|crashed. limit caps the page.
Response shape: data = {items: [<Session>...], meta: {...}} — capped JSON.
Example: {"state":"running","task_id":"T-123"}`),
		withString("state", desc("launching|running|done|failed|crashed")),
		withString("task_id"),
		withString("project_id"),
		withString("limit"),
	), a.handleSessionList)

	a.addTool(newTool("torque_session_stop",
		withDescription(`Stop a running session. Idempotent — returns stopped:false when the session is already terminal or unknown to this process.
Use for graceful shutdown of an orchestrator or end-agent before daemon restart.
Watch goroutine records terminal state asynchronously; poll torque_session_get for status convergence.
Response shape: data = {stopped:bool, id, reason?}.
Example: {"id":"SES-..."}`),
		withString("id", required()),
	), a.handleSessionStop)

	a.addTool(newTool("torque_session_attach",
		withDescription(`Probe a session for attachability and return its metadata.
Note: the MCP transport cannot stream PTY output. Use this tool to inspect the session record and a hint pointing at the HTTP /api/v1/sessions/{id}/attach surface (where actual streaming will land in S2).
Pair with torque_session_get when you only need the snapshot.
Response shape: data = {session:<Session>, hint:string}.
Example: {"id":"SES-..."}`),
		withString("id", required()),
	), a.handleSessionAttach)

	a.addTool(newTool("torque_session_checkpoint",
		withDescription(`Persist a checkpoint (opaque caller payload + optional note) for a session.
Use to capture continuity state — orchestrator plan progress, reviewer queue depth, etc. — before a planned interruption.
Pair with torque_session_resume to spawn a new session id seeded from this checkpoint's state.
Response shape: data = <Checkpoint> singleton — id, session_id, payload, created_at.
Example: {"id":"SES-...","payload":"{\"step\":42}","note":"after design pass"}`),
		withString("id", required()),
		withString("payload", desc("Opaque JSON payload (string)")),
		withString("note"),
	), a.handleSessionCheckpoint)

	a.addTool(newTool("torque_session_resume",
		withDescription(`Resume a session: launches a NEW session id seeded from a previous session's checkpoint.
Use after a daemon restart, a graceful stop, or an orphan-sweep crash to pick up an orchestrator/reviewer where it left off.
checkpoint_id is optional — when omitted the manager picks the most recent checkpoint. agent_profile/workdir overrides default to the source session's values.
Response shape: data = <Session> for the new id (status=running on success).
Example: {"id":"SES-...","checkpoint_id":"SCP-..."}`),
		withString("id", required(), desc("Source session ID")),
		withString("checkpoint_id", desc("Optional — defaults to latest")),
		withString("launch_profile", desc("Torque launch_profile id override (preferred over agent_profile)")),
		withString("agent_profile"),
		withString("workdir"),
		withString("system_prompt"),
	), a.handleSessionResume)
}

func (a *Adapter) requireSessionMgr() (*agent.Manager, error) {
	if a.sessions == nil {
		return nil, errors.New("session manager not wired in this MCP host")
	}
	return a.sessions, nil
}

func sessionStringMapParam(name, description string) toolOpt {
	return func(t *toolSpec) {
		if t.Properties == nil {
			t.Properties = map[string]any{}
		}
		t.Properties[name] = map[string]any{
			"anyOf": []any{
				map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				map[string]any{"type": "string"},
			},
			"description": description,
		}
	}
}

func (a *Adapter) handleSessionCreate(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	launchProfile := reqStr(req, "launch_profile")
	agentProfile := reqStr(req, "agent_profile")
	// At least one selector must be provided. agent.Options.Validate
	// enforces the same invariant downstream, but surfacing it here as
	// ErrCodeArgInvalid (with a clear field hint) lets MCP clients
	// self-correct rather than seeing the failure wrapped as a generic
	// domain error from Boot.
	if launchProfile == "" && agentProfile == "" {
		return errResult(ErrCodeArgInvalid,
			"at least one of launch_profile or agent_profile is required",
			"launch_profile")
	}
	// Validate legacy agent_profile only when launch_profile is empty; the
	// resolver's legacy-compat path handles agent_profile values without
	// requiring registry membership.
	if launchProfile == "" {
		if err := config.ValidateProfileName(mgr.KnownProfiles(), agentProfile); err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "agent_profile")
		}
	}
	env, res := reqStringMap(req, "env")
	if res != nil {
		return nil, res
	}
	meta, res := reqStringMap(req, "meta")
	if res != nil {
		return nil, res
	}
	sess, err := mgr.Boot(ctx, agent.Options{
		Mode:          agent.ModeLongLived,
		LaunchProfile: launchProfile,
		AgentProfile:  agentProfile,
		Workdir:       reqStr(req, "workdir"),
		ProjectID:     reqStr(req, "project_id"),
		TaskID:        reqStr(req, "task_id"),
		SystemPrompt:  reqStr(req, "system_prompt"),
		Env:           env,
		SessionMeta:   meta,
	})
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	return okResult(sess)
}

func reqStringMap(req map[string]any, key string) (map[string]string, error) {
	raw, ok := req[key]
	if !ok {
		return nil, nil
	}
	out, err := sessioninput.DecodeStringMapValue(raw, key)
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), key)
	}
	return out, nil
}

func (a *Adapter) handleSessionGet(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	sess, err := mgr.Get(reqStr(req, "id"))
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(sess)
}

func (a *Adapter) handleSessionList(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	limit := reqInt(req, "limit")
	out, err := mgr.List(
		agent.Status(reqStr(req, "state")),
		reqStr(req, "task_id"),
		reqStr(req, "project_id"),
		limit,
	)
	if err != nil {
		return errFromService(err)
	}
	items := make([]any, 0, len(out))
	for _, s := range out {
		items = append(items, s)
	}
	return cappedJSONResult(items, defaultGenericListLimit)
}

func (a *Adapter) handleSessionStop(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	id := reqStr(req, "id")
	if err := mgr.Stop(ctx, id); err != nil {
		if errors.Is(err, agent.ErrSessionNotRunning) {
			return okResult(map[string]interface{}{"stopped": false, "reason": err.Error(), "id": id})
		}
		return errFromService(err)
	}
	return okResult(map[string]interface{}{"stopped": true, "id": id})
}

func (a *Adapter) handleSessionAttach(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	sess, err := mgr.Get(reqStr(req, "id"))
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"session": sess,
		"hint":    "MCP cannot stream PTY output; attach via HTTP /api/v1/sessions/{id}/attach (not yet enabled).",
	})
}

func (a *Adapter) handleSessionCheckpoint(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	cp, err := mgr.Checkpoint(agent.CheckpointRequest{
		SessionID: reqStr(req, "id"),
		Payload:   reqStr(req, "payload"),
		Note:      reqStr(req, "note"),
	})
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(cp)
}

func (a *Adapter) handleSessionResume(ctx context.Context, req map[string]any) (any, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	newID, err := mgr.Resume(ctx, agent.ResumeRequest{
		SessionID:     reqStr(req, "id"),
		CheckpointID:  reqStr(req, "checkpoint_id"),
		LaunchProfile: reqStr(req, "launch_profile"),
		AgentProfile:  reqStr(req, "agent_profile"),
		Workdir:       reqStr(req, "workdir"),
		SystemPrompt:  reqStr(req, "system_prompt"),
	})
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrSessionNotFound):
			return errResult(ErrCodeNotFound, err.Error(), "")
		case errors.Is(err, agent.ErrNoCheckpoint):
			return errResult(ErrCodeArgInvalid, err.Error(), "checkpoint_id")
		}
		return errFromService(err)
	}
	sess, err := mgr.Get(newID)
	if err != nil {
		return errFromService(err)
	}
	return okResult(sess)
}
