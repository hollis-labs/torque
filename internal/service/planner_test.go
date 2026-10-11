package service_test

import (
	"errors"
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plannerService(t *testing.T) *service.PlannerService {
	t.Helper()
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	return svc.Planner
}

func mustCreate(t *testing.T, p *service.PlannerService, db string, item service.PlannerItem) service.PlannerItem {
	t.Helper()
	got, err := p.Create(db, item, "tester")
	require.NoError(t, err)
	return got
}

func TestPlannerRequiresFeature(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Planner.List("risks", nil, "")
	var fe *service.FeatureDisabledError
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, "planner", fe.Feature)
}

func TestPlannerCreateAllocatesIDsAndDefaults(t *testing.T) {
	p := plannerService(t)

	a := mustCreate(t, p, "decisions", service.PlannerItem{"title": "First"})
	b := mustCreate(t, p, "decisions", service.PlannerItem{"title": "Second"})
	assert.Equal(t, "DEC-001", a["id"])
	assert.Equal(t, "DEC-002", b["id"])
	assert.Equal(t, "needs-decision", a["status"])
	assert.EqualValues(t, 1, a["rev"])
	assert.Equal(t, "tester", a["author"])
	assert.NotEmpty(t, a["created"])
	assert.NotContains(t, a, "order", "no item has an order yet, so none is invented")

	pr := mustCreate(t, p, "priorities", service.PlannerItem{"title": "Top", "rank": 1})
	assert.Equal(t, "PR-01", pr["id"], "priorities keep a two-digit counter")

	w1 := mustCreate(t, p, "workstreams", service.PlannerItem{"title": "Torque: Planner!"})
	w2 := mustCreate(t, p, "workstreams", service.PlannerItem{"title": "Torque: Planner!"})
	assert.Equal(t, "WS-torque-planner", w1["id"])
	assert.Equal(t, "WS-torque-planner-2", w2["id"])

	// A new item goes to the end once some item has an order.
	_, err := p.Reorder("decisions", []string{"DEC-002", "DEC-001"})
	require.NoError(t, err)
	c := mustCreate(t, p, "decisions", service.PlannerItem{"title": "Third"})
	assert.EqualValues(t, 30, c["order"])

	// Inbox items record added_by, not author.
	in := mustCreate(t, p, "inbox", service.PlannerItem{"title": "look"})
	assert.Equal(t, "tester", in["added_by"])
	assert.NotContains(t, in, "author")
	assert.Equal(t, "topic", in["kind"])
}

func TestPlannerCreateValidatesAndRefusesDuplicates(t *testing.T) {
	p := plannerService(t)

	_, err := p.Create("risks", service.PlannerItem{"title": "t", "severity": "catastrophic"}, "")
	var ve *service.PlannerValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Errors, service.PlannerFieldError{Path: "severity", Message: "must be one of low, medium, high"})
	var plain *service.ValidationError
	assert.True(t, errors.As(err, &plain), "unwraps to ValidationError for generic handlers")

	_, err = p.Create("risks", service.PlannerItem{"severity": "low"}, "")
	require.True(t, errors.As(err, &ve), "no title and no id")

	mustCreate(t, p, "risks", service.PlannerItem{"id": "RK-777", "title": "t", "kind": "risk", "severity": "low"})
	_, err = p.Create("risks", service.PlannerItem{"id": "RK-777", "title": "again", "kind": "risk", "severity": "low"}, "")
	var ce *service.PlannerConflictError
	require.True(t, errors.As(err, &ce))

	// ids are unique across databases too.
	_, err = p.Create("ideas", service.PlannerItem{"id": "RK-777", "title": "x"}, "")
	require.True(t, errors.As(err, &ce))

	_, err = p.Create("nope", service.PlannerItem{"title": "t"}, "")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestPlannerUpdateMergesAndChecksRev(t *testing.T) {
	p := plannerService(t)
	it := mustCreate(t, p, "ideas", service.PlannerItem{"title": "t", "kind": "idea", "tldr": "x", "tags": []any{"a"}})

	rev := 1
	got, err := p.Update("ideas", it["id"].(string), service.PlannerItem{"status": "discussing", "tldr": nil, "tags": []any{"b", "c"}}, &rev)
	require.NoError(t, err)
	assert.Equal(t, "discussing", got["status"])
	assert.NotContains(t, got, "tldr", "null removes a field")
	assert.Equal(t, []any{"b", "c"}, got["tags"], "arrays are replaced")
	assert.EqualValues(t, 2, got["rev"])

	// A stale rev conflicts and carries the current item.
	_, err = p.Update("ideas", it["id"].(string), service.PlannerItem{"status": "parked"}, &rev)
	var ce *service.PlannerConflictError
	require.True(t, errors.As(err, &ce))
	assert.EqualValues(t, 2, ce.Current["rev"])
	assert.Equal(t, "discussing", ce.Current["status"])

	// No rev is last-write-wins.
	got, err = p.Update("ideas", it["id"].(string), service.PlannerItem{"status": "parked"}, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 3, got["rev"])

	// Protected fields and schema violations are refused; nothing is written.
	_, err = p.Update("ideas", it["id"].(string), service.PlannerItem{"rev": 9}, nil)
	var ve *service.ValidationError
	require.True(t, errors.As(err, &ve))
	_, err = p.Update("ideas", it["id"].(string), service.PlannerItem{"status": "bogus"}, nil)
	var pv *service.PlannerValidationError
	require.True(t, errors.As(err, &pv))
	again, err := p.Get("ideas", it["id"].(string))
	require.NoError(t, err)
	assert.EqualValues(t, 3, again["rev"])

	_, err = p.Update("ideas", "ID-missing", service.PlannerItem{"status": "new"}, nil)
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestPlannerCommentsGetSequentialIDs(t *testing.T) {
	p := plannerService(t)
	it := mustCreate(t, p, "ideas", service.PlannerItem{"title": "t", "kind": "idea"})
	id := it["id"].(string)

	_, err := p.Comment("ideas", id, "  ", "", "", "")
	require.Error(t, err)

	got, err := p.Comment("ideas", id, "one", "", "", "pm")
	require.NoError(t, err)
	got, err = p.Comment("ideas", id, "two", "alice", "decision", "pm")
	require.NoError(t, err)
	cs := got["comments"].([]any)
	require.Len(t, cs, 2)
	assert.Equal(t, "c-1", cs[0].(map[string]any)["id"])
	assert.Equal(t, "pm", cs[0].(map[string]any)["author"], "actor is the default author")
	assert.Equal(t, "c-2", cs[1].(map[string]any)["id"])
	assert.Equal(t, "alice", cs[1].(map[string]any)["author"], "an explicit author wins")
	assert.Equal(t, "decision", cs[1].(map[string]any)["kind"])
	assert.EqualValues(t, 3, got["rev"], "each comment bumps rev")

	_, err = p.Comment("ideas", id, "x", "", "shout", "")
	require.Error(t, err)
}

func TestPlannerItemLinks(t *testing.T) {
	p := plannerService(t)
	idea := mustCreate(t, p, "ideas", service.PlannerItem{"title": "i", "kind": "idea"})
	ws := mustCreate(t, p, "workstreams", service.PlannerItem{"title": "w"})
	from := service.PlannerRef{DB: "ideas", ID: idea["id"].(string)}
	to := service.PlannerRef{DB: "workstreams", ID: ws["id"].(string)}

	got, err := p.Link(from, to, "workstream_ids", true)
	require.NoError(t, err)
	assert.Equal(t, []any{ws["id"]}, got["workstream_ids"])
	assert.EqualValues(t, 2, got["rev"])

	got, err = p.Link(from, to, "workstream_ids", true)
	require.NoError(t, err)
	assert.EqualValues(t, 2, got["rev"], "adding twice changes nothing")

	got, err = p.Link(from, to, "workstream_ids", false)
	require.NoError(t, err)
	assert.NotContains(t, got, "workstream_ids")

	_, err = p.Link(from, service.PlannerRef{DB: "workstreams", ID: "WS-nope"}, "", true)
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf), "both ends must exist")
	_, err = p.Link(from, to, "title", true)
	var ve *service.ValidationError
	require.True(t, errors.As(err, &ve), "only id-list fields may be linked")
}

func TestPlannerExternalLinks(t *testing.T) {
	p := plannerService(t)
	it := mustCreate(t, p, "decisions", service.PlannerItem{"title": "d"})
	id := it["id"].(string)

	got, err := p.LinkAdd("decisions", id, service.PlannerLink{Kind: "torque", Ref: "CW-20261003-0137", Label: " task "})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"kind": "torque", "ref": "CW-20261003-0137", "label": "task"}}, got["links"])

	for _, bad := range []service.PlannerLink{
		{Kind: "carrier-pigeon", Ref: "x"},
		{Kind: "url", Ref: "ftp://x"},
		{Kind: "torque", Ref: "task-1"},
		{Kind: "pr", Ref: "  "},
	} {
		_, err = p.LinkAdd("decisions", id, bad)
		var pv *service.PlannerValidationError
		assert.True(t, errors.As(err, &pv), "%+v", bad)
	}

	_, err = p.LinkAdd("decisions", id, service.PlannerLink{Kind: "torque", Ref: "CW-20261003-0137"})
	var ce *service.PlannerConflictError
	require.True(t, errors.As(err, &ce), "same kind+ref twice")

	got, err = p.LinkRemove("decisions", id, "torque", "CW-20261003-0137")
	require.NoError(t, err)
	assert.NotContains(t, got, "links")
	_, err = p.LinkRemove("decisions", id, "torque", "CW-20261003-0137")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestPlannerReorder(t *testing.T) {
	p := plannerService(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		mustCreate(t, p, "inbox", service.PlannerItem{"id": "IN-" + id, "title": id})
	}
	out, err := p.Reorder("inbox", []string{"IN-c", "IN-a"})
	require.NoError(t, err)
	var ids []string
	for _, o := range out {
		ids = append(ids, o.ID)
	}
	assert.Equal(t, []string{"IN-c", "IN-a"}, ids, "unordered items stay unordered")
	assert.EqualValues(t, 10, out[0].Order)
	assert.EqualValues(t, 20, out[1].Order)

	// Now b gets an order; the earlier-ordered items follow the named ones.
	out, err = p.Reorder("inbox", []string{"IN-b"})
	require.NoError(t, err)
	ids = nil
	for _, o := range out {
		ids = append(ids, o.ID)
	}
	assert.Equal(t, []string{"IN-b", "IN-c", "IN-a"}, ids)

	list, err := p.List("inbox", nil, "")
	require.NoError(t, err)
	var listed []string
	for _, it := range list {
		listed = append(listed, it["id"].(string))
	}
	assert.Equal(t, []string{"IN-b", "IN-c", "IN-a", "IN-d"}, listed)

	_, err = p.Reorder("inbox", []string{"IN-a", "IN-a"})
	require.Error(t, err)
	_, err = p.Reorder("inbox", []string{"IN-zzz"})
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestPlannerListFiltersAndSorts(t *testing.T) {
	p := plannerService(t)
	mustCreate(t, p, "risks", service.PlannerItem{"id": "RK-1", "title": "b", "kind": "risk", "severity": "high", "status": "open", "tags": []any{"x"}})
	mustCreate(t, p, "risks", service.PlannerItem{"id": "RK-2", "title": "a", "kind": "gap", "severity": "low", "status": "open"})
	mustCreate(t, p, "risks", service.PlannerItem{"id": "RK-3", "title": "c", "kind": "risk", "severity": "low", "status": "closed", "tags": []any{"x", "y"}})

	ids := func(items []service.PlannerItem) (out []string) {
		for _, it := range items {
			out = append(out, it["id"].(string))
		}
		return
	}
	got, _ := p.List("risks", map[string]any{"kind": "risk"}, "")
	assert.Equal(t, []string{"RK-1", "RK-3"}, ids(got))
	got, _ = p.List("risks", map[string]any{"tags": "y"}, "")
	assert.Equal(t, []string{"RK-3"}, ids(got), "array fields match membership")
	got, _ = p.List("risks", nil, "title")
	assert.Equal(t, []string{"RK-2", "RK-1", "RK-3"}, ids(got))
	got, _ = p.List("risks", nil, "-title")
	assert.Equal(t, []string{"RK-3", "RK-1", "RK-2"}, ids(got))
}

func TestPlannerDecisionLifecycle(t *testing.T) {
	p := plannerService(t)
	d := mustCreate(t, p, "decisions", service.PlannerItem{
		"title": "pick",
		"options": []any{
			map[string]any{"id": "a", "label": "Alpha"},
			map[string]any{"id": "b", "label": "Beta"},
		},
	})
	id := d["id"].(string)

	got, err := p.Defer(id, "later", "", "pm")
	require.NoError(t, err)
	assert.Equal(t, "deferred", got["status"])
	assert.Equal(t, "later", got["defer_note"])

	_, err = p.Decide(id, "zzz", "", "", "pm")
	var pv *service.PlannerValidationError
	require.True(t, errors.As(err, &pv))

	got, err = p.Decide(id, "b", "going with beta", "", "chrispian")
	require.NoError(t, err)
	assert.Equal(t, "decided", got["status"])
	assert.Equal(t, "b", got["selected_option"])
	assert.Equal(t, "chrispian", got["decided_by"])
	assert.Equal(t, "Beta", got["decision"], "decision text defaults to the option label")
	assert.NotContains(t, got, "defer_note")
	cs := got["comments"].([]any)
	assert.Equal(t, "decision", cs[len(cs)-1].(map[string]any)["kind"])

	got, err = p.Reopen(id)
	require.NoError(t, err)
	assert.Equal(t, "needs-decision", got["status"])
	for _, k := range []string{"selected_option", "decided_at", "decided_by", "defer_note"} {
		assert.NotContains(t, got, k)
	}
}

func TestPlannerInboxFlow(t *testing.T) {
	p := plannerService(t)
	in, err := p.InboxAdd(service.PlannerItem{"title": "Explore X", "body": "details", "path": "/tmp/x.md"}, "", "pm")
	require.NoError(t, err)
	assert.Equal(t, "IN-001", in["id"])

	inbox, item, err := p.InboxPromote("IN-001", "ideas", service.PlannerItem{"kind": "exploration"}, "pm")
	require.NoError(t, err)
	assert.Equal(t, "promoted", inbox["status"])
	assert.Equal(t, map[string]any{"db": "ideas", "id": "ID-explore-x"}, inbox["promoted_to"])
	assert.Contains(t, inbox["related_ids"], "ID-explore-x")
	assert.Equal(t, "exploration", item["kind"], "fields override defaults")
	assert.Equal(t, "details", item["notes"], "body becomes notes")
	assert.Equal(t, []any{map[string]any{"kind": "file", "ref": "/tmp/x.md"}}, item["links"])
	assert.Equal(t, []any{"IN-001"}, item["related_ids"])

	_, _, err = p.InboxPromote("IN-001", "risks", nil, "pm")
	var ce *service.PlannerConflictError
	require.True(t, errors.As(err, &ce), "already promoted")
	_, _, err = p.InboxPromote("IN-001", "inbox", nil, "pm")
	require.Error(t, err)

	// A failed promote leaves the inbox item untouched.
	p.InboxAdd(service.PlannerItem{"title": "Second"}, "", "pm")
	_, _, err = p.InboxPromote("IN-002", "risks", service.PlannerItem{"severity": "nonsense"}, "pm")
	require.Error(t, err)
	still, _ := p.Get("inbox", "IN-002")
	assert.Equal(t, "new", still["status"])

	_, item, err = p.InboxPromote("IN-002", "risks", nil, "pm")
	require.NoError(t, err)
	assert.Equal(t, "RK-001", item["id"])
	assert.Equal(t, "medium", item["severity"])

	_, _, err = p.InboxPromote("IN-404", "ideas", nil, "pm")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))

	p.InboxAdd(service.PlannerItem{"title": "Third"}, "", "pm")
	got, err := p.InboxDismiss("IN-003", "not now", "", "pm")
	require.NoError(t, err)
	assert.Equal(t, "dismissed", got["status"])
	assert.Equal(t, "not now", got["comments"].([]any)[0].(map[string]any)["text"])

	// Promoting into priorities takes the next rank.
	mustCreate(t, p, "priorities", service.PlannerItem{"title": "p", "rank": 4})
	p.InboxAdd(service.PlannerItem{"title": "Fourth"}, "", "pm")
	_, item, err = p.InboxPromote("IN-004", "priorities", nil, "pm")
	require.NoError(t, err)
	assert.EqualValues(t, 5, item["rank"])
}

func TestPlannerSearchSchemaAndDatabases(t *testing.T) {
	p := plannerService(t)
	mustCreate(t, p, "ideas", service.PlannerItem{"title": "Alpha idea", "kind": "idea", "notes": "mentions Zebra"})
	i2 := mustCreate(t, p, "risks", service.PlannerItem{"title": "Beta", "kind": "risk", "severity": "low"})
	_, err := p.Comment("risks", i2["id"].(string), "zebra crossing", "", "", "")
	require.NoError(t, err)

	hits, err := p.Search("ZEBRA", "")
	require.NoError(t, err)
	require.Len(t, hits, 2)
	hits, err = p.Search("zebra alpha", "")
	require.NoError(t, err)
	require.Len(t, hits, 1, "every term must match")
	assert.Equal(t, "ideas", hits[0].DB)
	hits, _ = p.Search("zebra", "risks")
	require.Len(t, hits, 1)
	_, err = p.Search("   ", "")
	require.Error(t, err)

	info, err := p.Schema("roadmap")
	require.NoError(t, err)
	assert.Contains(t, info.Required, "horizon")
	assert.Equal(t, []string{"now", "next", "later", "someday"}, info.Enums["horizon"])
	assert.Contains(t, info.LinkKinds, "torque")
	_, err = p.Schema("nope")
	require.Error(t, err)

	dbs, err := p.Databases()
	require.NoError(t, err)
	require.Len(t, dbs, 7)
	counts := map[string]int{}
	for _, d := range dbs {
		counts[d.Name] = d.Count
	}
	assert.Equal(t, 1, counts["ideas"])
	assert.Equal(t, 0, counts["decisions"])
}

func TestPlannerConcurrentWritesAllLand(t *testing.T) {
	p := plannerService(t)
	it := mustCreate(t, p, "ideas", service.PlannerItem{"title": "busy", "kind": "idea"})
	id := it["id"].(string)

	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := p.Comment("ideas", id, "hello", "", "", "pm")
			errs <- err
		}()
	}
	var failed int
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			failed++
			t.Logf("comment failed: %v", err)
		}
	}
	got, err := p.Get("ideas", id)
	require.NoError(t, err)
	comments := got["comments"].([]any)
	// Every comment that reported success must be present, with unique ids.
	assert.Len(t, comments, n-failed)
	seen := map[any]bool{}
	for _, c := range comments {
		cid := c.(map[string]any)["id"]
		assert.False(t, seen[cid], "duplicate comment id %v", cid)
		seen[cid] = true
	}
	assert.EqualValues(t, 1+len(comments), got["rev"], "rev counts every write once")
	assert.Zero(t, failed, "retries absorb ordinary contention")
}
