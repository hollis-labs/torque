package mcpadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

const (
	defaultTagListLimit = service.DefaultTagListLimit
	maxTagListLimit     = service.MaxTagListLimit
)

type tagRecord struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (a *Adapter) registerTagTools() {
	a.addTool(newTool("torque_tag_list",
		withDescription(`List the global tag catalog, including tags currently linked to no tasks. This is catalog discovery only; scoped usage/counts live on torque_task_facets.
Response shape: data = {items:[{slug,name,description,color,created_at,updated_at}], meta:{truncated,returned,limit,total,has_more,next_cursor,hint?}}. Default limit 50, max 200. Order is name COLLATE NOCASE ASC with slug ASC as the stable tiebreak, matching HTTP GET /tags default name ordering. query is a literal substring over slug/name/description; SQL wildcards are escaped and have no special meaning. color is an exact catalog color filter.
Continuation: pass meta.next_cursor back as cursor with the same query/color; the cursor is derived from the last tag actually emitted after the MCP byte cap.
Errors: malformed limit/cursor or wrong argument types return error.code=arg_invalid with field set.
Example: {"query":"api","limit":"50"}`),
		withString("query", desc("Literal substring over slug/name/description; %, _, and backslash are escaped")),
		withString("color", desc("Exact color filter: zinc|red|orange|amber|green|teal|blue|violet|pink")),
		withString("limit", desc("Max results (integer, default 50, max 200)")),
		withString("cursor", desc("Opaque cursor from meta.next_cursor; omit for the first page")),
	), a.handleTagList)

	a.addTool(newTool("torque_tag_get",
		withDescription(`Fetch one global catalog tag by slug. Slug lookup treats existing stored slugs as opaque identity values; this read path does not revalidate or rewrite historical slugs.
Response shape: data = {slug,name,description,color,created_at,updated_at}.
Errors: a missing slug returns error.code=not_found; wrong argument types return arg_invalid.
Use this when you already know the catalog slug and need its mutable display metadata.
Example: {"slug":"api"}`),
		withString("slug", required(), desc("Tag slug identity to fetch")),
	), a.handleTagGet)

	a.addTool(newTool("torque_tag_create",
		withDescription(`Create a global catalog tag through TagService validation/defaults. If slug is omitted, it is derived from name only at create time. Explicit slug must satisfy the current create rules; duplicate slug returns error.code=conflict. Name max 64 chars, description max 500, color defaults to zinc.
Response shape: data = {slug,name,description,color,created_at,updated_at}.
Slug is identity after creation; later updates can change name/description/color but not slug.
Errors: validation failures return arg_invalid with field set; duplicate slug returns conflict.
Example: {"name":"API","slug":"api","color":"blue"}`),
		withString("name", required(), desc("Display name; slug derives from this only when slug is omitted")),
		withString("slug", desc("Optional explicit slug identity")),
		withString("description", desc("Optional description, max 500 chars")),
		withString("color", desc("Optional palette color; defaults to zinc")),
	), a.handleTagCreate)

	a.addTool(newTool("torque_tag_update",
		withDescription(`Partially update a global catalog tag's mutable metadata. Slug is identity and cannot be changed. Omitted fields are untouched; explicit empty description clears it; explicit empty color resets to the service default zinc.
Response shape: data = {slug,name,description,color,created_at,updated_at}.
Presence matters: omit a key to leave it unchanged; pass an empty string only when you mean to clear/default it.
Errors: missing slug returns not_found; validation failures return arg_invalid with field set.
Example: {"slug":"api","description":"","color":"zinc"}`),
		withString("slug", required(), desc("Tag slug identity to update")),
		withString("name", desc("New display name; omit to leave unchanged")),
		withString("description", desc("New description; pass empty string to clear")),
		withString("color", desc("New palette color; pass empty string to reset to zinc")),
	), a.handleTagUpdate)

	a.addTool(newTool("torque_tag_delete",
		withDescription(`Delete a global catalog tag. Destructive: removes the tag catalog row and cascades all task_tags links for this slug. There is no undo.
Response shape: data = {deleted:true, slug}.
Global effect: every task loses this tag link immediately through the existing store cascade.
Prefer merge when the intent is consolidation rather than removal.
Example: {"slug":"obsolete"}`),
		withString("slug", required(), desc("Tag slug identity to delete")),
	), a.handleTagDelete)

	a.addTool(newTool("torque_tag_merge",
		withDescription(`Merge one global tag into another. Destructive: source and into must be distinct existing slugs. Destination metadata is preserved; task links are rewritten/deduped to the destination in one write transaction; the source catalog row is deleted. No alias record is retained.
Response shape: data = {source_slug, into_slug, tag:{slug,name,description,color,created_at,updated_at}} where tag is the preserved destination record.
Global effect: all tasks formerly linked to source_slug point at into_slug after the call; tasks already linked to both keep one destination link.
Errors: same source/destination returns arg_invalid on into_slug; missing source or destination returns not_found.
Example: {"source_slug":"bug","into_slug":"defect"}`),
		withString("source_slug", required(), desc("Source tag slug to delete after links move")),
		withString("into_slug", required(), desc("Existing destination tag slug to preserve")),
	), a.handleTagMerge)
}

func toTagRecord(t sqlstore.TagRecord) tagRecord {
	return tagRecord{
		Slug:        t.Slug,
		Name:        t.Name,
		Description: t.Description,
		Color:       t.Color,
		CreatedAt:   t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func tagArg(req map[string]any, key string, required bool) (string, error) {
	v, ok := req[key]
	if !ok {
		if required {
			return "", argError(ErrCodeArgInvalid, key+" is required", key)
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", argError(ErrCodeArgInvalid, fmt.Sprintf("%s must be a string (got %T)", key, v), key)
	}
	if required && strings.TrimSpace(s) == "" {
		return "", argError(ErrCodeArgInvalid, key+" is required", key)
	}
	return s, nil
}

func tagLimitArg(req map[string]any) (int, error) {
	limit, present, err := reqTaskListInt(req, "limit")
	if err != nil {
		return 0, argError(ErrCodeArgInvalid, err.Error(), "limit")
	}
	if !present {
		return defaultTagListLimit, nil
	}
	if limit <= 0 {
		return 0, argError(ErrCodeArgInvalid, "limit must be greater than 0", "limit")
	}
	if limit > maxTagListLimit {
		limit = maxTagListLimit
	}
	return limit, nil
}

func (a *Adapter) handleTagList(ctx context.Context, req map[string]any) (any, error) {
	limit, errRes := tagLimitArg(req)
	if errRes != nil {
		return nil, errRes
	}
	query, errRes := tagArg(req, "query", false)
	if errRes != nil {
		return nil, errRes
	}
	color, errRes := tagArg(req, "color", false)
	if errRes != nil {
		return nil, errRes
	}
	cursor, errRes := tagArg(req, "cursor", false)
	if errRes != nil {
		return nil, errRes
	}
	afterName, afterSlug, err := service.DecodeTagListCursor(cursor)
	if err != nil {
		return errFromService(err)
	}
	result, err := a.svc.Tag.ListPage(service.TagListInput{
		Query:     query,
		Color:     color,
		Limit:     limit,
		AfterName: afterName,
		AfterSlug: afterSlug,
	})
	if err != nil {
		return errFromService(err)
	}
	items := make([]any, 0, len(result.Tags))
	for _, t := range result.Tags {
		items = append(items, toTagRecord(t))
	}
	return cappedCursorJSONResultWithTotal(items, result.Limit, &result.Total, service.TagListSortBy, service.TagListSortDir, result.HasMoreFromQuery, func(i int) (string, string) {
		return result.Tags[i].Name, result.Tags[i].Slug
	})
}

func (a *Adapter) handleTagGet(ctx context.Context, req map[string]any) (any, error) {
	slug, errRes := tagArg(req, "slug", true)
	if errRes != nil {
		return nil, errRes
	}
	tag, err := a.svc.Tag.Get(slug)
	if err != nil {
		return errFromService(err)
	}
	return okResult(toTagRecord(*tag))
}

func (a *Adapter) handleTagCreate(ctx context.Context, req map[string]any) (any, error) {
	name, errRes := tagArg(req, "name", true)
	if errRes != nil {
		return nil, errRes
	}
	slug, errRes := tagArg(req, "slug", false)
	if errRes != nil {
		return nil, errRes
	}
	description, errRes := tagArg(req, "description", false)
	if errRes != nil {
		return nil, errRes
	}
	color, errRes := tagArg(req, "color", false)
	if errRes != nil {
		return nil, errRes
	}
	tag, err := a.svc.Tag.Create(service.TagCreateInput{Name: name, Slug: slug, Description: description, Color: color})
	if err != nil {
		if service.IsTagUniqueConstraintError(err) {
			return errResult(ErrCodeConflict, "tag slug already exists", "slug")
		}
		return errFromService(err)
	}
	return okResult(toTagRecord(*tag))
}

func (a *Adapter) handleTagUpdate(ctx context.Context, req map[string]any) (any, error) {
	slug, errRes := tagArg(req, "slug", true)
	if errRes != nil {
		return nil, errRes
	}
	var input service.TagUpdateInput
	if reqHasArg(req, "name") {
		v, res := tagArg(req, "name", false)
		if res != nil {
			return nil, res
		}
		input.Name = &v
	}
	if reqHasArg(req, "description") {
		v, res := tagArg(req, "description", false)
		if res != nil {
			return nil, res
		}
		input.Description = &v
	}
	if reqHasArg(req, "color") {
		v, res := tagArg(req, "color", false)
		if res != nil {
			return nil, res
		}
		input.Color = &v
	}
	tag, err := a.svc.Tag.Update(slug, input)
	if err != nil {
		return errFromService(err)
	}
	return okResult(toTagRecord(*tag))
}

func (a *Adapter) handleTagDelete(ctx context.Context, req map[string]any) (any, error) {
	slug, errRes := tagArg(req, "slug", true)
	if errRes != nil {
		return nil, errRes
	}
	if err := a.svc.Tag.Delete(slug); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"deleted": true, "slug": slug})
}

func (a *Adapter) handleTagMerge(ctx context.Context, req map[string]any) (any, error) {
	source, errRes := tagArg(req, "source_slug", true)
	if errRes != nil {
		return nil, errRes
	}
	into, errRes := tagArg(req, "into_slug", true)
	if errRes != nil {
		return nil, errRes
	}
	if err := a.svc.Tag.Merge(source, into); err != nil {
		var verr *service.ValidationError
		if errors.As(err, &verr) && verr.Field == "into" {
			return errResult(ErrCodeArgInvalid, err.Error(), "into_slug")
		}
		return errFromService(err)
	}
	dest, err := a.svc.Tag.Get(into)
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"source_slug": source,
		"into_slug":   into,
		"tag":         toTagRecord(*dest),
	})
}
