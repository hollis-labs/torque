package mcpadapter

import (
	"context"
	"errors"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerSessionTools surfaces the long-lived agent session manager
// (CW-20260503-0014) over MCP. Tools are no-ops when the adapter has no
// SessionMgr handle wired (mcp-only stdio path); each handler returns a
// `domain` envelope explaining the missing wiring rather than panicking.
func (a *Adapter) registerSessionTools() {
	a.server.AddTool(mcp.NewTool("clockwork_session_create",
		mcp.WithDescription(`Create AND launch a long-lived agent session (sessionmgr).
Use to spawn an agent (Reviewer end-agent, Orchestrator, planner, etc.) whose lifetime exceeds a single task — distinct from per-task cliexec sessions which spawn-and-die.
Pair with clockwork_session_checkpoint mid-run and clockwork_session_resume to seed a fresh session from prior checkpoint state.
Response shape: data = <Session> singleton — ID, status, runtime descriptors, project/task soft-FKs.
Example: {"agent_profile":"default","workdir":"/tmp/sess","task_id":"T-123"}`),
		mcp.WithString("agent_profile", mcp.Required(), mcp.Description("Clockwork agent profile name")),
		mcp.WithString("workdir", mcp.Required(), mcp.Description("Spawned process working directory (boot dir for claude)")),
		mcp.WithString("project_id", mcp.Description("Optional project soft-FK")),
		mcp.WithString("task_id", mcp.Description("Optional task soft-FK")),
		mcp.WithString("system_prompt", mcp.Description("Boot/system prompt for the agent")),
	), a.handleSessionCreate)

	// _launch is an alias for _create — the ticket lists both. Kept as
	// distinct registrations so MCP descriptions can diverge later if
	// create-then-launch splits into two phases.
	a.server.AddTool(mcp.NewTool("clockwork_session_launch",
		mcp.WithDescription(`Alias for clockwork_session_create — same args, same response.
Kept distinct in the registry so create-then-launch can split into two phases without a breaking rename. Today both names route to the same handler.
Use either; agents picking from the tool catalog should treat them as identical surfaces.
Response shape: data = <Session> singleton.
Example: {"agent_profile":"default","workdir":"/tmp/sess"}`),
		mcp.WithString("agent_profile", mcp.Required()),
		mcp.WithString("workdir", mcp.Required()),
		mcp.WithString("project_id"),
		mcp.WithString("task_id"),
		mcp.WithString("system_prompt"),
	), a.handleSessionCreate)

	a.server.AddTool(mcp.NewTool("clockwork_session_get",
		mcp.WithDescription(`Fetch one session record by ID.
Use to inspect lifecycle status, exit code, runtime descriptors, and resume hint after launch or before resume.
Pair with clockwork_session_list for cohort discovery; clockwork_session_get is the singleton accessor.
Response shape: data = <Session> singleton.
Example: {"id":"SES-..."}`),
		mcp.WithString("id", mcp.Required()),
	), a.handleSessionGet)

	a.server.AddTool(mcp.NewTool("clockwork_session_list",
		mcp.WithDescription(`List long-lived agent sessions, newest-first. Optional state/task/project filters.
Use to find live or recently-terminated sessions, audit orphan-sweep results (state=crashed), or build dashboards over running cohorts.
state filter values: launching|running|done|failed|crashed. limit caps the page.
Response shape: data = {items: [<Session>...], meta: {...}} — capped JSON.
Example: {"state":"running","task_id":"T-123"}`),
		mcp.WithString("state", mcp.Description("launching|running|done|failed|crashed")),
		mcp.WithString("task_id"),
		mcp.WithString("project_id"),
		mcp.WithString("limit"),
	), a.handleSessionList)

	a.server.AddTool(mcp.NewTool("clockwork_session_stop",
		mcp.WithDescription(`Stop a running session. Idempotent — returns stopped:false when the session is already terminal or unknown to this process.
Use for graceful shutdown of an orchestrator or end-agent before daemon restart.
Watch goroutine records terminal state asynchronously; poll clockwork_session_get for status convergence.
Response shape: data = {stopped:bool, id, reason?}.
Example: {"id":"SES-..."}`),
		mcp.WithString("id", mcp.Required()),
	), a.handleSessionStop)

	a.server.AddTool(mcp.NewTool("clockwork_session_attach",
		mcp.WithDescription(`Probe a session for attachability and return its metadata.
Note: the MCP transport cannot stream PTY output. Use this tool to inspect the session record and a hint pointing at the HTTP /api/v1/sessions/{id}/attach surface (where actual streaming will land in S2).
Pair with clockwork_session_get when you only need the snapshot.
Response shape: data = {session:<Session>, hint:string}.
Example: {"id":"SES-..."}`),
		mcp.WithString("id", mcp.Required()),
	), a.handleSessionAttach)

	a.server.AddTool(mcp.NewTool("clockwork_session_checkpoint",
		mcp.WithDescription(`Persist a checkpoint (opaque caller payload + optional note) for a session.
Use to capture continuity state — orchestrator plan progress, reviewer queue depth, etc. — before a planned interruption.
Pair with clockwork_session_resume to spawn a new session id seeded from this checkpoint's state.
Response shape: data = <Checkpoint> singleton — id, session_id, payload, created_at.
Example: {"id":"SES-...","payload":"{\"step\":42}","note":"after design pass"}`),
		mcp.WithString("id", mcp.Required()),
		mcp.WithString("payload", mcp.Description("Opaque JSON payload (string)")),
		mcp.WithString("note"),
	), a.handleSessionCheckpoint)

	a.server.AddTool(mcp.NewTool("clockwork_session_resume",
		mcp.WithDescription(`Resume a session: launches a NEW session id seeded from a previous session's checkpoint.
Use after a daemon restart, a graceful stop, or an orphan-sweep crash to pick up an orchestrator/reviewer where it left off.
checkpoint_id is optional — when omitted the manager picks the most recent checkpoint. agent_profile/workdir overrides default to the source session's values.
Response shape: data = <Session> for the new id (status=running on success).
Example: {"id":"SES-...","checkpoint_id":"SCP-..."}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Source session ID")),
		mcp.WithString("checkpoint_id", mcp.Description("Optional — defaults to latest")),
		mcp.WithString("agent_profile"),
		mcp.WithString("workdir"),
		mcp.WithString("system_prompt"),
	), a.handleSessionResume)
}

func (a *Adapter) requireSessionMgr() (*sessionmgr.Manager, error) {
	if a.sessions == nil {
		return nil, errors.New("session manager not wired in this MCP host")
	}
	return a.sessions, nil
}

func (a *Adapter) handleSessionCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	id, err := mgr.Launch(ctx, sessionmgr.LaunchRequest{
		AgentProfile: reqStr(req, "agent_profile"),
		Workdir:      reqStr(req, "workdir"),
		ProjectID:    reqStr(req, "project_id"),
		TaskID:       reqStr(req, "task_id"),
		SystemPrompt: reqStr(req, "system_prompt"),
	})
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	sess, err := mgr.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return okResult(sess)
}

func (a *Adapter) handleSessionGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	sess, err := mgr.Get(reqStr(req, "id"))
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(sess)
}

func (a *Adapter) handleSessionList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	limit := reqInt(req, "limit")
	out, err := mgr.List(
		sessionmgr.Status(reqStr(req, "state")),
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

func (a *Adapter) handleSessionStop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	id := reqStr(req, "id")
	if err := mgr.Stop(ctx, id); err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotRunning) {
			return okResult(map[string]interface{}{"stopped": false, "reason": err.Error(), "id": id})
		}
		return errFromService(err)
	}
	return okResult(map[string]interface{}{"stopped": true, "id": id})
}

func (a *Adapter) handleSessionAttach(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	sess, err := mgr.Get(reqStr(req, "id"))
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"session": sess,
		"hint":    "MCP cannot stream PTY output; attach via HTTP /api/v1/sessions/{id}/attach (not yet enabled).",
	})
}

func (a *Adapter) handleSessionCheckpoint(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	cp, err := mgr.Checkpoint(sessionmgr.CheckpointRequest{
		SessionID: reqStr(req, "id"),
		Payload:   reqStr(req, "payload"),
		Note:      reqStr(req, "note"),
	})
	if err != nil {
		if errors.Is(err, sessionmgr.ErrSessionNotFound) {
			return errResult(ErrCodeNotFound, err.Error(), "")
		}
		return errFromService(err)
	}
	return okResult(cp)
}

func (a *Adapter) handleSessionResume(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mgr, err := a.requireSessionMgr()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	newID, err := mgr.Resume(ctx, sessionmgr.ResumeRequest{
		SessionID:    reqStr(req, "id"),
		CheckpointID: reqStr(req, "checkpoint_id"),
		AgentProfile: reqStr(req, "agent_profile"),
		Workdir:      reqStr(req, "workdir"),
		SystemPrompt: reqStr(req, "system_prompt"),
	})
	if err != nil {
		switch {
		case errors.Is(err, sessionmgr.ErrSessionNotFound):
			return errResult(ErrCodeNotFound, err.Error(), "")
		case errors.Is(err, sessionmgr.ErrNoCheckpoint):
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
