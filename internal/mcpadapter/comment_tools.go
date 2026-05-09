package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerCommentTools() {
	a.addTool(mcp.NewTool("clockwork_comment_add",
		mcp.WithDescription(`Append a comment (freeform prose) to an entity; returns the persisted CommentRecord with assigned ID.
Comments are polymorphic — entity_type selects which kind of entity the comment is attached to. Currently supported: "task". Future: "collection", "epic", "sprint", "project". Use for agent-to-user channel, review notes, or blocked-reason explanation; structured audit trails should go in artifacts via clockwork_artifact_create. Comments never drive lifecycle.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"entity_type":"task","entity_id":"T-123","author":"reviewer","content":"Please also cover the null-parent case."}`),
		mcp.WithString("entity_type", mcp.Required(), mcp.Description(`Entity kind the comment is attached to. Valid: "task". Future: "collection", "epic", "sprint", "project".`)),
		mcp.WithString("entity_id", mcp.Required(), mcp.Description("ID of the entity (e.g. task ID for entity_type=task)")),
		mcp.WithString("author", mcp.Description("Comment author slug/id")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment body (prose)")),
	), a.handleCommentAdd)

	a.addTool(mcp.NewTool("clockwork_comment_list",
		mcp.WithDescription(`List all comments on an entity, oldest first (chronological order). Default brief shape includes a 100-char excerpt of the body; pass verbose="true" for full content.
Use to review the discussion thread for a given entity; clockwork_comment_add to append. For newest-first cross-entity search use clockwork_comment_search. No comment_get/delete yet — brief ID + list is the read surface.
Response shape: data = {items: [<briefComment or CommentRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"entity_type":"task","entity_id":"T-123"}`),
		mcp.WithString("entity_type", mcp.Required(), mcp.Description(`Entity kind. Valid: "task". Future: "collection", "epic", "sprint", "project".`)),
		mcp.WithString("entity_id", mcp.Required(), mcp.Description("ID of the entity")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleCommentList)

	a.addTool(mcp.NewTool("clockwork_comment_search",
		mcp.WithDescription(`Search comments by content across all entities, optionally scoped to one entity (entity_type + entity_id) or author. Returns newest first.
Use to discover comments by content; clockwork_comment_list for per-entity ordered history. No comment_get/delete yet.
Response shape: data = {items: [<CommentRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"query":"review notes","entity_type":"task","entity_id":"T-123"}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Substring match on comment content")),
		mcp.WithString("entity_type", mcp.Description(`Restrict to one entity kind (optional). Valid: "task" (more later).`)),
		mcp.WithString("entity_id", mcp.Description("Restrict to one entity (optional; usually paired with entity_type)")),
		mcp.WithString("author", mcp.Description("Exact author match (optional)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 25, max 100)")),
	), a.handleCommentSearch)
}

func (a *Adapter) handleCommentAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entityType := reqStr(req, "entity_type")
	entityID := reqStr(req, "entity_id")
	if entityType == "" {
		return errResult(ErrCodeArgInvalid, "entity_type is required", "entity_type")
	}
	if entityID == "" {
		return errResult(ErrCodeArgInvalid, "entity_id is required", "entity_id")
	}
	comment, err := a.svc.Comment.Add(
		entityType,
		entityID,
		reqStr(req, "author"),
		reqStr(req, "content"),
	)
	if err != nil {
		return errFromService(err)
	}
	// comment is typed as *sqlstore.CommentRecord — return the bare record so
	// callers can see the assigned ID / timestamp without a second round-trip.
	return okResult(comment)
}

func (a *Adapter) handleCommentList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entityType := reqStr(req, "entity_type")
	entityID := reqStr(req, "entity_id")
	if entityType == "" {
		return errResult(ErrCodeArgInvalid, "entity_type is required", "entity_type")
	}
	if entityID == "" {
		return errResult(ErrCodeArgInvalid, "entity_id is required", "entity_id")
	}
	verbose := reqStrBool(req, "verbose")
	comments, err := a.svc.Comment.List(entityType, entityID)
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(comments))
	for _, c := range comments {
		if verbose {
			items = append(items, c)
		} else {
			items = append(items, toBriefComment(c))
		}
	}
	return cappedJSONResult(items, limit)
}

const (
	defaultCommentSearchLimit = 25
	maxCommentSearchLimit     = 100
)

func (a *Adapter) handleCommentSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := reqStr(req, "query")
	if query == "" {
		return errResult(ErrCodeArgInvalid, "query is required", "query")
	}
	limit := clampLimit(reqInt(req, "limit"), defaultCommentSearchLimit, maxCommentSearchLimit)

	filter := sqlstore.CommentFilter{
		Search:     query,
		EntityType: reqStr(req, "entity_type"),
		EntityID:   reqStr(req, "entity_id"),
		Author:     reqStr(req, "author"),
		Limit:      limit,
	}

	comments, err := a.svc.Comment.Search(filter)
	if err != nil {
		return errFromService(err)
	}

	items := make([]any, 0, len(comments))
	for _, c := range comments {
		items = append(items, c)
	}
	return cappedJSONResult(items, limit)
}
