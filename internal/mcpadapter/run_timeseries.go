package mcpadapter

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/torque/internal/service"
	"strings"
)

func (a *Adapter) registerRunTimeSeriesTool() {
	opts := []toolOpt{withDescription(`Gap-free run time-series aggregates over the exact torque_run_list cohort. since/until are required inclusive run-start bounds; bucket=hour|day (default day). At most 200 intersecting buckets; larger windows reject on until, never truncate.
Response: data={bucket,tz_offset_minutes,since,until,buckets:[{start,count,prompt_tokens,completion_tokens,cost,status_counts}],totals:{count,prompt_tokens,completion_tokens,cost}}. start is the UTC instant of the local bucket edge. Empty buckets have zero totals and status_counts:{}.
Profile filters match the current task launch_profile with fallback agent_profile, not the historical run profile.
tz_offset_minutes is a fixed UTC offset (-840..840, default0), not an IANA/DST zone. Cost sums canonical ledger entries by run ID, irrespective of ledger dates. Tokens and status use run records. MCP byte overflow rejects on until; shorten the window rather than accepting an incomplete series.
Example: {"since":"2026-10-01T00:00:00Z","until":"2026-10-01T23:59:59.999999999Z","bucket":"hour"}`)}
	for _, key := range []string{"task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "bucket"} {
		opts = append(opts, withString(key, desc("Same run cohort filters; status/executor/profile accept CSV. bucket is hour or day.")))
	}
	for _, key := range []string{"since", "until"} {
		opts = append(opts, withString(key, required(), desc("Inclusive run-start bound, RFC3339 or Unix milliseconds; required.")))
	}
	opts = append(opts, withString("tz_offset_minutes", desc("Fixed offset minutes from UTC, -840..840; default0.")))
	a.addTool(newTool("torque_run_timeseries", opts...), a.handleRunTimeSeries)
}

func (a *Adapter) handleRunTimeSeries(ctx context.Context, req map[string]any) (any, error) {
	if err := validateTaskListStringArgs(req, "task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until", "bucket"); err != nil {
		return nil, err
	}
	offset, _, err := reqTaskListInt(req, "tz_offset_minutes")
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "tz_offset_minutes")
	}
	result, err := a.svc.Run.TimeSeries(service.RunTimeSeriesQuery{RunQuery: service.RunQuery{TaskID: reqStr(req, "task_id"), ProjectID: reqStr(req, "project_id"), SprintID: reqStr(req, "sprint_id"), EpicID: reqStr(req, "epic_id"), Statuses: strings.Split(reqStr(req, "status"), ","), Executors: strings.Split(reqStr(req, "executor"), ","), Profiles: strings.Split(reqStr(req, "profile"), ","), Since: reqStr(req, "since"), Until: reqStr(req, "until")}, Bucket: reqStr(req, "bucket"), TZOffsetMinutes: offset})
	if err != nil {
		return errFromService(err)
	}
	return cappedRunTimeSeriesResult(result)
}

func cappedRunTimeSeriesResult(result service.RunTimeSeriesResult) (any, error) {
	response := Response{OK: true, Data: result}
	encoded, err := json.Marshal(response)
	if err != nil {
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}
	if len(encoded) > maxMCPResponseBytes {
		return nil, argError(ErrCodeArgInvalid, "time series exceeds the MCP response budget; shorten since/until or narrow filters", "until")
	}
	return response, nil
}
