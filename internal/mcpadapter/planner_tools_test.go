package mcpadapter_test

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPlannerAdapter(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	return adapterFromService(svc)
}

func plannerTool(t *testing.T, a *mcpadapter.Adapter, name string, args map[string]any) map[string]any {
	t.Helper()
	text, isErr := callTool(t, a, name, args)
	require.False(t, isErr, "%s: %s", name, text)
	var out map[string]any
	parseData(t, text, &out)
	return out
}

func plannerToolList(t *testing.T, a *mcpadapter.Adapter, name string, args map[string]any) []any {
	t.Helper()
	text, isErr := callTool(t, a, name, args)
	require.False(t, isErr, "%s: %s", name, text)
	var out []any
	parseData(t, text, &out)
	return out
}

func plannerToolErr(t *testing.T, a *mcpadapter.Adapter, name string, args map[string]any) (code, message string) {
	t.Helper()
	text, isErr := callTool(t, a, name, args)
	require.True(t, isErr, "%s should fail: %s", name, text)
	code, message, _ = parseError(t, text)
	return code, message
}

func TestPlannerToolsNotRegisteredWhenDisabled(t *testing.T) {
	a := setupAdapter(t)
	assert.False(t, toolIsRegistered(t, a, "torque_planner_list"))
}

func TestPlannerToolsSurfacedWhenEnabled(t *testing.T) {
	a := setupPlannerAdapter(t)
	var names []string
	for _, def := range a.Server().ToolDefinitions() {
		if len(def.Name) > len("torque_planner_") && def.Name[:len("torque_planner_")] == "torque_planner_" {
			names = append(names, def.Name)
		}
	}
	assert.Equal(t, []string{
		"torque_planner_board", "torque_planner_comment", "torque_planner_create", "torque_planner_databases", "torque_planner_decide",
		"torque_planner_defer", "torque_planner_get", "torque_planner_inbox_add", "torque_planner_inbox_dismiss",
		"torque_planner_inbox_promote", "torque_planner_link_add", "torque_planner_link_remove", "torque_planner_list",
		"torque_planner_relate", "torque_planner_reopen", "torque_planner_reorder", "torque_planner_schema",
		"torque_planner_search", "torque_planner_update",
	}, names)
}

func TestPlannerToolsLifecycle(t *testing.T) {
	a := setupPlannerAdapter(t)

	created := plannerTool(t, a, "torque_planner_create", map[string]any{
		"db": "risks", "item": map[string]any{"title": "Backups", "kind": "gap", "severity": "high", "tldr": "no backup"},
	})
	assert.Equal(t, "RK-001", created["id"])

	// A stringified object works too.
	created2 := plannerTool(t, a, "torque_planner_create", map[string]any{
		"db": "risks", "item": `{"title":"Second","kind":"debt","severity":"low"}`, "author": "tester",
	})
	assert.Equal(t, "tester", created2["author"])

	brief := plannerToolList(t, a, "torque_planner_list", map[string]any{"db": "risks", "filters": map[string]any{"severity": "high"}})
	require.Len(t, brief, 1)
	b := brief[0].(map[string]any)
	assert.Equal(t, "no backup", b["tldr"])
	assert.NotContains(t, b, "author", "brief list omits non-summary fields")
	full := plannerToolList(t, a, "torque_planner_list", map[string]any{"db": "risks", "brief": false})
	assert.Contains(t, full[0].(map[string]any), "author")

	got := plannerTool(t, a, "torque_planner_get", map[string]any{"db": "risks", "id": "RK-001"})
	assert.Equal(t, "Backups", got["title"])

	updated := plannerTool(t, a, "torque_planner_update", map[string]any{
		"db": "risks", "id": "RK-001", "patch": map[string]any{"status": "mitigating"}, "rev": "1",
	})
	assert.EqualValues(t, 2, updated["rev"])
	code, msg := plannerToolErr(t, a, "torque_planner_update", map[string]any{
		"db": "risks", "id": "RK-001", "patch": map[string]any{"status": "closed"}, "rev": 1, // numeric rev too
	})
	assert.Equal(t, "conflict", code)
	assert.Contains(t, msg, "current is 2")

	code, msg = plannerToolErr(t, a, "torque_planner_update", map[string]any{
		"db": "risks", "id": "RK-001", "patch": map[string]any{"severity": "apocalyptic"},
	})
	assert.Equal(t, "arg_invalid", code)
	assert.Contains(t, msg, "severity")

	commented := plannerTool(t, a, "torque_planner_comment", map[string]any{"db": "risks", "id": "RK-001", "text": "noted", "kind": "decision"})
	assert.Len(t, commented["comments"], 1)

	linked := plannerTool(t, a, "torque_planner_link_add", map[string]any{"db": "risks", "id": "RK-001", "kind": "torque", "ref": "CW-20261011-0050"})
	assert.Len(t, linked["links"], 1)
	code, _ = plannerToolErr(t, a, "torque_planner_link_add", map[string]any{"db": "risks", "id": "RK-001", "kind": "torque", "ref": "CW-20261011-0050"})
	assert.Equal(t, "conflict", code)
	removed := plannerTool(t, a, "torque_planner_link_remove", map[string]any{"db": "risks", "id": "RK-001", "kind": "torque", "ref": "CW-20261011-0050"})
	assert.NotContains(t, removed, "links")

	related := plannerTool(t, a, "torque_planner_relate", map[string]any{"from_db": "risks", "from_id": "RK-001", "to_db": "risks", "to_id": "RK-002"})
	assert.Equal(t, []any{"RK-002"}, related["related_ids"])
	unrelated := plannerTool(t, a, "torque_planner_relate", map[string]any{"from_db": "risks", "from_id": "RK-001", "to_db": "risks", "to_id": "RK-002", "remove": true})
	assert.NotContains(t, unrelated, "related_ids")

	order := plannerToolList(t, a, "torque_planner_reorder", map[string]any{"db": "risks", "ids": []any{"RK-002", "RK-001"}})
	assert.Equal(t, "RK-002", order[0].(map[string]any)["id"])

	hits := plannerToolList(t, a, "torque_planner_search", map[string]any{"q": "backups"})
	assert.Len(t, hits, 1)

	dbs := plannerToolList(t, a, "torque_planner_databases", map[string]any{})
	assert.Len(t, dbs, 7)
	schema := plannerTool(t, a, "torque_planner_schema", map[string]any{"db": "decisions"})
	assert.Contains(t, schema["enums"], "status")

	code, _ = plannerToolErr(t, a, "torque_planner_get", map[string]any{"db": "risks", "id": "RK-404"})
	assert.Equal(t, "not_found", code)
	code, _ = plannerToolErr(t, a, "torque_planner_create", map[string]any{"db": "risks"})
	assert.Equal(t, "arg_invalid", code)
}

func TestPlannerToolsDecisionsAndInbox(t *testing.T) {
	a := setupPlannerAdapter(t)
	plannerTool(t, a, "torque_planner_create", map[string]any{"db": "decisions", "item": map[string]any{
		"title": "pick", "options": []any{map[string]any{"id": "a", "label": "Alpha"}, map[string]any{"id": "b", "label": "Beta"}}}})

	d := plannerTool(t, a, "torque_planner_defer", map[string]any{"id": "DEC-001", "note": "later"})
	assert.Equal(t, "deferred", d["status"])
	code, _ := plannerToolErr(t, a, "torque_planner_decide", map[string]any{"id": "DEC-001", "option": "zzz"})
	assert.Equal(t, "arg_invalid", code)
	d = plannerTool(t, a, "torque_planner_decide", map[string]any{"id": "DEC-001", "option": "b", "comment": "beta it is", "author": "chrispian"})
	assert.Equal(t, "decided", d["status"])
	assert.Equal(t, "chrispian", d["decided_by"])
	d = plannerTool(t, a, "torque_planner_reopen", map[string]any{"id": "DEC-001"})
	assert.Equal(t, "needs-decision", d["status"])

	in := plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "Look at JEV", "kind": "idea", "path": "~/dev/inbox/jev.md"})
	assert.Equal(t, "IN-001", in["id"])
	res := plannerTool(t, a, "torque_planner_inbox_promote", map[string]any{"id": "IN-001", "to_db": "ideas"})
	assert.Equal(t, "promoted", res["inbox"].(map[string]any)["status"])
	assert.Equal(t, "idea", res["item"].(map[string]any)["kind"])
	code, _ = plannerToolErr(t, a, "torque_planner_inbox_promote", map[string]any{"id": "IN-001", "to_db": "ideas"})
	assert.Equal(t, "conflict", code)

	plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "noise"})
	dis := plannerTool(t, a, "torque_planner_inbox_dismiss", map[string]any{"id": "IN-002", "note": "dup"})
	assert.Equal(t, "dismissed", dis["status"])
}

func TestPlannerInboxAddAllocatesAfterDismissedMaxID(t *testing.T) {
	var collisionIDs []string
	for n := 8; n <= 22; n++ {
		collisionIDs = append(collisionIDs, fmt.Sprintf("IN-%03d", n))
	}
	for _, tc := range []struct {
		name     string
		ids      []string
		want     string
		wantNext string
	}{
		{"gaps and decimal digits", []string{"IN-001", "IN-017", "IN-019"}, "IN-020", "IN-021"},
		{"IN-019 collision", collisionIDs, "IN-023", "IN-024"},
		{"width rollover", []string{"IN-001", "IN-999"}, "IN-1000", "IN-1001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := setupPlannerAdapter(t)
			for _, id := range tc.ids {
				plannerTool(t, a, "torque_planner_create", map[string]any{
					"db": "inbox", "item": map[string]any{"id": id, "title": "Existing " + id},
				})
			}
			maxID := tc.ids[len(tc.ids)-1]
			plannerTool(t, a, "torque_planner_inbox_dismiss", map[string]any{"id": maxID})

			added := plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "After gaps and dismissal"})
			require.Equal(t, tc.want, added["id"], "allocate above the numeric maximum, including dismissed items")
			plannerTool(t, a, "torque_planner_inbox_dismiss", map[string]any{"id": tc.want})
			next := plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "After another dismissal"})
			assert.Equal(t, tc.wantNext, next["id"], "dismissal must not release an ID for reuse")

			for _, id := range tc.ids {
				existing := plannerTool(t, a, "torque_planner_get", map[string]any{"db": "inbox", "id": id})
				assert.Equal(t, "Existing "+id, existing["title"])
				if id == maxID {
					assert.Equal(t, "dismissed", existing["status"])
				}
			}
		})
	}
}

func TestPlannerBoardTool(t *testing.T) {
	a := setupPlannerAdapter(t)
	plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "Look at the board"})
	plannerTool(t, a, "torque_planner_create", map[string]any{"db": "decisions", "item": map[string]any{"title": "Pick", "status": "needs-decision"}})

	b := plannerTool(t, a, "torque_planner_board", map[string]any{"scope": "all"})
	sections := b["sections"].(map[string]any)
	holds := sections["holds"].([]any)
	require.Len(t, holds, 2)
	assert.Equal(t, "DEC-001", holds[0].(map[string]any)["id"], "decisions first, then the inbox")
	assert.EqualValues(t, 30, b["limit"])

	small := plannerTool(t, a, "torque_planner_board", map[string]any{"scope": "all", "limit": "1", "recent_hours": 24})
	assert.EqualValues(t, 1, small["limit"])
	assert.EqualValues(t, 24, small["recent_hours"])
	assert.Len(t, small["sections"].(map[string]any)["holds"], 1)
	assert.EqualValues(t, 2, small["totals"].(map[string]any)["holds"], "the total keeps the full count")

	text := plannerTool(t, a, "torque_planner_board", map[string]any{"scope": "all", "text": true})
	assert.Contains(t, text["text"], "HOLDS (WAITING ON YOU)")
	assert.Contains(t, text["text"], "DECIDE")

	code, _ := plannerToolErr(t, a, "torque_planner_board", map[string]any{"limit": 999})
	assert.Equal(t, "arg_invalid", code)
	code, _ = plannerToolErr(t, a, "torque_planner_board", map[string]any{"scope": "everything"})
	assert.Equal(t, "arg_invalid", code)
}

func TestPlannerBoardToolScope(t *testing.T) {
	a := setupPlannerAdapter(t)
	plannerTool(t, a, "torque_planner_create", map[string]any{"db": "workstreams", "item": map[string]any{
		"id": "WS-shown", "title": "Shown", "status": "active", "tags": []any{"board"}, "next_step": "ship it", "blockers": []any{"waiting on keys"},
	}})
	plannerTool(t, a, "torque_planner_create", map[string]any{"db": "workstreams", "item": map[string]any{"id": "WS-hidden", "title": "Hidden", "status": "active"}})
	plannerTool(t, a, "torque_planner_inbox_add", map[string]any{"title": "Unrelated"})

	curated := plannerTool(t, a, "torque_planner_board", map[string]any{})
	assert.Equal(t, "curated", curated["scope"], "curated is the default")
	sections := curated["sections"].(map[string]any)
	flight := sections["in_flight"].([]any)
	require.Len(t, flight, 1)
	assert.Equal(t, "WS-shown", flight[0].(map[string]any)["id"])
	holds := sections["holds"].([]any)
	require.Len(t, holds, 1, "only the blocked board workstream; the unrelated inbox item is not on the board")
	assert.Equal(t, "WS-shown", holds[0].(map[string]any)["id"])

	text := plannerTool(t, a, "torque_planner_board", map[string]any{"text": true})
	assert.Contains(t, text["text"], "WS-shown")
	assert.Contains(t, text["text"], "1 blocker")
	assert.NotContains(t, text["text"], "WS-hidden")

	all := plannerTool(t, a, "torque_planner_board", map[string]any{"scope": "all"})
	assert.Equal(t, "all", all["scope"])
	assert.Len(t, all["sections"].(map[string]any)["holds"], 1)
	assert.Equal(t, "IN-001", all["sections"].(map[string]any)["holds"].([]any)[0].(map[string]any)["id"])
}
