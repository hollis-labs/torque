package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
)

// Planner tools (feature-flagged: requires features.planner). Thin mappings
// onto service.PlannerService, which owns validation, ids, rev and the
// feature gate. The tools mirror the portfolio-tracker operations; see
// docs/mcp-tools-reference.md ("Planner").

const plannerFlagNote = "(feature-flagged: requires features.planner)"

func (a *Adapter) registerPlannerTools() {
	db := func(extra string) toolOpt {
		return withString("db", required(), desc("Planner database: priorities, workstreams, roadmap, ideas, decisions, risks, inbox"+extra))
	}
	id := withString("id", required(), desc("Item id, e.g. DEC-012, WS-torque, IN-003"))
	author := withString("author", desc(`Author of the write; default "portfolio-manager"`))

	a.addTool(newTool("torque_planner_databases",
		withDescription(`List the planner databases with id prefix, item count and latest updated date `+plannerFlagNote+`.
The planner holds the portfolio-level things: priorities, workstreams, roadmap, ideas, decisions, risks and an inbox. Tasks stay in Torque tasks; items point at them with links.
Response shape: data = [{name, prefix, schema, updated, count}].
Example: {}`),
	), a.handlePlannerDatabases)

	a.addTool(newTool("torque_planner_schema",
		withDescription(`Describe one database's item schema: required fields, every known field, enums by field, and the link kinds `+plannerFlagNote+`.
Call before create/update when unsure which enum values a field accepts.
Response shape: data = {db, id, description, required[], fields{}, enums{}, link_kinds[]}.
Example: {"db":"roadmap"}`),
		db(""),
	), a.handlePlannerSchema)

	a.addTool(newTool("torque_planner_list",
		withDescription(`List the items of a database in manual order (items with no order last) `+plannerFlagNote+`.
filters match top-level fields exactly (array fields match membership), e.g. {"status":"open","severity":"high"}. sort is a field name, "-" prefix for descending. By default items come back brief (id, title, tldr, status/kind/area/horizon/severity/rank, priority, rev, updated, order); pass brief=false for full items, or use torque_planner_get for one.
Response shape: data = [<item>...].
Example: {"db":"decisions","filters":{"status":"needs-decision"}}`),
		db(""),
		withObject("filters", desc("Exact-match filters on top-level fields")),
		withString("sort", desc("Field name; prefix - for descending")),
		withBoolean("brief", desc("Default true: summary fields only. false: full items")),
	), a.handlePlannerList)

	a.addTool(newTool("torque_planner_get",
		withDescription(`Get one planner item in full, with its comments and links `+plannerFlagNote+`.
Response shape: data = <item>.
Example: {"db":"decisions","id":"DEC-012"}`),
		db(""), id,
	), a.handlePlannerGet)

	a.addTool(newTool("torque_planner_search",
		withDescription(`Case-insensitive search across the planner: every term must appear in an item's id, title or text fields, including comments and options `+plannerFlagNote+`.
Response shape: data = [{db, id, title, status}].
Example: {"q":"nanite plugin","db":"roadmap"}`),
		withString("q", required(), desc("Search terms, all must match")),
		withString("db", desc("Limit to one database")),
	), a.handlePlannerSearch)

	a.addTool(newTool("torque_planner_create",
		withDescription(`Create a planner item `+plannerFlagNote+`.
id is generated when omitted (PR-/DEC-/RK-/IN- sequential; WS-/RM-/ID- slug of the title). rev starts at 1; created/updated, author and order (end of list) are defaulted. Validated against the database schema (torque_planner_schema). For inbox items prefer torque_planner_inbox_add.
Response shape: data = <item>.
Example: {"db":"risks","item":{"title":"No backup for planner DB","kind":"gap","severity":"high"}}`),
		db(""),
		withObject("item", required(), desc("The item; title is required")),
		author,
	), a.handlePlannerCreate)

	a.addTool(newTool("torque_planner_update",
		withDescription(`Merge a patch into an item: top-level fields, arrays replaced, null removes a field `+plannerFlagNote+`.
id, created, comments and rev cannot be patched (use torque_planner_comment for comments). If rev is given and differs from the item's current rev the call fails with a conflict naming the current rev; omit rev for last write wins.
Response shape: data = <item> (rev incremented).
Example: {"db":"roadmap","id":"RM-llm-gateway","patch":{"status":"landed"},"rev":5}`),
		db(""), id,
		withObject("patch", required(), desc("Fields to set; null removes a field")),
		withString("rev", desc("Expected current rev (integer) for conflict detection")),
	), a.handlePlannerUpdate)

	a.addTool(newTool("torque_planner_comment",
		withDescription(`Append a comment to an item `+plannerFlagNote+`.
kind "decision" marks the comment as the record of a decision. Bumps the item's rev.
Response shape: data = <item>.
Example: {"db":"ideas","id":"ID-cliproxy-review","text":"Spike done; see DEC-017."}`),
		db(""), id,
		withString("text", required(), desc("Comment text")),
		withString("kind", desc("comment (default) or decision")),
		author,
	), a.handlePlannerComment)

	a.addTool(newTool("torque_planner_relate",
		withDescription(`Add (or with remove=true, remove) a reference from one item to another in an id-list field `+plannerFlagNote+`.
field is related_ids (default), depends_on or any *_ids field (roadmap_ids, workstream_ids, ...). Both items must exist. Idempotent. External pointers (Torque tasks, PRs, URLs) use torque_planner_link_add.
Response shape: data = the "from" item.
Example: {"from_db":"ideas","from_id":"ID-x","to_db":"workstreams","to_id":"WS-torque","field":"workstream_ids"}`),
		withString("from_db", required(), desc("Database of the referencing item")),
		withString("from_id", required(), desc("Id of the referencing item")),
		withString("to_db", required(), desc("Database of the referenced item")),
		withString("to_id", required(), desc("Id of the referenced item")),
		withString("field", desc("related_ids (default), depends_on or *_ids")),
		withBoolean("remove", desc("true removes the reference")),
	), a.handlePlannerRelate)

	a.addTool(newTool("torque_planner_link_add",
		withDescription(`Add an external pointer {kind, ref, label?} to an item's links `+plannerFlagNote+`.
kind: torque (ref a task id like CW-20261003-0137), tesseract, tether, pr, adr, url (http/https only), file (a plain pointer, never read), commit, workstream. A duplicate kind+ref is a conflict.
Response shape: data = <item>.
Example: {"db":"roadmap","id":"RM-llm-gateway","kind":"torque","ref":"CW-20261003-0137","label":"gateway task"}`),
		db(""), id,
		withString("kind", required(), desc("Link kind")),
		withString("ref", required(), desc("Pointer: task id, URL, path, ...")),
		withString("label", desc("Optional display label")),
	), a.handlePlannerLinkAdd)

	a.addTool(newTool("torque_planner_link_remove",
		withDescription(`Remove the external pointer with this kind and ref from an item `+plannerFlagNote+`. not_found when there is no such pointer.
Response shape: data = <item>.
Example: {"db":"roadmap","id":"RM-llm-gateway","kind":"torque","ref":"CW-20261003-0137"}`),
		db(""), id,
		withString("kind", required(), desc("Link kind")),
		withString("ref", required(), desc("Pointer to remove")),
	), a.handlePlannerLinkRemove)

	a.addTool(newTool("torque_planner_reorder",
		withDescription(`Set the manual order of the listed items to 10, 20, 30 ... in the given sequence `+plannerFlagNote+`.
Other items that had an order follow in their current relative order; items with none stay unordered. All writes land together.
Response shape: data = [{id, order, rev}] in the new order.
Example: {"db":"priorities","ids":["PR-03","PR-01"]}`),
		db(""),
		withArray("ids", required(), items(map[string]any{"type": "string"}), desc("Item ids in the desired order")),
	), a.handlePlannerReorder)

	a.addTool(newTool("torque_planner_decide",
		withDescription(`Decide a needs-decision (or deferred) decision `+plannerFlagNote+`.
option must be one of the decision's options. Sets selected_option, status=decided, decided_at, decided_by; a comment is recorded as a decision comment. The decision text defaults to the option's label.
Response shape: data = <decision>.
Example: {"id":"DEC-021","option":"a","comment":"Going with the typed tables."}`),
		id,
		withString("option", required(), desc("Option id")),
		withString("comment", desc("Why; recorded as a decision comment")),
		author,
	), a.handlePlannerDecide)

	a.addTool(newTool("torque_planner_defer",
		withDescription(`Set a decision to deferred, with an optional note that is also recorded as a comment `+plannerFlagNote+`.
Response shape: data = <decision>.
Example: {"id":"DEC-021","note":"Wait for the Tether teams work."}`),
		id,
		withString("note", desc("Why it is deferred")),
		author,
	), a.handlePlannerDefer)

	a.addTool(newTool("torque_planner_reopen",
		withDescription(`Return a decision to needs-decision, clearing selected_option, decided_at, decided_by and defer_note `+plannerFlagNote+`.
Response shape: data = <decision>.
Example: {"id":"DEC-021"}`),
		id,
	), a.handlePlannerReopen)

	a.addTool(newTool("torque_planner_inbox_add",
		withDescription(`Add an item to the planner inbox: something to look at that is not an item yet `+plannerFlagNote+`.
path is a filesystem pointer only; the planner never reads it. Triage later with torque_planner_inbox_promote or torque_planner_inbox_dismiss.
Response shape: data = <inbox item> (IN-nnn, status new).
Example: {"title":"Look at JEV for triage","kind":"idea","body":"See ~/dev/inbox/jev.md","path":"~/dev/inbox/jev.md"}`),
		withString("title", required(), desc("What to look at")),
		withString("kind", desc("topic (default), idea, question, file, link, other")),
		withString("body", desc("Detail")),
		withString("path", desc("Filesystem path pointer")),
		withString("added_by", desc(`Who added it; default "portfolio-manager"`)),
	), a.handlePlannerInboxAdd)

	a.addTool(newTool("torque_planner_inbox_promote",
		withDescription(`Promote an inbox item into another database `+plannerFlagNote+`.
Creates an item in to_db from the inbox item (title; body becomes notes; path becomes a file link; related_ids point back), applies fields on top, and marks the inbox item promoted. Both writes land together. Defaults: ideas kind=idea; risks kind=risk severity=medium; roadmap area=process horizon=someday; priorities next rank.
Response shape: data = {inbox, item}.
Example: {"id":"IN-003","to_db":"ideas","fields":{"kind":"exploration"}}`),
		id,
		withString("to_db", required(), desc("priorities, workstreams, roadmap, ideas, decisions or risks")),
		withObject("fields", desc("Fields to set or override on the new item")),
	), a.handlePlannerInboxPromote)

	a.addTool(newTool("torque_planner_inbox_dismiss",
		withDescription(`Mark an inbox item dismissed; an optional note is recorded as a comment `+plannerFlagNote+`.
Response shape: data = <inbox item>.
Example: {"id":"IN-004","note":"Duplicate of IN-002."}`),
		id,
		withString("note", desc("Why")),
		author,
	), a.handlePlannerInboxDismiss)
}

// plannerObj reads an object argument that may arrive as a map or, from
// clients that stringify objects, as a JSON string.
func plannerObj(req map[string]any, key string) (map[string]any, error) {
	switch v := req[key].(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil, argError(ErrCodeArgInvalid, fmt.Sprintf("%s must be a JSON object: %v", key, err), key)
		}
		return m, nil
	}
	return nil, argError(ErrCodeArgInvalid, key+" must be a JSON object", key)
}

// plannerBrief keeps the summary fields of an item for list responses.
func plannerBrief(item map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"id", "title", "tldr", "status", "kind", "area", "horizon", "severity", "rank", "priority", "rev", "updated", "order"} {
		if v, ok := item[k]; ok {
			out[k] = v
		}
	}
	return out
}

func (a *Adapter) handlePlannerDatabases(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Databases()
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerSchema(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Schema(reqStr(req, "db"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerList(ctx context.Context, req map[string]any) (any, error) {
	filters, err := plannerObj(req, "filters")
	if err != nil {
		return nil, err
	}
	items, err := a.svc.Planner.List(reqStr(req, "db"), filters, reqStr(req, "sort"))
	if err != nil {
		return errFromService(err)
	}
	if !reqHasArg(req, "brief") || reqBool(req, "brief") {
		brief := make([]map[string]any, len(items))
		for i, it := range items {
			brief[i] = plannerBrief(it)
		}
		return okResult(brief)
	}
	return okResult(items)
}

func (a *Adapter) handlePlannerGet(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Get(reqStr(req, "db"), reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerSearch(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Search(reqStr(req, "q"), reqStr(req, "db"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerCreate(ctx context.Context, req map[string]any) (any, error) {
	item, err := plannerObj(req, "item")
	if err != nil {
		return nil, err
	}
	if item == nil {
		return errResult(ErrCodeArgInvalid, "item is required", "item")
	}
	out, err := a.svc.Planner.Create(reqStr(req, "db"), item, reqStr(req, "author"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerUpdate(ctx context.Context, req map[string]any) (any, error) {
	patch, err := plannerObj(req, "patch")
	if err != nil {
		return nil, err
	}
	if patch == nil {
		return errResult(ErrCodeArgInvalid, "patch is required", "patch")
	}
	var rev *int
	if v, ok := req["rev"]; ok && v != nil && v != "" { // number or numeric string; junk reads as 0 and conflicts loudly
		n := reqInt(req, "rev")
		rev = &n
	}
	out, err := a.svc.Planner.Update(reqStr(req, "db"), reqStr(req, "id"), patch, rev)
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerComment(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Comment(reqStr(req, "db"), reqStr(req, "id"), reqStr(req, "text"), reqStr(req, "author"), reqStr(req, "kind"), "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerRelate(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Link(
		service.PlannerRef{DB: reqStr(req, "from_db"), ID: reqStr(req, "from_id")},
		service.PlannerRef{DB: reqStr(req, "to_db"), ID: reqStr(req, "to_id")},
		reqStr(req, "field"), !reqBool(req, "remove"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerLinkAdd(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.LinkAdd(reqStr(req, "db"), reqStr(req, "id"),
		service.PlannerLink{Kind: reqStr(req, "kind"), Ref: reqStr(req, "ref"), Label: reqStr(req, "label")})
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerLinkRemove(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.LinkRemove(reqStr(req, "db"), reqStr(req, "id"), reqStr(req, "kind"), reqStr(req, "ref"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerReorder(ctx context.Context, req map[string]any) (any, error) {
	ids, err := reqStrSlice(req, "ids")
	if err != nil {
		return nil, err
	}
	out, err := a.svc.Planner.Reorder(reqStr(req, "db"), ids)
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerDecide(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Decide(reqStr(req, "id"), reqStr(req, "option"), reqStr(req, "comment"), reqStr(req, "author"), "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerDefer(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Defer(reqStr(req, "id"), reqStr(req, "note"), reqStr(req, "author"), "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerReopen(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.Reopen(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerInboxAdd(ctx context.Context, req map[string]any) (any, error) {
	fields := map[string]any{}
	for _, k := range []string{"title", "kind", "body", "path"} {
		if v := reqStr(req, k); v != "" {
			fields[k] = v
		}
	}
	out, err := a.svc.Planner.InboxAdd(fields, reqStr(req, "added_by"), "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handlePlannerInboxPromote(ctx context.Context, req map[string]any) (any, error) {
	fields, err := plannerObj(req, "fields")
	if err != nil {
		return nil, err
	}
	inbox, item, err := a.svc.Planner.InboxPromote(reqStr(req, "id"), reqStr(req, "to_db"), fields, "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"inbox": inbox, "item": item})
}

func (a *Adapter) handlePlannerInboxDismiss(ctx context.Context, req map[string]any) (any, error) {
	out, err := a.svc.Planner.InboxDismiss(reqStr(req, "id"), reqStr(req, "note"), reqStr(req, "author"), "")
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}
