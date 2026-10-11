package sqlstore_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plannerFixtures holds one item per database exercising its typed columns,
// an unknown field (kept in extra), a comment and links.
func plannerFixtures() map[string]sqlstore.PlannerItem {
	common := func(id, title string) sqlstore.PlannerItem {
		return sqlstore.PlannerItem{
			"id": id, "title": title, "tldr": "short", "source": "test",
			"author": "chrispian", "created": "2026-10-08", "updated": "2026-10-09",
			"rev": 2, "order": 10.5, "priority": 3,
			"tags": []any{"a", "b"}, "related_ids": []any{"X-1"},
			"links":    []any{map[string]any{"kind": "torque", "ref": "CW-1"}, map[string]any{"kind": "url", "ref": "https://x.test", "label": "x"}},
			"comments": []any{map[string]any{"id": "c1", "author": "pm", "created": "2026-10-08", "text": "hi"}, map[string]any{"id": "c2", "author": "pm", "created": "2026-10-09", "kind": "decision", "text": "ok"}},
			"mystery":  map[string]any{"kept": true},
		}
	}
	with := func(it sqlstore.PlannerItem, kv map[string]any) sqlstore.PlannerItem {
		for k, v := range kv {
			it[k] = v
		}
		return it
	}
	return map[string]sqlstore.PlannerItem{
		"priorities":  with(common("PR-001", "p"), map[string]any{"rank": 1, "status": "active", "summary": "s", "sub_items": []any{"x"}, "open_questions": []any{"q"}, "roadmap_ids": []any{"RM-a"}, "workstream_ids": []any{"WS-a"}, "idea_ids": []any{"ID-a"}}),
		"workstreams": with(common("WS-a", "w"), map[string]any{"status": "active", "phase": "landing", "torque_project_ids": []any{"PRJ-1"}, "torque_tag": "t", "owner": "o", "goal": "g", "state": "st", "next_step": "n", "blockers": []any{"b"}, "priority_ids": []any{"PR-001"}, "roadmap_ids": []any{"RM-a"}, "tether_workstream_id": "tw"}),
		"roadmap":     with(common("RM-a", "r"), map[string]any{"area": "gui", "horizon": "now", "status": "planned", "mvp": true, "outcome": "o", "depends_on": []any{"RM-b"}, "priority_ids": []any{"PR-001"}, "workstream_ids": []any{"WS-a"}, "tasks": []any{"CW-2"}}),
		"ideas":       with(common("ID-a", "i"), map[string]any{"kind": "question", "status": "new", "summary": "s", "context": "c", "options": []any{map[string]any{"id": "o1", "label": "L"}}, "recommendation": "r", "decision": "d", "discuss_in": "anytime", "priority_ids": []any{"PR-001"}, "roadmap_ids": []any{"RM-a"}, "workstream_ids": []any{"WS-a"}}),
		"decisions":   with(common("DEC-001", "d"), map[string]any{"status": "decided", "date": "2026-10-07", "decided_by": "c", "area": "process", "decision": "d", "rationale": "r", "alternatives": []any{"alt"}, "options": []any{map[string]any{"id": "o1", "label": "L"}}, "selected_option": "o1", "decided_at": "2026-10-08", "defer_note": "dn", "supersedes": "DEC-000", "superseded_by": "DEC-002"}),
		"risks":       with(common("RK-001", "k"), map[string]any{"kind": "gap", "severity": "high", "status": "open", "description": "d", "impact": "i", "mitigation": "m", "owner": "o", "raised": "2026-10-01", "roadmap_ids": []any{"RM-a"}, "workstream_ids": []any{"WS-a"}}),
		"inbox":       with(common("IN-001", "n"), map[string]any{"kind": "file", "status": "promoted", "body": "b", "path": "/p", "added_by": "pm", "promoted_to": map[string]any{"db": "ideas", "id": "ID-a"}}),
	}
}

// normalize makes two items comparable regardless of Go number types.
func normalize(t *testing.T, it sqlstore.PlannerItem) map[string]any {
	t.Helper()
	b, err := json.Marshal(it)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func TestPlannerRoundTripEveryDatabase(t *testing.T) {
	store := setupTestStore(t)
	fixtures := plannerFixtures()
	assert.ElementsMatch(t, sqlstore.PlannerDatabases(), keysOf(fixtures), "fixture per database")

	for db, item := range fixtures {
		require.NoError(t, store.PlannerCreate(db, item), db)
		got, err := store.PlannerGet(db, item["id"].(string))
		require.NoError(t, err, db)
		assert.Equal(t, normalize(t, item), normalize(t, got), db)
	}
}

func keysOf(m map[string]sqlstore.PlannerItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPlannerMinimalItemOmitsEmptyFields(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.PlannerCreate("risks", sqlstore.PlannerItem{"id": "RK-9", "title": "t", "kind": "risk", "severity": "low", "status": "open"}))
	got, err := store.PlannerGet("risks", "RK-9")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "RK-9", "title": "t", "kind": "risk", "severity": "low", "status": "open", "rev": float64(1)}, normalize(t, got))
}

func TestPlannerDuplicateAndUnknown(t *testing.T) {
	store := setupTestStore(t)
	it := sqlstore.PlannerItem{"id": "IN-1", "title": "t"}
	require.NoError(t, store.PlannerCreate("inbox", it))
	assert.True(t, errors.Is(store.PlannerCreate("inbox", it), sqlstore.ErrPlannerExists))
	assert.True(t, errors.Is(store.PlannerCreate("nope", it), sqlstore.ErrPlannerNotFound))
	_, err := store.PlannerGet("inbox", "IN-404")
	assert.True(t, errors.Is(err, sqlstore.ErrPlannerNotFound))
}

func TestPlannerUpdateChecksRev(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.PlannerCreate("ideas", sqlstore.PlannerItem{"id": "ID-1", "title": "t", "kind": "idea", "status": "new", "rev": 1}))

	require.NoError(t, store.PlannerUpdate("ideas", sqlstore.PlannerItem{"id": "ID-1", "title": "t2", "kind": "idea", "status": "discussing", "rev": 2}, 1))
	got, err := store.PlannerGet("ideas", "ID-1")
	require.NoError(t, err)
	assert.Equal(t, "t2", got["title"])
	assert.EqualValues(t, 2, got["rev"])

	err = store.PlannerUpdate("ideas", sqlstore.PlannerItem{"id": "ID-1", "title": "stale", "rev": 2}, 1)
	assert.True(t, errors.Is(err, sqlstore.ErrPlannerConflict))
	err = store.PlannerUpdate("ideas", sqlstore.PlannerItem{"id": "ID-404", "title": "x", "rev": 2}, 1)
	assert.True(t, errors.Is(err, sqlstore.ErrPlannerNotFound))
}

func TestPlannerLinksAndComments(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.PlannerCreate("decisions", sqlstore.PlannerItem{"id": "DEC-1", "title": "t", "status": "needs-decision"}))

	added, err := store.PlannerAddLink("decisions", "DEC-1", "pr", "hollis-labs/torque#1", "PR")
	require.NoError(t, err)
	assert.True(t, added)
	added, err = store.PlannerAddLink("decisions", "DEC-1", "pr", "hollis-labs/torque#1", "")
	require.NoError(t, err)
	assert.False(t, added, "same kind+ref is not added twice")

	require.NoError(t, store.PlannerAddComment("decisions", "DEC-1", map[string]any{"id": "c1", "author": "a", "text": "first"}))
	require.NoError(t, store.PlannerAddComment("decisions", "DEC-1", map[string]any{"id": "c2", "author": "a", "text": "second", "kind": "decision"}))

	got, err := store.PlannerGet("decisions", "DEC-1")
	require.NoError(t, err)
	assert.Len(t, got["links"], 1)
	comments := got["comments"].([]any)
	require.Len(t, comments, 2)
	assert.Equal(t, "first", comments[0].(map[string]any)["text"], "comments keep insertion order")
	assert.Equal(t, "decision", comments[1].(map[string]any)["kind"])

	removed, err := store.PlannerRemoveLink("decisions", "DEC-1", "pr", "hollis-labs/torque#1")
	require.NoError(t, err)
	assert.True(t, removed)
	removed, err = store.PlannerRemoveLink("decisions", "DEC-1", "pr", "hollis-labs/torque#1")
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestPlannerListOrdersByOrderThenInsertion(t *testing.T) {
	store := setupTestStore(t)
	for _, it := range []sqlstore.PlannerItem{
		{"id": "IN-3", "title": "unordered first"},
		{"id": "IN-2", "title": "b", "order": 20},
		{"id": "IN-1", "title": "a", "order": 10},
		{"id": "IN-4", "title": "unordered second"},
	} {
		require.NoError(t, store.PlannerCreate("inbox", it))
	}
	items, err := store.PlannerList("inbox")
	require.NoError(t, err)
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	assert.Equal(t, []string{"IN-1", "IN-2", "IN-3", "IN-4"}, ids)

	all, err := store.PlannerIDs("inbox")
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, all)
}

func TestPlannerApplyIsAtomic(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.PlannerCreate("inbox", sqlstore.PlannerItem{"id": "IN-1", "title": "t", "rev": 1,
		"comments": []any{map[string]any{"id": "c-1", "text": "keep"}}}))

	// The second write is stale (rev 1 expected 5): nothing from the batch lands.
	err := store.PlannerApply([]sqlstore.PlannerWrite{
		{DB: "ideas", Item: sqlstore.PlannerItem{"id": "ID-new", "title": "new"}, Create: true},
		{DB: "inbox", Item: sqlstore.PlannerItem{"id": "IN-1", "title": "changed", "rev": 6}, ExpectedRev: 5},
	})
	require.True(t, errors.Is(err, sqlstore.ErrPlannerConflict))
	_, err = store.PlannerGet("ideas", "ID-new")
	assert.True(t, errors.Is(err, sqlstore.ErrPlannerNotFound), "the create rolled back")
	got, err := store.PlannerGet("inbox", "IN-1")
	require.NoError(t, err)
	assert.Equal(t, "t", got["title"])

	// A good rewrite replaces comments and links with the ones carried.
	require.NoError(t, store.PlannerApply([]sqlstore.PlannerWrite{{DB: "inbox", ExpectedRev: 1, Item: sqlstore.PlannerItem{
		"id": "IN-1", "title": "t2", "rev": 2,
		"comments": []any{map[string]any{"id": "c-1", "text": "keep"}, map[string]any{"id": "c-2", "text": "new"}},
		"links":    []any{map[string]any{"kind": "url", "ref": "https://x.test"}},
	}}}))
	got, err = store.PlannerGet("inbox", "IN-1")
	require.NoError(t, err)
	assert.Len(t, got["comments"], 2)
	assert.Len(t, got["links"], 1)
}
