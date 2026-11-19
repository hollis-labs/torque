package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/service"
)

const unitDispatchDescription = `Dispatch a unit of work: launch a Tether team for a task that carries an execution plan (metadata.unit_plan).
Use to start a unit on demand, with or without the scheduler enabled. Torque (no agent) validates the plan, forms a team (1 orchestrator + 1-3 workers + 1 reviewer; Astra orchestrator/reviewer, Sol or Agy workers), briefs the orchestrator with the task, workstream, goal, done-when, scope and escalation target, records the team run id on the task (metadata.unit_run) and moves the task to doing. The team dissolves when the orchestrator reports its result; Torque then moves the task to review (per on_done) and posts the result, or blocks it with the reason.
Idempotent: a task that already has a live run returns that run with reused=true. If formation or briefing fails the run is dissolved, the task stays todo with a blocked_reason and is set manual so the scheduler will not retry it; fix the cause and call again. The plan contract is in docs/unit-of-work.md; create a task with torque_task_create_from_template (template unit-of-work).
Response shape: data = {task_id, run_id, attempt, state, reused}. Runs in the serve process; stdio MCP clients are proxied to POST /api/v1/units/dispatch.
Example: {"task_id":"CW-20261118-0050","workers":"2","worker_runtime":"agy"}`

func (a *Adapter) registerUnitTools() {
	a.addTool(newTool("torque_unit_dispatch",
		withDescription(unitDispatchDescription),
		withString("task_id", required(), desc("Task with an execution plan (metadata.unit_plan).")),
		withString("workers", desc("Override the plan's worker count, 1-3. Above 1 needs registered pool identities. (integer)")),
		withString("worker_runtime", desc("Override the plan's worker runtime: sol or agy.")),
	), a.handleUnitDispatch)
}

func (a *Adapter) handleUnitDispatch(ctx context.Context, req map[string]any) (any, error) {
	in := service.UnitDispatchInput{
		TaskID: reqStr(req, "task_id"), Workers: reqInt(req, "workers"), WorkerRuntime: reqStr(req, "worker_runtime"),
	}
	if in.TaskID == "" {
		return errResult(ErrCodeArgInvalid, "task_id required", "task_id")
	}
	if a.svc.Unit.Enabled() {
		res, err := a.svc.Unit.Dispatch(ctx, in)
		if err != nil {
			return errResult(ErrCodeDomain, err.Error(), "")
		}
		return okResult(res)
	}
	res, err := proxyUnitDispatch(ctx, in)
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	return okResult(res)
}

// proxyUnitDispatch forwards to the serve process, which owns the Tether
// connection and the run tracker.
func proxyUnitDispatch(ctx context.Context, in service.UnitDispatchInput) (*service.UnitDispatchResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config for unit dispatch proxy: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/units/dispatch", cfg.HTTPPort)
	body, _ := json.Marshal(map[string]any{"task_id": in.TaskID, "workers": in.Workers, "worker_runtime": in.WorkerRuntime})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("unit dispatch unavailable: no in-process dispatcher and proxy to %s failed: %w", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return nil, fmt.Errorf("unit dispatch: %s: %s", resp.Status, msg)
	}
	var out service.UnitDispatchResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode unit dispatch response: %w", err)
	}
	return &out, nil
}
