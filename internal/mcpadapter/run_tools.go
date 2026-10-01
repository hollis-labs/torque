package mcpadapter

import (
	"context"
	"strconv"
	"strings"

	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerRunTools() {
	a.registerRunFacetTool()
	a.addTool(newTool("torque_run_list",
		withDescription(`Query runs across tasks, newest first by default. Cursor pages default to 50 rows, maximum 200. Filters combine with AND; status accepts CSV.
Sort by started_at/status/duration/cost, with ascending ID tie-break. Duration is completed elapsed milliseconds; unfinished runs sort at -1. include_total opts into counting the full cohort. Cursor binds sort_by/sort_dir; cannot combine cursor with positive offset.
Response shape: data = {items: [<briefRun or RunRecord>...], meta: {has_more,next_cursor,returned,limit,total?}}. next_cursor is null on the final page. verbose=true returns full records.
Example: {"task_id":"T-123","limit":"50"}`),
		withString("task_id", desc("Task ID (optional)")),
		withString("project_id", desc("Filter task project")), withString("sprint_id", desc("Filter task sprint")), withString("epic_id", desc("Filter task epic")),
		withString("status", desc("Run status or CSV statuses")), withString("since", desc("Inclusive started_at lower bound, RFC3339 or unix millis")), withString("until", desc("Inclusive started_at upper bound, RFC3339 or unix millis")),
		withString("limit", desc("Page size, default 50, max 200")), withString("offset", desc("Jump-to-page offset; cannot combine with cursor")),
		withString("cursor", desc("Opaque next_cursor from preceding page")), withString("sort_by", desc("started_at (default), status, duration, cost")), withString("sort_dir", desc("asc or desc (default)")),
		withString("include_total", desc("Opt into total count, default false")), withString("verbose", desc("Full records, default false")),
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
	if err := validateTaskListStringArgs(req, "task_id", "project_id", "sprint_id", "epic_id", "status", "since", "until", "sort_by", "sort_dir", "cursor"); err != nil {
		return nil, err
	}
	q := service.RunQuery{TaskID: reqStr(req, "task_id"), ProjectID: reqStr(req, "project_id"), SprintID: reqStr(req, "sprint_id"), EpicID: reqStr(req, "epic_id"), Statuses: strings.Split(reqStr(req, "status"), ","), Since: reqStr(req, "since"), Until: reqStr(req, "until"), SortBy: reqStr(req, "sort_by"), SortDir: reqStr(req, "sort_dir"), Cursor: reqStr(req, "cursor")}
	var err error
	if q.Limit, _, err = reqTaskListInt(req, "limit"); err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "limit")
	}
	if q.Offset, q.OffsetSet, err = reqTaskListInt(req, "offset"); err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "offset")
	}
	if q.IncludeTotal, err = reqTaskListBool(req, "include_total"); err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "include_total")
	}
	verbose, err := reqTaskListBool(req, "verbose")
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "verbose")
	}
	result, err := a.svc.Run.Query(q)
	if err != nil {
		return errFromService(err)
	}
	items := make([]any, 0, len(result.Runs))
	for _, r := range result.Runs {
		if verbose {
			items = append(items, r)
		} else {
			items = append(items, toBriefRun(r))
		}
	}
	var offset []int
	if result.Meta.OffsetMeta != nil {
		offset = []int{result.Meta.Offset}
	}
	response, err := cappedCursorJSONResultWithTotal(items, result.Meta.Limit, result.Meta.Total, result.SortBy, result.SortDir, result.Meta.HasMore, func(i int) (string, string) {
		return result.Runs[i].QuerySortValue, strconv.FormatInt(result.Runs[i].ID, 10)
	}, offset...)
	if err != nil {
		return response, err
	}
	// A single oversized verbose record cannot produce a resumable page.
	// Return an actionable error rather than has_more with no continuation.
	if r, ok := response.(Response); ok {
		if env, ok := r.Data.(listEnvelopeCursor); ok && len(items) > 0 && len(env.Items) == 0 {
			return nil, argError(ErrCodeArgInvalid, "one run exceeds the MCP response budget; use verbose=false or fetch it with torque_run_get", "verbose")
		}
	}
	return response, nil
}

func (a *Adapter) handleRunGet(ctx context.Context, req map[string]any) (any, error) {
	id := int64(reqInt(req, "id"))
	run, err := a.svc.Run.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return okResult(run)
}
