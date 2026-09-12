package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

const (
	defaultHTTPTaskListLimit = 50
	maxHTTPTaskListLimit     = 200
)

// taskJSON converts a TaskRecord to a JSON-friendly map with snake_case keys
// and proper null handling for sql.Null* types. Tags, the run aggregate,
// subtodos, and collectionName are passed in so the caller can batch-load
// them rather than requiring a store handle here. When agg is nil, stats
// render as zeroes. Nil subtodos is serialized as an empty array so the
// client can always rely on the key being present. collectionName is
// surfaced as null when the task has no collection_id or the lookup
// missed (e.g. archived collection still referenced by a task).
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord, dependsOn []string, agg *sqlstore.TaskRunAggregate, subtodos []sqlstore.Subtodo, collectionName string) map[string]interface{} {
	stats := map[string]interface{}{
		"run_count":         0,
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"cost":              0.0,
		// cost_source surfaces the canonical "where did this number come from?"
		// signal (measured | estimated | unknown | "" for no-runs) so the GUI
		// can render a badge alongside the dollar figure. Empty string means
		// there are no cost_ledger rows yet — the dashboard renders "—" in
		// that case rather than the misleading "$0.00".
		"cost_source": "",
	}
	if agg != nil {
		stats["run_count"] = agg.Count
		stats["prompt_tokens"] = agg.PromptTokens
		stats["completion_tokens"] = agg.CompletionTokens
		stats["cost"] = agg.Cost
		stats["cost_source"] = agg.CostSource
	}
	if subtodos == nil {
		subtodos = []sqlstore.Subtodo{}
	}
	if dependsOn == nil {
		dependsOn = []string{}
	}
	body := map[string]interface{}{
		"id":                 t.ID,
		"title":              t.Title,
		"description":        t.Description,
		"status":             t.Status,
		"priority":           t.Priority,
		"tags":               tagsJSON(tags),
		"manual":             t.Manual,
		"executor":           t.Executor,
		"launch_profile":     t.LaunchProfile,
		"agent_profile":      t.AgentProfile,
		"working_dir":        t.WorkingDir,
		"tools":              parseStringArray(t.Tools),
		"permissions":        parseFreeMap(t.Permissions),
		"environment":        parseStringMap(t.Environment),
		"system_prompt":      t.SystemPrompt,
		"agent_file":         t.AgentFile,
		"files":              parseStringArray(t.Files),
		"cost_budget":        nullFloat(t.CostBudget),
		"max_retries":        t.MaxRetries,
		"max_duration_ms":    nullInt(t.MaxDurationMs),
		"token_budget":       nullInt(t.TokenBudget),
		"on_done":            t.OnDone,
		"on_fail":            t.OnFail,
		"on_review":          t.OnReview,
		"on_done_merge":      t.OnDoneMerge,
		"escalation_chain":   parseStringArray(t.EscalationChain),
		"quality_gates":      parseStringArray(t.QualityGates),
		"deliverables":       parseDeliverables(t.Deliverables),
		"deliverable_preset": t.DeliverablePreset,
		"depends_on":         dependsOn,
		"blocked_reason":     t.BlockedReason,
		"metadata":           parseFreeMap(t.Metadata),
		"sprint_id":          nullStr(t.SprintID),
		"project_id":         nullStr(t.ProjectID),
		"epic_id":            nullStr(t.EpicID),
		"created_at":         t.CreatedAt,
		"updated_at":         t.UpdatedAt,

		"kind":                   t.Kind,
		"source_type":            t.SourceType,
		"source_ref":             nullStr(t.SourceRef),
		"trust":                  t.Trust,
		"checkpoint_mode":        t.CheckpointMode,
		"on_checkpoint_response": t.OnCheckpointResponse,

		"parent_id": nullStr(t.ParentID),

		// Collections (migration 020). NULL collection_id with non-NULL
		// added_to_collections_at means "in the inbox" (touched by collection
		// flow but currently unassigned); both NULL means "fresh, never
		// touched by collections". collection_position is included for
		// completeness but the GUI doesn't currently consume it.
		"collection_id":           nullStr(t.CollectionID),
		"collection_name":         nullableString(collectionName),
		"collection_position":     nullInt(t.CollectionPosition),
		"added_to_collections_at": nullTime(t.AddedToCollectionsAt),

		// Run roll-up — prompt/completion/cost summed across all recorded
		// runs for this task, plus a turn count. Nil aggregate renders
		// zeroes so clients can rely on the keys always being present.
		"stats": stats,

		// Structural checklist — auto-extracted from markdown `- [ ]` in the
		// description at create time, also writable via MCP/HTTP. Empty array
		// (never null) so the client can map without a guard.
		"subtodos": subtodos,
	}
	if policy, ok := requiredWorkflowPolicyFromTask(t); ok {
		body["required_workflow_policy"] = policy
	}
	return body
}

func requiredWorkflowPolicyFromTask(t *sqlstore.TaskRecord) (hitl.RequiredWorkflowPolicy, bool) {
	if !t.Metadata.Valid || t.Metadata.String == "" {
		return hitl.RequiredWorkflowPolicy{}, false
	}
	md := map[string]any{}
	if err := json.Unmarshal([]byte(t.Metadata.String), &md); err != nil {
		return hitl.RequiredWorkflowPolicy{}, false
	}
	policy, ok, err := hitl.ParseRequiredWorkflowFromMetadata(md)
	if err != nil {
		return hitl.RequiredWorkflowPolicy{}, false
	}
	return policy, ok
}

// tasksJSON converts a slice of TaskRecord to a JSON-friendly slice.
// Loads linked tags, run aggregates, and subtodos per-task (N+1 — acceptable
// at current scale; each is an indexed single-row read per task). Collection
// names are resolved in ONE batch query for all unique collection_ids in
// the slice — saves an N+1 on the lookup that the GUI uses for the badge.
func (s *Server) tasksJSON(tasks []sqlstore.TaskRecord) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, len(tasks))
	collectionNames := s.collectionNamesForTasks(tasks)
	for i := range tasks {
		tags, err := s.svc.Task.ListTags(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		deps, err := s.svc.Task.ListDependencyIDs(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		agg, err := s.svc.Run.Aggregate(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		subs, err := s.svc.Task.ListSubtodos(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		out[i] = taskJSON(&tasks[i], tags, deps, agg, subs, collectionNames[tasks[i].CollectionID.String])
	}
	return out, nil
}

// collectionNamesForTasks batch-resolves collection names for the unique
// non-NULL collection_ids in the slice. Returns an empty map (not nil) on
// any failure — surfacing collection_name is best-effort and shouldn't
// fail the whole task list response.
func (s *Server) collectionNamesForTasks(tasks []sqlstore.TaskRecord) map[string]string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(tasks))
	for i := range tasks {
		if !tasks[i].CollectionID.Valid || tasks[i].CollectionID.String == "" {
			continue
		}
		id := tasks[i].CollectionID.String
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]string{}
	}
	names, err := s.svc.Collection.LookupNames(ids)
	if err != nil {
		log.Printf("collection name lookup failed (continuing without names): %v", err)
		return map[string]string{}
	}
	return names
}

// collectionNameForTask resolves the collection name for a single task.
// Returns an empty string when the task has no collection_id or the
// lookup fails — best-effort, never blocks the response.
func (s *Server) collectionNameForTask(t *sqlstore.TaskRecord) string {
	if t == nil || !t.CollectionID.Valid || t.CollectionID.String == "" {
		return ""
	}
	names, err := s.svc.Collection.LookupNames([]string{t.CollectionID.String})
	if err != nil {
		return ""
	}
	return names[t.CollectionID.String]
}

// nullableString is the JSON-friendly equivalent of nullStr for plain
// Go strings — empty becomes JSON null, non-empty becomes the string.
// Used for batch-resolved fields like collection_name.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullFloat(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

func nullInt(ni sql.NullInt64) interface{} {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func nullTime(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

// TaskCreateRequest is the JSON request body for POST /api/v1/tasks.
// Field types are non-pointer where the zero value is acceptable as
// "not provided" (e.g. empty string), and pointer where we need to
// distinguish "not provided" from "explicit zero" (numeric nullables).
type TaskCreateRequest struct {
	Title             string                `json:"title"`
	Description       string                `json:"description,omitempty"`
	Priority          int                   `json:"priority,omitempty"`
	Tags              []string              `json:"tags,omitempty"`
	Manual            bool                  `json:"manual,omitempty"`
	Executor          string                `json:"executor,omitempty"`
	LaunchProfile     string                `json:"launch_profile,omitempty"`
	AgentProfile      string                `json:"agent_profile,omitempty"`
	WorkingDir        string                `json:"working_dir,omitempty"`
	Tools             []string              `json:"tools,omitempty"`
	Permissions       map[string]any        `json:"permissions,omitempty"`
	Environment       map[string]string     `json:"environment,omitempty"`
	SystemPrompt      string                `json:"system_prompt,omitempty"`
	AgentFile         string                `json:"agent_file,omitempty"`
	Files             []string              `json:"files,omitempty"`
	CostBudget        *float64              `json:"cost_budget,omitempty"`
	MaxRetries        *int                  `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                `json:"token_budget,omitempty"`
	OnDone            string                `json:"on_done,omitempty"`
	OnFail            string                `json:"on_fail,omitempty"`
	OnReview          string                `json:"on_review,omitempty"`
	OnDoneMerge       string                `json:"on_done_merge,omitempty"`
	EscalationChain   []string              `json:"escalation_chain,omitempty"`
	QualityGates      []string              `json:"quality_gates,omitempty"`
	Deliverables      []service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset string                `json:"deliverable_preset,omitempty"`
	DependsOn         []string              `json:"depends_on,omitempty"`
	BlockedReason     string                `json:"blocked_reason,omitempty"`
	Metadata          map[string]any        `json:"metadata,omitempty"`
	SprintID          string                `json:"sprint_id,omitempty"`
	ProjectID         string                `json:"project_id,omitempty"`
	EpicID            string                `json:"epic_id,omitempty"`

	// Facet fields (migration 007)
	Kind                 string `json:"kind,omitempty"`
	SourceType           string `json:"source_type,omitempty"`
	SourceRef            string `json:"source_ref,omitempty"`
	Trust                string `json:"trust,omitempty"`
	CheckpointMode       string `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse string `json:"on_checkpoint_response,omitempty"`

	// Parent linkage (migration 013)
	ParentID string `json:"parent_id,omitempty"`
}

// TaskUpdateRequest is the JSON request body for PUT /api/v1/tasks/:id.
// Every field is a pointer so "not provided" (nil) is distinguishable from
// "explicit zero/empty value". The Status field is present only so the
// handler can detect and reject it — status changes go through the
// dedicated /transition endpoint instead.
type TaskUpdateRequest struct {
	Title             *string                `json:"title,omitempty"`
	Description       *string                `json:"description,omitempty"`
	Priority          *int                   `json:"priority,omitempty"`
	Tags              *[]string              `json:"tags,omitempty"`
	Manual            *bool                  `json:"manual,omitempty"`
	Executor          *string                `json:"executor,omitempty"`
	LaunchProfile     *string                `json:"launch_profile,omitempty"`
	AgentProfile      *string                `json:"agent_profile,omitempty"`
	WorkingDir        *string                `json:"working_dir,omitempty"`
	Tools             *[]string              `json:"tools,omitempty"`
	Permissions       *map[string]any        `json:"permissions,omitempty"`
	Environment       *map[string]string     `json:"environment,omitempty"`
	SystemPrompt      *string                `json:"system_prompt,omitempty"`
	AgentFile         *string                `json:"agent_file,omitempty"`
	Files             *[]string              `json:"files,omitempty"`
	CostBudget        *float64               `json:"cost_budget,omitempty"`
	MaxRetries        *int                   `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                 `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                 `json:"token_budget,omitempty"`
	OnDone            *string                `json:"on_done,omitempty"`
	OnFail            *string                `json:"on_fail,omitempty"`
	OnReview          *string                `json:"on_review,omitempty"`
	OnDoneMerge       *string                `json:"on_done_merge,omitempty"`
	EscalationChain   *[]string              `json:"escalation_chain,omitempty"`
	QualityGates      *[]string              `json:"quality_gates,omitempty"`
	Deliverables      *[]service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset *string                `json:"deliverable_preset,omitempty"`
	DependsOn         *[]string              `json:"depends_on,omitempty"`
	BlockedReason     *string                `json:"blocked_reason,omitempty"`
	Metadata          *map[string]any        `json:"metadata,omitempty"`
	SprintID          *string                `json:"sprint_id,omitempty"`
	ProjectID         *string                `json:"project_id,omitempty"`
	EpicID            *string                `json:"epic_id,omitempty"`

	// Facet fields (migration 007)
	Kind                 *string `json:"kind,omitempty"`
	SourceType           *string `json:"source_type,omitempty"`
	SourceRef            *string `json:"source_ref,omitempty"`
	Trust                *string `json:"trust,omitempty"`
	CheckpointMode       *string `json:"checkpoint_mode,omitempty"`
	OnCheckpointResponse *string `json:"on_checkpoint_response,omitempty"`

	// Parent linkage (migration 013). Empty string clears the parent.
	ParentID *string `json:"parent_id,omitempty"`

	// Status is included only for detection — the handler returns 400 if it
	// is non-nil and points the caller to POST /tasks/:id/transition.
	Status *string `json:"status,omitempty"`
}

// parseStringArray parses a NullString containing a JSON-encoded []string.
// Returns an empty slice (not nil) on missing or invalid data so the JSON
// response shape stays stable.
func parseStringArray(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// parseStringMap parses a NullString containing a JSON-encoded map[string]string.
func parseStringMap(ns sql.NullString) map[string]string {
	if !ns.Valid || ns.String == "" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// parseFreeMap parses a NullString containing a JSON-encoded free-form map.
func parseFreeMap(ns sql.NullString) map[string]any {
	if !ns.Valid || ns.String == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// parseDeliverables parses a NullString containing a JSON-encoded []Deliverable.
func parseDeliverables(ns sql.NullString) []service.Deliverable {
	if !ns.Valid || ns.String == "" {
		return []service.Deliverable{}
	}
	var out []service.Deliverable
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []service.Deliverable{}
	}
	return out
}

// nullJSONString marshals v to JSON and wraps the result in a *sql.NullString
// suitable for assigning to a TaskUpdate pointer field. Returns a nil-valued
// NullString if marshaling fails (should never happen for well-formed Go values
// the request layer accepts).
func nullJSONString(v any) *sql.NullString {
	raw, err := json.Marshal(v)
	if err != nil {
		return &sql.NullString{Valid: false}
	}
	return &sql.NullString{String: string(raw), Valid: true}
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeFieldError(w, http.StatusBadRequest, "query", "invalid query string: "+err.Error())
		return
	}
	query, qerr := parseHTTPTaskQuery(q, false)
	if qerr != nil {
		writeFieldError(w, http.StatusBadRequest, qerr.field, qerr.Error())
		return
	}
	query.WithTotal = true

	result, err := s.svc.Task.Query(query)
	if err != nil {
		var verr *service.ValidationError
		if errors.As(err, &verr) {
			writeFieldError(w, http.StatusBadRequest, verr.Field, verr.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks := result.Tasks
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasMore := result.HasMoreFromQuery
	var nextOffset interface{}
	var continuation interface{}
	var nextCursor interface{}
	if hasMore {
		next := result.Offset + len(out)
		if len(result.Tasks) > 0 {
			cursor := service.TaskQueryCursor(result.Tasks[len(result.Tasks)-1], result.SortBy, result.SortDir)
			nextCursor = cursor
		}
		if query.Cursor != "" {
			nextOffset = nil
			continuation = map[string]interface{}{
				"limit":    result.Limit,
				"sort_by":  result.SortBy,
				"sort_dir": result.SortDir,
				"cursor":   nextCursor,
			}
		} else {
			nextOffset = next
			continuation = map[string]interface{}{
				"limit":  result.Limit,
				"offset": next,
			}
		}
	} else {
		nextOffset = nil
		nextCursor = nil
		continuation = nil
	}
	// Offset pagination reports an exact count for the matching cohort, but
	// concurrent writes between requests can still shift later pages.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks":        out,
		"total":        result.Total,
		"returned":     len(out),
		"limit":        result.Limit,
		"offset":       result.Offset,
		"has_more":     hasMore,
		"next_offset":  nextOffset,
		"next_cursor":  nextCursor,
		"sort_by":      result.SortBy,
		"sort_dir":     result.SortDir,
		"continuation": continuation,
	})
}

func (s *Server) taskFacets(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeFieldError(w, http.StatusBadRequest, "query", "invalid query string: "+err.Error())
		return
	}
	for _, key := range []string{"limit", "offset", "cursor", "sort_by", "sort_dir"} {
		if _, ok := q[key]; ok {
			writeFieldError(w, http.StatusBadRequest, key, key+" is not supported by task facets; facets always count the whole matching cohort")
			return
		}
	}
	query, qerr := parseHTTPTaskQuery(q, true)
	if qerr != nil {
		writeFieldError(w, http.StatusBadRequest, qerr.field, qerr.Error())
		return
	}
	fq := service.TaskFacetQuery{TaskQuery: query}
	if _, ok := q["dimensions"]; ok {
		fq.Dimensions = splitHTTPFacetCSV(q.Get("dimensions"))
	}
	if _, ok := q["bucket_limit"]; ok {
		n, err := parseTaskListInt(q.Get("bucket_limit"), "bucket_limit")
		if err != nil {
			writeFieldError(w, http.StatusBadRequest, "bucket_limit", err.Error())
			return
		}
		fq.BucketLimit = n
	}
	result, err := s.svc.Task.Facets(fq)
	if err != nil {
		var verr *service.ValidationError
		if errors.As(err, &verr) {
			writeFieldError(w, http.StatusBadRequest, verr.Field, verr.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseHTTPTaskQuery(q url.Values, facet bool) (service.TaskQuery, *taskListQueryError) {
	query := service.TaskQuery{}
	if err := validateTaskListQueryKeys(q, facet); err != nil {
		return query, err
	}
	if v := q.Get("status"); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) == 1 {
			query.Status = parts[0]
		} else {
			query.Statuses = parts
		}
	}
	if _, ok := q["priority"]; ok {
		priorities, err := parseHTTPPrioritySet(q.Get("priority"))
		if err != nil {
			return query, &taskListQueryError{field: "priority", message: err.Error()}
		}
		query.Priorities = priorities
	}
	if err := applyHTTPTaskQueryIntBound(q, "priority_gte", &query.PriorityGte); err != nil {
		return query, &taskListQueryError{field: "priority_gte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryIntBound(q, "priority_lte", &query.PriorityLte); err != nil {
		return query, &taskListQueryError{field: "priority_lte", message: err.Error()}
	}
	if v := q.Get("sprint_id"); v != "" {
		query.SprintID = v
	}
	if v := q.Get("project_id"); v != "" {
		query.ProjectID = v
	}
	if v := q.Get("epic_id"); v != "" {
		query.EpicID = v
	}
	if v := q.Get("executor"); v != "" {
		query.Executor = v
	}
	if v := q.Get("kind"); v != "" {
		query.Kind = v
	}
	if _, ok := q["include_internal"]; ok {
		v, err := parseTaskListBool(q.Get("include_internal"), "include_internal")
		if err != nil {
			return query, &taskListQueryError{field: "include_internal", message: err.Error()}
		}
		query.IncludeInternal = v
	}
	if v := q.Get("source_type"); v != "" {
		query.SourceType = v
	}
	if v := q.Get("source_ref"); v != "" {
		query.SourceRef = v
	}
	if v := q.Get("trust"); v != "" {
		query.Trust = v
	}
	if v := q.Get("checkpoint_mode"); v != "" {
		query.CheckpointMode = v
	}
	// parent_id filter — special value "null" returns roots (parent IS NULL).
	if _, ok := q["parent_id"]; ok {
		query.ParentIDSet = true
		query.ParentID = q.Get("parent_id")
	}
	// manual filter — accepts canonical booleans plus UI aliases. "both" and
	// empty mean no filter, preserving the documented sentinel.
	if _, ok := q["manual"]; ok {
		manual, err := parseTaskListManual(q.Get("manual"))
		if err != nil {
			return query, &taskListQueryError{field: "manual", message: err.Error()}
		}
		query.Manual = manual
	}
	if v := q.Get("tags"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		query.TagSlugs = out
	} else if v := q.Get("tag"); v != "" {
		query.TagSlugs = []string{strings.TrimSpace(v)}
	}
	query.TagSlugsAny = splitHTTPTagCSV(q.Get("tags_any"))
	query.TagSlugsNone = splitHTTPTagCSV(q.Get("tags_none"))
	if fields, err := splitHTTPPresenceFields(q, "missing"); err != nil {
		return query, &taskListQueryError{field: "missing", message: err.Error()}
	} else {
		query.MissingFields = fields
	}
	if fields, err := splitHTTPPresenceFields(q, "present"); err != nil {
		return query, &taskListQueryError{field: "present", message: err.Error()}
	} else {
		query.PresentFields = fields
	}
	if v := q.Get("search"); v != "" {
		query.Search = v
	}
	if v := q.Get("agent_profile"); v != "" {
		query.AgentProfile = v
	}
	if v := q.Get("launch_profile"); v != "" {
		query.LaunchProfile = v
	}
	query.CreatedAfter = q.Get("created_after")
	query.CreatedBefore = q.Get("created_before")
	query.UpdatedAfter = q.Get("updated_after")
	query.UpdatedBefore = q.Get("updated_before")
	if err := applyHTTPTaskQueryFloatBound(q, "cost_budget_gte", &query.CostBudgetGte); err != nil {
		return query, &taskListQueryError{field: "cost_budget_gte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryFloatBound(q, "cost_budget_lte", &query.CostBudgetLte); err != nil {
		return query, &taskListQueryError{field: "cost_budget_lte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryInt64Bound(q, "token_budget_gte", &query.TokenBudgetGte); err != nil {
		return query, &taskListQueryError{field: "token_budget_gte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryInt64Bound(q, "token_budget_lte", &query.TokenBudgetLte); err != nil {
		return query, &taskListQueryError{field: "token_budget_lte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryInt64Bound(q, "max_duration_ms_gte", &query.MaxDurationMsGte); err != nil {
		return query, &taskListQueryError{field: "max_duration_ms_gte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryInt64Bound(q, "max_duration_ms_lte", &query.MaxDurationMsLte); err != nil {
		return query, &taskListQueryError{field: "max_duration_ms_lte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryIntBound(q, "max_retries_gte", &query.MaxRetriesGte); err != nil {
		return query, &taskListQueryError{field: "max_retries_gte", message: err.Error()}
	}
	if err := applyHTTPTaskQueryIntBound(q, "max_retries_lte", &query.MaxRetriesLte); err != nil {
		return query, &taskListQueryError{field: "max_retries_lte", message: err.Error()}
	}
	query.SortBy = q.Get("sort_by")
	query.SortDir = q.Get("sort_dir")
	query.Cursor = q.Get("cursor")
	if _, ok := q["limit"]; ok {
		n, err := parseTaskListInt(q.Get("limit"), "limit")
		if err != nil {
			return query, &taskListQueryError{field: "limit", message: err.Error()}
		}
		query.Limit = n
	}
	if _, ok := q["offset"]; ok {
		n, err := parseTaskListInt(q.Get("offset"), "offset")
		if err != nil {
			return query, &taskListQueryError{field: "offset", message: err.Error()}
		}
		query.Offset = n
	}
	return query, nil
}

type taskListQueryError struct {
	field   string
	message string
}

func (e taskListQueryError) Error() string {
	return e.message
}

func writeFieldError(w http.ResponseWriter, status int, field, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "field": field})
}

func validateTaskListQueryKeys(q url.Values, facet bool) *taskListQueryError {
	supported := map[string]bool{
		"status": true, "priority": true, "priority_gte": true, "priority_lte": true, "sprint_id": true, "project_id": true, "epic_id": true,
		"executor": true, "kind": true, "include_internal": true, "source_type": true,
		"source_ref": true, "trust": true, "checkpoint_mode": true, "parent_id": true,
		"manual": true, "tags": true, "tag": true, "tags_any": true, "tags_none": true, "missing": true, "present": true, "search": true, "limit": true, "offset": true,
		"agent_profile": true, "launch_profile": true,
		"created_after": true, "created_before": true, "updated_after": true, "updated_before": true,
		"cost_budget_gte": true, "cost_budget_lte": true,
		"token_budget_gte": true, "token_budget_lte": true,
		"max_duration_ms_gte": true, "max_duration_ms_lte": true,
		"max_retries_gte": true, "max_retries_lte": true,
		"sort_by": true, "sort_dir": true, "cursor": true,
	}
	if facet {
		supported["dimensions"] = true
		supported["bucket_limit"] = true
	}
	for key, values := range q {
		if !supported[key] {
			return &taskListQueryError{
				field:   key,
				message: "unsupported query parameter " + key + "; supported task-list parameters are status, priority, priority_gte, priority_lte, sprint_id, project_id, epic_id, executor, kind, include_internal, source_type, source_ref, trust, checkpoint_mode, parent_id, manual, tags, tag, tags_any, tags_none, missing, present, search, agent_profile, launch_profile, created_after, created_before, updated_after, updated_before, cost_budget_gte, cost_budget_lte, token_budget_gte, token_budget_lte, max_duration_ms_gte, max_duration_ms_lte, max_retries_gte, max_retries_lte, sort_by, sort_dir, cursor, limit, offset",
			}
		}
		if len(values) > 1 {
			return &taskListQueryError{
				field:   key,
				message: "query parameter " + key + " may only be supplied once; use comma-separated values where supported",
			}
		}
	}
	return nil
}

func splitHTTPFacetCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func splitHTTPTagCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitHTTPPresenceFields(q url.Values, key string) ([]string, error) {
	raw, ok := q[key]
	if !ok {
		return nil, nil
	}
	if len(raw) == 0 || raw[0] == "" {
		return nil, nil
	}
	if strings.TrimSpace(raw[0]) == "" {
		return nil, taskListQueryError{field: key, message: key + " fields cannot be blank"}
	}
	parts := strings.Split(raw[0], ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		field := strings.TrimSpace(p)
		if field == "" {
			return nil, taskListQueryError{field: key, message: key + " fields cannot be blank"}
		}
		out = append(out, field)
	}
	return out, nil
}

func parseHTTPPrioritySet(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		n, err := parseTaskListInt(part, "priority")
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func parseTaskListInt(raw, field string) (int, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, taskListQueryError{field: field, message: field + " must be an integer"}
	}
	n, err := strconv.ParseInt(v, 10, 0)
	if err != nil {
		return 0, taskListQueryError{field: field, message: field + " must be an integer"}
	}
	if int64(int(n)) != n {
		return 0, taskListQueryError{field: field, message: field + " must fit in a Go int"}
	}
	return int(n), nil
}

func applyHTTPTaskQueryFloatBound(q url.Values, field string, target **float64) error {
	if _, ok := q[field]; !ok {
		return nil
	}
	v := strings.TrimSpace(q.Get(field))
	if v == "" {
		return taskListQueryError{field: field, message: field + " must be a finite number"}
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return taskListQueryError{field: field, message: field + " must be a finite number"}
	}
	*target = &n
	return nil
}

func applyHTTPTaskQueryInt64Bound(q url.Values, field string, target **int64) error {
	if _, ok := q[field]; !ok {
		return nil
	}
	v := strings.TrimSpace(q.Get(field))
	if v == "" {
		return taskListQueryError{field: field, message: field + " must be an integer"}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return taskListQueryError{field: field, message: field + " must be an integer"}
	}
	*target = &n
	return nil
}

func applyHTTPTaskQueryIntBound(q url.Values, field string, target **int) error {
	if _, ok := q[field]; !ok {
		return nil
	}
	n, err := parseTaskListInt(q.Get(field), field)
	if err != nil {
		return err
	}
	*target = &n
	return nil
}

func boundedHTTPTaskListLimit(n int) int {
	if n <= 0 {
		return defaultHTTPTaskListLimit
	}
	if n > maxHTTPTaskListLimit {
		return maxHTTPTaskListLimit
	}
	return n
}

func parseTaskListBool(raw, field string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, taskListQueryError{field: field, message: field + " must be true/false, 1/0, or yes/no"}
	}
}

func parseTaskListManual(raw string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "both":
		return nil, nil
	case "manual", "true", "1":
		v := true
		return &v, nil
	case "auto", "false", "0":
		v := false
		return &v, nil
	default:
		return nil, taskListQueryError{field: "manual", message: "manual must be manual/auto/both, true/false, or 1/0"}
	}
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deps, err := s.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(task, tags, deps, agg, subs, s.collectionNameForTask(task)))
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req TaskCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	// Safety override per CW-20260417-0133: force manual=true on every task
	// create until portfolio callers stop shipping manual=false (explicitly or
	// by default). The bug: non-manual tasks are picked up by the scheduler
	// and, lacking a valid agent_profile, fail-and-burn-retries immediately.
	// Temporary. Pairs with CW-20260417-0134 (removal) and CW-20260417-0413
	// (permanent create-time validation). Update path is UNCHANGED so
	// operators can still promote reviewed tasks to manual=false explicitly.
	if !req.Manual {
		log.Printf("POST /tasks: forcing manual=true on %q (caller passed manual=false) — safety override per CW-20260417-0133", req.Title)
		req.Manual = true
	}

	input := service.TaskCreateInput{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Tags:              req.Tags,
		Manual:            req.Manual,
		Executor:          req.Executor,
		LaunchProfile:     req.LaunchProfile,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		Tools:             req.Tools,
		Permissions:       req.Permissions,
		Environment:       req.Environment,
		SystemPrompt:      req.SystemPrompt,
		AgentFile:         req.AgentFile,
		Files:             req.Files,
		CostBudget:        req.CostBudget,
		MaxRetries:        req.MaxRetries,
		MaxDurationMs:     req.MaxDurationMs,
		TokenBudget:       req.TokenBudget,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		EscalationChain:   req.EscalationChain,
		QualityGates:      req.QualityGates,
		Deliverables:      req.Deliverables,
		DeliverablePreset: req.DeliverablePreset,
		DependsOn:         req.DependsOn,
		BlockedReason:     req.BlockedReason,
		Metadata:          req.Metadata,
		SprintID:          req.SprintID,
		ProjectID:         req.ProjectID,
		EpicID:            req.EpicID,

		Kind:                 req.Kind,
		SourceType:           req.SourceType,
		SourceRef:            req.SourceRef,
		Trust:                req.Trust,
		CheckpointMode:       req.CheckpointMode,
		OnCheckpointResponse: req.OnCheckpointResponse,

		ParentID: req.ParentID,
	}

	task, err := s.svc.Task.Create(input)
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deps, err := s.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Auto-extracted subtodos are persisted during Create; fetch them so the
	// client sees them on the very first response instead of needing a
	// follow-up GET.
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("task.created", map[string]interface{}{"task_id": task.ID, "title": task.Title})
	// Freshly created task has no collection_id yet, so pass empty name.
	writeJSON(w, http.StatusCreated, taskJSON(task, tags, deps, nil, subs, ""))
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req TaskUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Status != nil {
		writeError(w, http.StatusBadRequest,
			"status updates are not allowed via PUT /tasks/:id — use POST /tasks/:id/transition")
		return
	}

	update := sqlstore.TaskUpdate{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Manual:            req.Manual,
		Executor:          req.Executor,
		LaunchProfile:     req.LaunchProfile,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		SystemPrompt:      req.SystemPrompt,
		AgentFile:         req.AgentFile,
		MaxRetries:        req.MaxRetries,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		DeliverablePreset: req.DeliverablePreset,
		BlockedReason:     req.BlockedReason,
	}

	// JSON-blob fields wrap as *sql.NullString
	if req.Tools != nil {
		update.Tools = nullJSONString(*req.Tools)
	}
	if req.Permissions != nil {
		update.Permissions = nullJSONString(*req.Permissions)
	}
	if req.Environment != nil {
		update.Environment = nullJSONString(*req.Environment)
	}
	if req.Files != nil {
		update.Files = nullJSONString(*req.Files)
	}
	if req.EscalationChain != nil {
		update.EscalationChain = nullJSONString(*req.EscalationChain)
	}
	if req.QualityGates != nil {
		update.QualityGates = nullJSONString(*req.QualityGates)
	}
	if req.Deliverables != nil {
		update.Deliverables = nullJSONString(*req.Deliverables)
	}
	if req.Metadata != nil {
		update.Metadata = nullJSONString(*req.Metadata)
	}

	// Numeric nullables wrap as *sql.NullFloat64 / *sql.NullInt64
	if req.CostBudget != nil {
		update.CostBudget = &sql.NullFloat64{Float64: *req.CostBudget, Valid: true}
	}
	if req.MaxDurationMs != nil {
		update.MaxDurationMs = &sql.NullInt64{Int64: *req.MaxDurationMs, Valid: true}
	}
	if req.TokenBudget != nil {
		update.TokenBudget = &sql.NullInt64{Int64: *req.TokenBudget, Valid: true}
	}

	// Association fields wrap as *sql.NullString
	if req.SprintID != nil {
		update.SprintID = &sql.NullString{String: *req.SprintID, Valid: *req.SprintID != ""}
	}
	if req.ProjectID != nil {
		update.ProjectID = &sql.NullString{String: *req.ProjectID, Valid: *req.ProjectID != ""}
	}
	if req.EpicID != nil {
		update.EpicID = &sql.NullString{String: *req.EpicID, Valid: *req.EpicID != ""}
	}

	// Facet fields (migration 007). String facets map directly; source_ref
	// wraps as *sql.NullString so the empty string clears the column.
	update.Kind = req.Kind
	update.SourceType = req.SourceType
	update.Trust = req.Trust
	update.CheckpointMode = req.CheckpointMode
	update.OnCheckpointResponse = req.OnCheckpointResponse
	if req.SourceRef != nil {
		update.SourceRef = &sql.NullString{String: *req.SourceRef, Valid: *req.SourceRef != ""}
	}
	// Parent linkage (migration 013). Empty string clears the parent.
	if req.ParentID != nil {
		update.ParentID = &sql.NullString{String: *req.ParentID, Valid: *req.ParentID != ""}
	}

	input := service.TaskUpdateInput{TaskUpdate: update, Tags: req.Tags, DependsOn: req.DependsOn}

	if err := s.svc.Task.Update(id, input); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deps, err := s.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.updated", map[string]interface{}{"task_id": id})
	writeJSON(w, http.StatusOK, taskJSON(task, tags, deps, agg, subs, s.collectionNameForTask(task)))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Task.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transitionTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
		Force  bool   `json:"force"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	transition := s.svc.Task.Transition
	if req.Force {
		transition = s.svc.Task.ForceTransition
	}
	if err := transition(r.Context(), id, req.Status); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deps, err := s.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	writeJSON(w, http.StatusOK, taskJSON(task, tags, deps, agg, subs, s.collectionNameForTask(task)))
}

func (s *Server) bulkTransitionTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	succeeded, _ := s.svc.Task.BulkTransition(r.Context(), req.IDs, req.Status)
	// Broadcast only the IDs that actually transitioned. The prior code
	// took req.IDs[:n], which on partial failure broadcast the WRONG ids
	// (the first n input ids, regardless of which actually succeeded) —
	// Copilot review on PR #104 found this; BulkTransition's signature
	// changed at the same time to return succeeded IDs.
	for _, id := range succeeded {
		s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": len(succeeded)})
}

func (s *Server) searchTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	tasks, err := s.svc.Task.Search(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}
