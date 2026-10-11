package service_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTracker writes tracker-shaped envelopes into a temp dir.
func writeTracker(t *testing.T, dbs map[string][]map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	for db, items := range dbs {
		b, err := json.Marshal(map[string]any{"schema": "portfolio/" + db + "@1", "updated": "2026-10-08", "items": items})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, db+".json"), b, 0o644))
	}
	return dir
}

func trackerFixture() map[string][]map[string]any {
	return map[string][]map[string]any{
		"roadmap": {
			{"id": "RM-a", "title": "A", "area": "gui", "horizon": "now", "status": "planned", "mvp": false, "rev": 3,
				"created": "2026-10-01", "updated": "2026-10-05", "order": 20, "tags": []any{"x"},
				"links":    []any{map[string]any{"kind": "torque", "ref": "CW-20261001-0001", "label": "t"}},
				"comments": []any{map[string]any{"id": "c-1", "author": "pm", "text": "hi", "created": "2026-10-02T10:00:00.000Z"}, map[string]any{"id": "c-2", "author": "me", "kind": "decision", "text": "ok", "created": "2026-10-03T10:00:00.000Z"}},
				"made_up":  map[string]any{"kept": []any{1, 2}}},
		},
		"decisions": {
			{"id": "DEC-001", "title": "D", "status": "decided", "rev": 2, "created": "2026-10-07", "updated": "2026-10-08", "related_ids": []any{"RM-a"}, "options": []any{map[string]any{"id": "a", "label": "A"}}},
		},
		"inbox": {
			{"id": "IN-001", "title": "no rev here", "kind": "topic", "status": "new"},
		},
	}
}

func importCounts(r *service.PlannerImportReport) map[string][4]int {
	out := map[string][4]int{}
	for _, d := range r.DBs {
		out[d.DB] = [4]int{d.Create, d.Update, d.Unchanged, d.Invalid}
	}
	return out
}

func TestPlannerImportKeepsEverythingAndIsIdempotent(t *testing.T) {
	p := plannerService(t)
	dir := writeTracker(t, trackerFixture())

	dry, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir})
	require.NoError(t, err)
	assert.False(t, dry.Applied)
	assert.Equal(t, [4]int{1, 0, 0, 0}, importCounts(dry)["roadmap"])
	got, _ := p.List("roadmap", nil, "")
	assert.Empty(t, got, "a dry run writes nothing")

	rep, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true})
	require.NoError(t, err)
	assert.True(t, rep.Applied)
	assert.True(t, rep.Verified, "%+v", rep.Mismatches)
	assert.True(t, rep.Clean())

	rm, err := p.Get("roadmap", "RM-a")
	require.NoError(t, err)
	assert.EqualValues(t, 3, rm["rev"], "rev is kept, not reset")
	assert.Equal(t, "2026-10-01", rm["created"])
	assert.Equal(t, "2026-10-05", rm["updated"])
	assert.EqualValues(t, 20, rm["order"])
	assert.Equal(t, map[string]any{"kept": []any{float64(1), float64(2)}}, rm["made_up"], "unknown fields survive")
	cs := rm["comments"].([]any)
	require.Len(t, cs, 2)
	assert.Equal(t, "c-2", cs[1].(map[string]any)["id"])
	assert.Equal(t, "decision", cs[1].(map[string]any)["kind"])
	assert.Equal(t, "2026-10-03T10:00:00.000Z", cs[1].(map[string]any)["created"])
	assert.Len(t, rm["links"], 1)

	in, _ := p.Get("inbox", "IN-001")
	assert.EqualValues(t, 1, in["rev"], "a missing rev becomes 1, as the tracker's own migration does")

	// Ids allocated after an import continue the source's numbering.
	next, err := p.Create("decisions", service.PlannerItem{"title": "next"}, "t")
	require.NoError(t, err)
	assert.Equal(t, "DEC-002", next["id"])

	// Second run (with the new decision in Torque): everything from the source is unchanged.
	again, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, [4]int{0, 0, 1, 0}, importCounts(again)["roadmap"])
	assert.Equal(t, [4]int{0, 0, 1, 0}, importCounts(again)["decisions"])
	assert.True(t, again.Clean())
}

func TestPlannerImportRerunPolicy(t *testing.T) {
	p := plannerService(t)
	fx := trackerFixture()
	dir := writeTracker(t, fx)
	_, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true})
	require.NoError(t, err)

	// The source moves ahead: refreshed, comments replaced with the source's.
	fx["roadmap"][0]["rev"] = 4
	fx["roadmap"][0]["status"] = "in-progress"
	fx["roadmap"][0]["comments"] = append(fx["roadmap"][0]["comments"].([]any), map[string]any{"id": "c-3", "author": "pm", "text": "third", "created": "2026-10-09T10:00:00.000Z"})
	dir = writeTracker(t, fx)
	rep, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, [4]int{0, 1, 0, 0}, importCounts(rep)["roadmap"])
	assert.True(t, rep.Verified)
	rm, _ := p.Get("roadmap", "RM-a")
	assert.Equal(t, "in-progress", rm["status"])
	assert.Len(t, rm["comments"], 3)

	// Torque moves ahead of the source (an edit made in Torque): left alone and reported.
	_, err = p.Update("roadmap", "RM-a", service.PlannerItem{"status": "landed"}, nil)
	require.NoError(t, err)
	rep, err = p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true})
	require.NoError(t, err)
	require.Len(t, rep.Attention, 1)
	assert.Equal(t, service.PlannerImportNewer, rep.Attention[0].Action)
	assert.Contains(t, rep.Attention[0].Fields, "status")
	rm, _ = p.Get("roadmap", "RM-a")
	assert.Equal(t, "landed", rm["status"], "Torque's newer copy survives")

	// --force overwrites it.
	rep, err = p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true, Force: true})
	require.NoError(t, err)
	assert.True(t, rep.Verified)
	rm, _ = p.Get("roadmap", "RM-a")
	assert.Equal(t, "in-progress", rm["status"])

	// Same rev, different content: diverged, not touched.
	_, err = p.Update("roadmap", "RM-a", service.PlannerItem{"outcome": "x"}, nil) // rev 5 vs source 4 -> newer
	require.NoError(t, err)
	fx["roadmap"][0]["rev"] = 5
	fx["roadmap"][0]["outcome"] = "y"
	dir = writeTracker(t, fx)
	rep, err = p.ImportPlanner(service.PlannerImportOptions{Dir: dir})
	require.NoError(t, err)
	require.Len(t, rep.Attention, 1)
	assert.Equal(t, service.PlannerImportDiverged, rep.Attention[0].Action)
	assert.Contains(t, rep.Attention[0].Fields, "outcome")
}

func TestPlannerImportReportsBadInputWithoutBlockingGoodItems(t *testing.T) {
	p := plannerService(t)
	fx := trackerFixture()
	fx["risks"] = []map[string]any{
		{"id": "RK-001", "title": "ok", "kind": "risk", "severity": "low", "status": "open"},
		{"id": "RK-002", "title": "bad enum", "kind": "risk", "severity": "catastrophic", "status": "open"},
		{"id": "RK-001", "title": "dup in file", "kind": "risk", "severity": "low", "status": "open"},
		{"id": "DEC-001", "title": "id taken by a decision", "kind": "risk", "severity": "low", "status": "open"},
		{"title": "no id", "kind": "risk", "severity": "low", "status": "open"},
	}
	dir := writeTracker(t, fx)
	rep, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, [4]int{1, 0, 0, 4}, importCounts(rep)["risks"])
	assert.Len(t, rep.Attention, 4)
	assert.False(t, rep.Clean())
	_, err = p.Get("risks", "RK-001")
	require.NoError(t, err, "valid items still land")
	_, err = p.Get("risks", "RK-002")
	require.Error(t, err)
}

func TestPlannerImportRefusesWrongEnvelopeAndToleratesMissingFiles(t *testing.T) {
	p := plannerService(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "risks.json"), []byte(`{"schema":"portfolio/ideas@1","items":[]}`), 0o644))
	_, err := p.ImportPlanner(service.PlannerImportOptions{Dir: dir})
	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)

	empty := t.TempDir() // no files at all: every database reports "no file"
	rep, err := p.ImportPlanner(service.PlannerImportOptions{Dir: empty, Apply: true})
	require.NoError(t, err)
	assert.True(t, rep.Clean())
	for _, d := range rep.DBs {
		assert.NotEmpty(t, d.Note)
	}

	_, err = p.ImportPlanner(service.PlannerImportOptions{})
	require.Error(t, err)
}
