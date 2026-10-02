package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerEntityFacetTool(entity string) {
	options := []toolOpt{withDescription(`Count the whole filtered list cohort without loading list rows. Use for parent stat cards and filter choices; the corresponding list tool supplies paged records.
Response shape: data = {matching_count,bucket_limit,dimensions,facets:[{dimension,buckets:[{value,count}],total_distinct,returned,truncated}],task_rollups:{scopes:[{scope_id,total,counts}],total_distinct,returned,truncated}}.
Parent responses also include exact task_totals:{total,counts}; projects add children:{sprints,epics} per scope and child_totals:{sprints,epics}. rollup_ids selects only rollup groups (max 200 distinct IDs), not the cohort; requested zero-task cohort IDs are explicit and non-cohort IDs omitted. include_archived also controls project child counts.
Internal tasks are excluded from rollups; zero-task parents are included. bucket_limit defaults to 50/max 200 and bounds each dimension and parent groups independently. Every returned parent's status counts are complete.
Buckets sort count descending, value ascending (null last among ties); parents sort task count descending, ID ascending. Row limit/offset/cursor/sort parameters are rejected.
Example: {"status":"active","dimensions":"status","bucket_limit":"20"}`), withString("status", desc("Same status filter as the list.")), withString("include_archived", desc("Include archived parents; default false.")), withString("dimensions", desc("Comma-separated dimensions: project status; epic status,project_id,priority; sprint status,project_id,approval_mode. Defaults to all supported dimensions.")), withString("bucket_limit", desc("Bucket bound, default 50, max 200.")), withString("rollup_ids", desc("CSV parent IDs for complete rollups, maximum 200 distinct IDs. Does not restrict cohort totals or facets."))}
	if entity != "project" {
		options = append(options, withString("project_id", desc("Filter by project ID.")))
	}
	options = append(options, withString("search", desc("Same substring search as the list.")))
	if entity == "sprint" {
		options = append(options, withString("epic_id", desc("Same epic filter as the sprint list.")))
		for _, key := range []string{"over_budget", "cost_budget_min", "cost_budget_max"} {
			options = append(options, withString(key, desc("Same budget filter as the list.")))
		}
	}
	a.addTool(newTool("torque_"+entity+"_facets", options...), func(ctx context.Context, req map[string]any) (any, error) { return a.handleEntityFacets(entity, req) })
}

func (a *Adapter) handleEntityFacets(entity string, req map[string]any) (any, error) {
	status, e := reqQueryString(req, "status")
	if e != nil {
		return nil, e
	}
	archived, e := reqQueryBool(req, "include_archived")
	if e != nil {
		return nil, e
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
		raw, e := reqQueryString(req, "dimensions")
		if e != nil {
			return nil, e
		}
		opts.Dimensions = strings.Split(raw, ",")
	}
	if reqHasArg(req, "rollup_ids") {
		raw, e := reqQueryString(req, "rollup_ids")
		if e != nil {
			return nil, e
		}
		opts.RollupIDs = strings.Split(raw, ",")
	}
	project, e := reqQueryString(req, "project_id")
	if e != nil {
		return nil, e
	}
	search, e := reqQueryString(req, "search")
	if e != nil {
		return nil, e
	}
	var result sqlstore.EntityFacetResult
	var err error
	switch entity {
	case "project":
		result, err = a.svc.Project.Facets(service.ProjectFacetQuery{ProjectQuery: service.ProjectQuery{Status: status, Search: search, IncludeArchived: archived}, FacetOptions: opts})
	case "epic":
		result, err = a.svc.Epic.Facets(service.EpicFacetQuery{EpicQuery: service.EpicQuery{Status: status, ProjectID: project, Search: search, IncludeArchived: archived}, FacetOptions: opts})
	case "sprint":
		epic, e := reqQueryString(req, "epic_id")
		if e != nil {
			return nil, e
		}
		over, e := reqQueryBool(req, "over_budget")
		if e != nil {
			return nil, e
		}
		min, e := reqQueryFloat(req, "cost_budget_min")
		if e != nil {
			return nil, e
		}
		max, e := reqQueryFloat(req, "cost_budget_max")
		if e != nil {
			return nil, e
		}
		result, err = a.svc.Sprint.Facets(service.SprintFacetQuery{SprintQuery: service.SprintQuery{EpicID: epic, Status: status, Search: search, ProjectID: project, IncludeArchived: archived, OverBudget: over, CostBudgetMin: min, CostBudgetMax: max}, FacetOptions: opts})
	}
	if err != nil {
		return errFromService(err)
	}
	return cappedEntityFacetResult(result)
}

// Trim only whole buckets or parent groups under MCP's byte budget. Exact
// cohort counts, totals and distinct counts survive trimming.
func cappedEntityFacetResult(r sqlstore.EntityFacetResult) (any, error) {
	r.Facets = append([]sqlstore.EntityFacet{}, r.Facets...)
	if r.TaskRollups != nil {
		rollups := *r.TaskRollups
		r.TaskRollups = &rollups
	}
	for {
		encoded, err := json.Marshal(Response{OK: true, Data: r})
		if err != nil {
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
		if len(encoded) <= maxMCPResponseBytes {
			return Response{OK: true, Data: r}, nil
		}
		longest := -1
		for i := range r.Facets {
			if len(r.Facets[i].Buckets) > 0 && (longest < 0 || len(r.Facets[i].Buckets) > len(r.Facets[longest].Buckets)) {
				longest = i
			}
		}
		if r.TaskRollups != nil && len(r.TaskRollups.Scopes) > 0 && (longest < 0 || len(r.TaskRollups.Scopes) >= len(r.Facets[longest].Buckets)) {
			r.TaskRollups.Scopes = r.TaskRollups.Scopes[:len(r.TaskRollups.Scopes)-1]
			r.TaskRollups.Returned = len(r.TaskRollups.Scopes)
			r.TaskRollups.Truncated = true
		} else if longest >= 0 {
			f := &r.Facets[longest]
			f.Buckets = f.Buckets[:len(f.Buckets)-1]
			f.Returned = len(f.Buckets)
			f.Truncated = true
		} else {
			return errResult(ErrCodeInternal, "facet metadata exceeds response budget", "")
		}
	}
}
