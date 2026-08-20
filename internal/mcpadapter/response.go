package mcpadapter

import (
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// maxMCPResponseBytes is the size guard for list/search responses. mark3labs
// enforces a hard 128KB cap on tool stdio responses; we leave headroom for
// the JSON-RPC envelope, base64 framing, and future Phase C `{ok, data, error}`
// wrapping. Every list/search handler that uses the `{items, meta}` envelope
// feeds through cappedJSONResult so a too-large fan-out degrades to a
// truncated response with a hint instead of a hard transport error. See
// CW-20260418-0012.
const maxMCPResponseBytes = 100 * 1024 // 100KB

// Default and maximum limits for list/search tools. Kept here instead of
// scattered through per-tool handlers so the shape contract is discoverable
// in one place.
const (
	defaultTaskSearchLimit   = 25
	maxTaskSearchLimit       = 100
	maxTaskListLimit         = 200
	defaultGenericListLimit  = 100
	maxGenericListLimit      = 500
	defaultTemplateListLimit = 100
	maxTemplateListLimit     = 500
)

// listMeta is the companion to items[] in the list/search response envelope.
// Fields are intentionally lower-case + snake_case to match the eventual
// {ok, data, error} Phase C envelope style — every per-tool response is
// `{items, meta}` at the top level; Phase C will set `data = {items, meta}`
// with no further nesting.
type listMeta struct {
	Truncated bool   `json:"truncated"`
	Returned  int    `json:"returned"`
	Limit     int    `json:"limit"`
	Hint      string `json:"hint,omitempty"`
}

// ---- brief shapes -----------------------------------------------------------

// All brief shapes target ~150 bytes per record. They intentionally drop large
// free-text columns (description, system_prompt, template body, stack traces,
// artifact content, comment content) and JSON-blob columns. Tags, when
// included, are a flat []string of slugs — never the full TagRecord.

type briefTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     int      `json:"priority"`
	Manual       bool     `json:"manual"`
	AgentProfile string   `json:"agent_profile,omitempty"`
	Kind         string   `json:"kind"`
	Tags         []string `json:"tags,omitempty"`
	UpdatedAt    string   `json:"updated_at"`
}

type briefSprint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ApprovalMode string `json:"approval_mode"`
	ProjectID    string `json:"project_id,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

type briefEpic struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Priority  int64  `json:"priority,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type briefProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	RepoPath  string `json:"repo_path,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type briefRun struct {
	ID        int64   `json:"id"`
	TaskID    string  `json:"task_id"`
	Executor  string  `json:"executor,omitempty"`
	Status    string  `json:"status"`
	ExitCode  *int64  `json:"exit_code,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	StartedAt string  `json:"started_at"`
}

type briefTemplate struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	AutoExecute bool   `json:"auto_execute"`
	IsArchived  bool   `json:"is_archived,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type briefComment struct {
	ID         int64  `json:"id"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Author     string `json:"author,omitempty"`
	CreatedAt  string `json:"created_at"`
	// First 100 chars of the comment body for context without bloat.
	Excerpt string `json:"excerpt,omitempty"`
}

type briefArtifact struct {
	ID        int64  `json:"id"`
	TaskID    string `json:"task_id"`
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	CreatedAt string `json:"created_at"`
}

type briefSubtodo struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
	Done     bool   `json:"done"`
}

type briefCheckpoint struct {
	ID            int64  `json:"id"`
	TaskID        string `json:"task_id"`
	CorrelationID string `json:"correlation_id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	EmittedAt     string `json:"emitted_at"`
}

// ---- to-brief converters ----------------------------------------------------

func toBriefTask(t sqlstore.TaskRecord, tags []string) briefTask {
	return briefTask{
		ID:           t.ID,
		Title:        t.Title,
		Status:       t.Status,
		Priority:     t.Priority,
		Manual:       t.Manual,
		AgentProfile: t.AgentProfile,
		Kind:         t.Kind,
		Tags:         tags,
		UpdatedAt:    t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefSprint(s sqlstore.SprintRecord) briefSprint {
	projectID := ""
	if s.ProjectID.Valid {
		projectID = s.ProjectID.String
	}
	return briefSprint{
		ID:           s.ID,
		Name:         s.Name,
		Status:       s.Status,
		ApprovalMode: s.ApprovalMode,
		ProjectID:    projectID,
		UpdatedAt:    s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefEpic(e sqlstore.EpicRecord) briefEpic {
	projectID := ""
	if e.ProjectID.Valid {
		projectID = e.ProjectID.String
	}
	var prio int64
	if e.Priority.Valid {
		prio = e.Priority.Int64
	}
	return briefEpic{
		ID:        e.ID,
		Name:      e.Name,
		Status:    e.Status,
		Priority:  prio,
		ProjectID: projectID,
		UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefProject(p sqlstore.ProjectRecord) briefProject {
	return briefProject{
		ID:        p.ID,
		Name:      p.Name,
		Status:    p.Status,
		RepoPath:  p.RepoPath,
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefRun(r sqlstore.RunRecord) briefRun {
	var exit *int64
	if r.ExitCode.Valid {
		v := r.ExitCode.Int64
		exit = &v
	}
	return briefRun{
		ID:        r.ID,
		TaskID:    r.TaskID,
		Executor:  r.Executor,
		Status:    r.Status,
		ExitCode:  exit,
		Cost:      r.Cost,
		StartedAt: r.StartedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefTemplate(t sqlstore.TemplateRecord) briefTemplate {
	return briefTemplate{
		ID:          t.ID,
		Version:     t.Version,
		Name:        t.Name,
		Kind:        t.Kind,
		AutoExecute: t.AutoExecute,
		IsArchived:  t.IsArchived,
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefComment(c sqlstore.CommentRecord) briefComment {
	excerpt := c.Content
	if len(excerpt) > 100 {
		excerpt = excerpt[:100]
	}
	return briefComment{
		ID:         c.ID,
		EntityType: c.EntityType,
		EntityID:   c.EntityID,
		Author:     c.Author,
		CreatedAt:  c.CreatedAt.UTC().Format(time.RFC3339),
		Excerpt:    excerpt,
	}
}

func toBriefArtifact(a sqlstore.ArtifactRecord) briefArtifact {
	return briefArtifact{
		ID:        a.ID,
		TaskID:    a.TaskID,
		Type:      a.Type,
		URL:       a.URL,
		FilePath:  a.FilePath,
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toBriefSubtodo(s sqlstore.Subtodo) briefSubtodo {
	return briefSubtodo{
		ID:       s.ID,
		Text:     s.Text,
		Required: s.Required,
		Done:     s.Done,
	}
}

func toBriefCheckpoint(c sqlstore.CheckpointRecord) briefCheckpoint {
	return briefCheckpoint{
		ID:            c.ID,
		TaskID:        c.TaskID,
		CorrelationID: c.CorrelationID,
		Type:          c.Type,
		Status:        c.Status,
		EmittedAt:     c.EmittedAt.UTC().Format(time.RFC3339),
	}
}

// ---- size-guarded envelope --------------------------------------------------

// reqStrBool reads an MCP string arg and coerces "true"/"false"/"1"/"0" to
// bool. Matches the Phase A convention of declaring numeric/bool-like params
// as strings so LLM clients that emit "verbose": "false" don't trip schema
// validation. Missing or unparseable values return false.
func reqStrBool(req mcp.CallToolRequest, key string) bool {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case bool:
		return n
	case string:
		switch n {
		case "true", "TRUE", "True", "1", "yes":
			return true
		}
		return false
	}
	return false
}

// clampLimit returns a sane limit for list/search tools. limit<=0 falls back
// to def; anything above max is clamped down. The caller is responsible for
// reading the raw int from the request.
func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

// listEnvelope is the inner {items, meta} payload that rides inside the
// Phase C Response.Data field. Pulled into a named type so the size-fit
// search below can marshal the full `{ok, data, error}` envelope (matching
// wire output) instead of the pre-Phase-C bare `{items, meta}`.
type listEnvelope struct {
	Items []any    `json:"items"`
	Meta  listMeta `json:"meta"`
}

// cappedJSONResult serializes items into the `{ok: true, data: {items, meta}}`
// response envelope, enforcing maxMCPResponseBytes. items MUST be a slice
// (reflected via len() on a known concrete slice type at the call site).
// limit is the applied limit (reported back in meta.limit). If the marshaled
// payload exceeds maxMCPResponseBytes, cappedJSONResult drops tail entries
// one at a time until the envelope fits, sets meta.truncated=true, and adds a
// hint. Callers pass the already-sliced/limited slice; the function does NOT
// re-apply limit.
//
// Per the Phase C ticket's "critical shape coordination" section, the wire
// shape is strictly FLAT: top-level `{ok, data, error}` with `data` holding
// the `{items, meta}` literal — no further nesting. The size budget accounts
// for the JSON overhead of the outer envelope so a list that nominally fits
// under the cap doesn't blow it once wrapped.
func cappedJSONResult(items []any, limit int) (*mcp.CallToolResult, error) {
	// Marshal via the envelope helper so the size we're budgeting against is
	// the actual wire payload the caller will see.
	marshalEnv := func(payload listEnvelope) ([]byte, error) {
		return json.MarshalIndent(Response{OK: true, Data: payload}, "", "  ")
	}

	meta := listMeta{
		Truncated: false,
		Returned:  len(items),
		Limit:     limit,
	}
	payload := listEnvelope{Items: items, Meta: meta}

	b, err := marshalEnv(payload)
	if err != nil {
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}

	// Fast path: fits in cap.
	if len(b) <= maxMCPResponseBytes {
		return mcp.NewToolResultText(string(b)), nil
	}

	// Slow path: shrink until it fits. We drop from the tail; callers pass
	// items already sorted by recency where that matters (ListTasks returns
	// updated_at DESC), so trimming the tail keeps the newest records.
	// Binary-search-like shrink: halve until it fits, then linearly grow to
	// the largest prefix that fits. For typical 2-3x overshoots this costs
	// a handful of marshals, not O(n).
	trimmed := items
	for len(trimmed) > 0 && len(b) > maxMCPResponseBytes {
		trimmed = trimmed[:len(trimmed)/2]
		meta.Truncated = true
		meta.Returned = len(trimmed)
		meta.Hint = "response too large; add filters or lower limit"
		payload = listEnvelope{Items: trimmed, Meta: meta}
		b, err = marshalEnv(payload)
		if err != nil {
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
	}

	// Grow back toward the largest prefix that still fits, using the full
	// `items` slice as the upper bound. This avoids losing records to the
	// halving heuristic when we only overshot by a small margin.
	lo, hi := len(trimmed), len(items)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		trial := items[:mid]
		payloadTry := listEnvelope{Items: trial, Meta: listMeta{
			Truncated: mid < len(items),
			Returned:  mid,
			Limit:     limit,
			Hint: func() string {
				if mid < len(items) {
					return "response too large; add filters or lower limit"
				}
				return ""
			}(),
		}}
		b2, err2 := marshalEnv(payloadTry)
		if err2 != nil {
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
		if len(b2) <= maxMCPResponseBytes {
			lo = mid
			trimmed = trial
		} else {
			hi = mid - 1
		}
	}

	// Finalize meta on the resolved prefix.
	meta.Truncated = len(trimmed) < len(items)
	meta.Returned = len(trimmed)
	meta.Hint = ""
	if meta.Truncated {
		meta.Hint = "response too large; add filters or lower limit"
	}
	payload = listEnvelope{Items: trimmed, Meta: meta}
	b, err = marshalEnv(payload)
	if err != nil {
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}
	return mcp.NewToolResultText(string(b)), nil
}

// ---- cursor-paginated envelope (PRIM-001 / PRIM-002) -----------------------

// listMetaCursor is listMeta's cursor-pagination companion, used by list
// tools that have adopted DEC-001's cursor pagination — Task's
// torque_task_list is the PRIM-001/PRIM-002 reference implementation; Phase
// 4 rolls this same shape out to the other 6 in-scope entities (Comment,
// Project, Epic, Sprint, Issue, Plan). Matches ADR-0004 §3's envelope text
// literally: `{items, meta: {returned, limit, total_count|has_more,
// next_cursor, hint?}}`.
//
// HasMore (not TotalCount): DEC-001 left the has_more-vs-total_count choice
// open and recommended has_more as the cheaper default (fetch limit+1, trim)
// absent a concrete need for exact counts. No such need surfaced for Task,
// so this reference implementation — and by extension every entity that
// copies it in Phase 4 — uses has_more. A future entity that genuinely needs
// an exact total_count can add that field to its own meta struct without
// touching this one.
//
// Truncated is kept distinct from HasMore/NextCursor: Truncated flags the
// orthogonal 100KB byte-size cap (cappedJSONResult's pre-existing behavior)
// tripping and dropping rows below what the query actually returned;
// HasMore/NextCursor flag whether the QUERY itself has more rows beyond this
// page. The two can differ — see cappedCursorJSONResult's doc comment.
type listMetaCursor struct {
	Truncated  bool    `json:"truncated"`
	Returned   int     `json:"returned"`
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
	Hint       string  `json:"hint,omitempty"`
}

// listEnvelopeCursor is listEnvelope's cursor-pagination companion — see
// listMetaCursor.
type listEnvelopeCursor struct {
	Items []any          `json:"items"`
	Meta  listMetaCursor `json:"meta"`
}

// cappedCursorJSONResult is cappedJSONResult's cursor-aware sibling for list
// tools that support DEC-001 cursor pagination (PRIM-001/PRIM-002). It is
// intentionally a separate function rather than a change to
// cappedJSONResult's signature: cappedJSONResult has 19 existing call sites
// across every other entity's list/search tool, none of which have adopted
// cursor pagination yet (that's each entity's own Phase 4 task per
// PRIM-001's "Out of scope") — changing its signature would force an
// unrelated, unwanted migration on all of them today.
//
// Parameters:
//   - items: the already-verbose/brief-converted records for AT MOST the
//     requested limit (callers that over-fetch limit+1 to detect has_more
//     must trim the extra row off items before calling — see hasMoreFromQuery
//     below for how that extra row's existence is still communicated).
//   - limit: the applied limit (reported back in meta.limit).
//   - sortBy, sortDir: the request's validated sort_by/sort_dir, stamped
//     into next_cursor so a subsequent call can be validated against them
//     (pagination.Cursor.Validate — DEC-001 cursors aren't portable across
//     different sort orders).
//   - hasMoreFromQuery: true when the store returned more rows than limit
//     (i.e. the caller fetched limit+1, found len > limit, and trimmed the
//     extra row before passing items here). This is ORTHOGONAL to the
//     byte-size truncation below — a query can have more rows AND still fit
//     under the byte cap, or have no more rows but still get byte-trimmed if
//     the page itself is huge.
//   - cursorAt: given the index of the LAST item actually included in the
//     final (possibly byte-cap-trimmed) response, returns that row's
//     string-encoded sort value and id so cappedCursorJSONResult can build
//     next_cursor. This is called AFTER the byte-size trim below is
//     resolved, never before — DEC-001 requires next_cursor to reflect what
//     actually shipped, not what the store returned, so a byte-cap trim that
//     drops rows below hasMoreFromQuery's original assumption still produces
//     a correct, resumable cursor (and correctly flips has_more to true even
//     if hasMoreFromQuery was false, since the byte cap itself created more
//     unseen rows).
func cappedCursorJSONResult(items []any, limit int, sortBy, sortDir string, hasMoreFromQuery bool, cursorAt func(lastIncludedIndex int) (sortValue, id string)) (*mcp.CallToolResult, error) {
	build := func(n int) ([]byte, error) {
		trimmed := items[:n]
		hasMore := hasMoreFromQuery || n < len(items)
		var nextCursor *string
		if hasMore && n > 0 {
			sv, id := cursorAt(n - 1)
			s := pagination.Encode(sortBy, sortDir, sv, id)
			nextCursor = &s
		}
		meta := listMetaCursor{
			Truncated:  n < len(items),
			Returned:   n,
			Limit:      limit,
			HasMore:    hasMore,
			NextCursor: nextCursor,
		}
		if meta.Truncated {
			meta.Hint = "response too large; add filters or lower limit"
		}
		return json.MarshalIndent(Response{OK: true, Data: listEnvelopeCursor{Items: trimmed, Meta: meta}}, "", "  ")
	}

	n := len(items)
	b, err := build(n)
	if err != nil {
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}

	// Fast path: fits in cap.
	if len(b) <= maxMCPResponseBytes {
		return mcp.NewToolResultText(string(b)), nil
	}

	// Slow path: same halve-then-grow shrink as cappedJSONResult.
	trimN := n / 2
	for trimN > 0 {
		b, err = build(trimN)
		if err != nil {
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
		if len(b) <= maxMCPResponseBytes {
			break
		}
		trimN /= 2
	}

	lo, hi := trimN, n
	for lo < hi {
		mid := (lo + hi + 1) / 2
		b2, err2 := build(mid)
		if err2 != nil {
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
		if len(b2) <= maxMCPResponseBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	b, err = build(lo)
	if err != nil {
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}
	return mcp.NewToolResultText(string(b)), nil
}

// ---- tag helper -------------------------------------------------------------

// briefTagSlugs pulls the slug-only projection for a task. The brief shape
// MUST emit []string of slugs, never TagRecord. Used by task-list handlers.
func briefTagSlugs(svc *service.Service, taskID string) []string {
	tags, err := svc.Task.ListTags(taskID)
	if err != nil || len(tags) == 0 {
		return nil
	}
	slugs := make([]string, 0, len(tags))
	for _, tag := range tags {
		slugs = append(slugs, tag.Slug)
	}
	return slugs
}
