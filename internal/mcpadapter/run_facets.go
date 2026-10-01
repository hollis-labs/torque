package mcpadapter

import (
	"context"
	"github.com/hollis-labs/torque/internal/service"
	"strings"
)

func (a *Adapter) registerRunFacetTool() {
	options := []toolOpt{withDescription(`Aggregate the entire filtered runs cohort using the same predicates as torque_run_list. Use for stat cards without paging runs.
Response shape: data = {matching_count,bucket_limit,dimensions,facets:[{dimension,buckets:[{value,count}],total_distinct,returned,truncated}],totals:{cost,prompt_tokens,completion_tokens}}.
Cost sums canonical cost_ledger entries by run ID over the exact matching runs; tokens use run records. since/until filter run start times, never ledger timestamps. Runs without ledger entries contribute zero cost.
The profile bucket is the current task profile, not the profile at run time: nonempty launch_profile, fallback agent_profile. No historical profile snapshot is stored on runs.
bucket_limit defaults to 50/max 200. Buckets sort count descending then value ascending (null last among ties). Row limit/offset/cursor/sort inputs are rejected.
Example: {"project_id":"PRJ-1","since":"2026-10-01T00:00:00Z","dimensions":"status,profile","bucket_limit":"20"}`)}
	for _, key := range []string{"task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until"} {
		options = append(options, withString(key, desc("Same filter as torque_run_list; status/executor/profile accept CSV, since/until inclusive RFC3339 or unix millis.")))
	}
	options = append(options, withString("dimensions", desc("CSV: status,executor,profile; defaults to all.")), withString("bucket_limit", desc("Bucket bound, default 50, max 200.")))
	a.addTool(newTool("torque_run_facets", options...), a.handleRunFacets)
}
func (a *Adapter) handleRunFacets(ctx context.Context, req map[string]any) (any, error) {
	if err := validateTaskListStringArgs(req, "task_id", "project_id", "sprint_id", "epic_id", "status", "executor", "profile", "since", "until", "dimensions"); err != nil {
		return nil, err
	}
	opts := service.FacetOptions{}
	if reqHasArg(req, "bucket_limit") {
		n, e := reqQueryInt(req, "bucket_limit")
		if e != nil {
			return nil, e
		}
		opts.BucketLimit = n
	}
	if reqHasArg(req, "dimensions") {
		opts.Dimensions = strings.Split(reqStr(req, "dimensions"), ",")
	}
	result, err := a.svc.Run.Facets(service.RunFacetQuery{RunQuery: service.RunQuery{TaskID: reqStr(req, "task_id"), ProjectID: reqStr(req, "project_id"), SprintID: reqStr(req, "sprint_id"), EpicID: reqStr(req, "epic_id"), Statuses: strings.Split(reqStr(req, "status"), ","), Executors: strings.Split(reqStr(req, "executor"), ","), Profiles: strings.Split(reqStr(req, "profile"), ","), Since: reqStr(req, "since"), Until: reqStr(req, "until")}, FacetOptions: opts})
	if err != nil {
		return errFromService(err)
	}
	return cappedEntityFacetResult(result)
}
