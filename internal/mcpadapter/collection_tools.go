package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerCollectionTools() {
	a.addTool(newTool("torque_collection_create",
		withDescription(`Create a collection (feature-flagged: requires features.collections).
Use to group tasks under a kanban-style container; sibling torque_sprint_create scopes a time-bounded approval cohort, torque_collection_create is an open-ended container without lifecycle.
Response shape: data = {<CollectionRecord fields>} — singleton.
Example: {"name":"Roadmap","description":"Quarterly themes"}`),
		withString("name", required(), desc("Collection name")),
		withString("description", desc("Collection description")),
	), a.handleCollectionCreate)

	a.addTool(newTool("torque_collection_list",
		withDescription(`List collections, optionally filtered by archive status; ordered created_at DESC.
Default status="active". Use status="archived" for the trash bin, "all" for both.
Response shape: data = {items: [<CollectionRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"status":"active"}`),
		withString("status", desc("Filter: active|archived|all (default active)")),
	), a.handleCollectionList)

	a.addTool(newTool("torque_collection_get",
		withDescription(`Fetch a collection by ID.
Use when you know the ID; torque_collection_list for browsing, torque_collection_tasks_list for the collection's tasks.
Response shape: data = {<CollectionRecord fields>} — singleton.
Example: {"id":"COL-20260503-0001"}`),
		withString("id", required(), desc("Collection ID")),
	), a.handleCollectionGet)

	a.addTool(newTool("torque_collection_update",
		withDescription(`Partial update of collection fields (name, description). Use torque_collection_archive for soft-delete.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"COL-20260503-0001","name":"Roadmap (renamed)"}`),
		withString("id", required(), desc("Collection ID")),
		withString("name", desc("New name")),
		withString("description", desc("New description")),
	), a.handleCollectionUpdate)

	a.addTool(newTool("torque_collection_archive",
		withDescription(`Archive a collection (soft-delete; preserves audit trail).
Response shape: data = {id, archived: true, message}.
Example: {"id":"COL-20260503-0001"}`),
		withString("id", required(), desc("Collection ID")),
	), a.handleCollectionArchive)

	a.addTool(newTool("torque_collection_unarchive",
		withDescription(`Restore an archived collection back to active.
Response shape: data = {id, unarchived: true, message}.
Example: {"id":"COL-20260503-0001"}`),
		withString("id", required(), desc("Collection ID")),
	), a.handleCollectionUnarchive)

	a.addTool(newTool("torque_collection_task_add",
		withDescription(`Add a task to a collection at the given position. position omitted/<=0 means append.
Sets task.added_to_collections_at on first add (write-once). Sibling torque_collection_inbox_add for inbox-only entry.
Response shape: data = {collection_id, task_id, added: true}.
Example: {"collection_id":"COL-20260503-0001","task_id":"T-1"}`),
		withString("collection_id", required(), desc("Target collection ID")),
		withString("task_id", required(), desc("Task ID")),
		withString("position", desc("1-based position; omit or <=0 to append")),
	), a.handleCollectionTaskAdd)

	a.addTool(newTool("torque_collection_task_remove",
		withDescription(`Remove a task from its collection, returning it to inbox. Preserves added_to_collections_at.
Response shape: data = {task_id, removed: true}.
Example: {"task_id":"T-1"}`),
		withString("task_id", required(), desc("Task ID")),
	), a.handleCollectionTaskRemove)

	a.addTool(newTool("torque_collection_task_reorder",
		withDescription(`Bulk-rewrite collection_position for tasks within a single collection. All task_ids must currently belong to collection_id; positions assigned 1..N in supplied order.
Response shape: data = {collection_id, reordered: <count>}.
Example: {"collection_id":"COL-20260503-0001","task_ids":["T-3","T-1","T-2"]}`),
		withString("collection_id", required(), desc("Collection ID")),
		withString("task_ids", required(), desc("Ordered list of task IDs as JSON array string (must all belong to collection_id)")),
	), a.handleCollectionTaskReorder)

	a.addTool(newTool("torque_collection_task_move",
		withDescription(`Atomic cross-collection move. Equivalent to remove+add but no intermediate inbox state.
Response shape: data = {task_id, target_collection_id, moved: true}.
Example: {"task_id":"T-1","target_collection_id":"COL-20260503-0002"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("target_collection_id", required(), desc("Destination collection ID")),
		withString("position", desc("1-based position in target; omit or <=0 to append")),
	), a.handleCollectionTaskMove)

	a.addTool(newTool("torque_collection_inbox_add",
		withDescription(`Mark a task as participating in the collections world without assigning it to a collection. Idempotent (write-once on first call).
Response shape: data = {task_id, in_inbox: true}.
Example: {"task_id":"T-1"}`),
		withString("task_id", required(), desc("Task ID")),
	), a.handleCollectionInboxAdd)

	a.addTool(newTool("torque_collection_inbox_list",
		withDescription(`List inbox tasks (added_to_collections_at NOT NULL AND collection_id IS NULL). Ordered by added_to_collections_at DESC.
Response shape: data = {items: [<briefTask>...], meta: {truncated, returned, limit, hint?}}.
Example: {}`),
	), a.handleCollectionInboxList)

	a.addTool(newTool("torque_collection_tasks_list",
		withDescription(`List the tasks in a collection, ordered by collection_position.
Response shape: data = {items: [<briefTask>...], meta: {truncated, returned, limit, hint?}}.
Example: {"collection_id":"COL-20260503-0001"}`),
		withString("collection_id", required(), desc("Collection ID")),
	), a.handleCollectionTasksList)
}

func (a *Adapter) handleCollectionCreate(ctx context.Context, req map[string]any) (any, error) {
	collection, err := a.svc.Collection.Create(service.CollectionCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
	})
	if err != nil {
		return errFromService(err)
	}
	return okResult(collection)
}

func (a *Adapter) handleCollectionList(ctx context.Context, req map[string]any) (any, error) {
	status := reqStr(req, "status")
	if status == "" {
		status = "active"
	}
	collections, err := a.svc.Collection.List(status)
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(collections))
	for _, c := range collections {
		items = append(items, c)
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleCollectionGet(ctx context.Context, req map[string]any) (any, error) {
	collection, err := a.svc.Collection.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(collection)
}

func (a *Adapter) handleCollectionUpdate(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")

	input := service.CollectionUpdateInput{}
	if reqHasArg(req, "name") {
		v := reqStr(req, "name")
		input.Name = &v
	}
	if reqHasArg(req, "description") {
		v := reqStr(req, "description")
		input.Description = &v
	}

	if err := a.svc.Collection.Update(id, input); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"updated": true,
		"message": fmt.Sprintf("Collection %s updated", id),
	})
}

func (a *Adapter) handleCollectionArchive(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Collection.Archive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":       id,
		"archived": true,
		"message":  fmt.Sprintf("Collection %s archived", id),
	})
}

func (a *Adapter) handleCollectionUnarchive(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Collection.Unarchive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":         id,
		"unarchived": true,
		"message":    fmt.Sprintf("Collection %s unarchived", id),
	})
}

func (a *Adapter) handleCollectionTaskAdd(ctx context.Context, req map[string]any) (any, error) {
	collectionID := reqStr(req, "collection_id")
	taskID := reqStr(req, "task_id")
	position := reqInt(req, "position")
	if err := a.svc.Collection.AddTask(collectionID, taskID, position); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"collection_id": collectionID,
		"task_id":       taskID,
		"added":         true,
	})
}

func (a *Adapter) handleCollectionTaskRemove(ctx context.Context, req map[string]any) (any, error) {
	taskID := reqStr(req, "task_id")
	// MCP tool intentionally unscoped ("remove from whatever collection it's
	// in" semantics, matching the tool description). The HTTP DELETE route
	// scopes by collection_id; agents that need scoped removal should call
	// that endpoint instead.
	if err := a.svc.Collection.RemoveTask(taskID, ""); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"task_id": taskID,
		"removed": true,
	})
}

func (a *Adapter) handleCollectionTaskReorder(ctx context.Context, req map[string]any) (any, error) {
	collectionID := reqStr(req, "collection_id")

	// task_ids accepts a JSON-array string (matching the adapter convention
	// established by tags/depends_on/files: schema declares string so LLM
	// clients that emit `"task_ids": "[\"T-1\",\"T-2\"]"` aren't rejected at
	// the mcp-go boundary) OR a native []interface{} (tests / well-typed
	// callers).
	var taskIDs []string
	if raw := reqStr(req, "task_ids"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &taskIDs); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid task_ids JSON: %v", err), "task_ids")
		}
	} else if v, ok := req["task_ids"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				taskIDs = append(taskIDs, s)
			}
		}
	}
	if len(taskIDs) == 0 {
		return errResult(ErrCodeArgInvalid, "task_ids must be a non-empty array of strings", "task_ids")
	}
	if err := a.svc.Collection.ReorderTasks(collectionID, taskIDs); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"collection_id": collectionID,
		"reordered":     len(taskIDs),
	})
}

func (a *Adapter) handleCollectionTaskMove(ctx context.Context, req map[string]any) (any, error) {
	taskID := reqStr(req, "task_id")
	target := reqStr(req, "target_collection_id")
	position := reqInt(req, "position")
	if err := a.svc.Collection.MoveTask(taskID, target, position); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"task_id":              taskID,
		"target_collection_id": target,
		"moved":                true,
	})
}

func (a *Adapter) handleCollectionInboxAdd(ctx context.Context, req map[string]any) (any, error) {
	taskID := reqStr(req, "task_id")
	if err := a.svc.Collection.AddToInbox(taskID); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"task_id":  taskID,
		"in_inbox": true,
	})
}

func (a *Adapter) handleCollectionInboxList(ctx context.Context, req map[string]any) (any, error) {
	tasks, err := a.svc.Collection.ListInboxTasks()
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, toBriefTask(t, briefTagSlugs(a.svc, t.ID)))
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleCollectionTasksList(ctx context.Context, req map[string]any) (any, error) {
	collectionID := reqStr(req, "collection_id")
	tasks, err := a.svc.Collection.ListCollectionTasks(collectionID)
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, toBriefTask(t, briefTagSlugs(a.svc, t.ID)))
	}
	return cappedJSONResult(items, limit)
}
