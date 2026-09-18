package mcpadapter

import (
	"context"
)

func (a *Adapter) registerRunTools() {
	a.addTool(newTool("torque_run_list",
		withDescription(`List runs (executor invocations) for a task; newest first.
Use to audit execution history — failures, exit codes, timestamps. torque_run_get for one run by ID. Brief shape drops stdout/stderr bodies; pass verbose="true" for full RunRecord.
Response shape: data = {items: [<briefRun or RunRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleRunList)

	a.addTool(newTool("torque_run_get",
		withDescription(`Fetch one run's full RunRecord by numeric ID (pass as string).
Use when you need full stdout/stderr/duration from a specific run; torque_run_list for discovery.
Response shape: data = {<RunRecord fields>} — singleton.
Example: {"id":"99"}`),
		withString("id", required(), desc("Run ID (integer; pass as string)")),
	), a.handleRunGet)
}

func (a *Adapter) handleRunList(ctx context.Context, req map[string]any) (any, error) {
	verbose := reqStrBool(req, "verbose")
	runs, err := a.svc.Run.List(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(runs))
	for _, r := range runs {
		if verbose {
			items = append(items, r)
		} else {
			items = append(items, toBriefRun(r))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleRunGet(ctx context.Context, req map[string]any) (any, error) {
	id := int64(reqInt(req, "id"))
	run, err := a.svc.Run.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return okResult(run)
}
