package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boardTask creates a task in a status with tags, then backdates it.
func boardTask(t *testing.T, svc *service.Service, title, status string, hoursAgo float64, priority int, tags ...string) string {
	t.Helper()
	task, err := svc.Task.Create(service.TaskCreateInput{Title: title, Status: status, Priority: priority, Tags: tags})
	require.NoError(t, err)
	at := time.Now().UTC().Add(-time.Duration(hoursAgo * float64(time.Hour))).Format("2006-01-02 15:04:05")
	_, err = svc.Store().DB().Exec(`UPDATE tasks SET updated_at = ? WHERE id = ?`, at, task.ID)
	require.NoError(t, err)
	return task.ID
}

func boardIDs(rows []service.PlannerBoardRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestPlannerBoardFromTheTaskStoreAndPlannerTables(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	p := svc.Planner

	active := boardTask(t, svc, "Worked on now", "doing", 5, 2, "ws-alpha")
	idle := boardTask(t, svc, "Started, quiet", "doing", 5, 2)
	review := boardTask(t, svc, "Awaiting review", "review", 1, 3)
	queued := boardTask(t, svc, "Next up", "queued", 2, 1)
	landed := boardTask(t, svc, "Just finished", "done", 1, 3)
	oldDone := boardTask(t, svc, "Finished long ago", "done", 24*30, 3)
	backlog := boardTask(t, svc, "Someday", "todo", 1, 3)

	// A comment 10 minutes ago keeps the quiet-looking task in flight: Torque does not bump updated_at on a comment.
	_, err := svc.Store().DB().Exec(`INSERT INTO comments (entity_type, entity_id, author, content, created_at) VALUES ('task', ?, 'a', 'progress', ?)`,
		active, time.Now().UTC().Add(-10*time.Minute).Format("2006-01-02 15:04:05"))
	require.NoError(t, err)

	_, err = p.Create("decisions", service.PlannerItem{"title": "Pick one", "status": "needs-decision"}, "t")
	require.NoError(t, err)
	_, err = p.Create("inbox", service.PlannerItem{"title": "Look at this"}, "t")
	require.NoError(t, err)
	_, err = p.Create("risks", service.PlannerItem{"title": "Big risk", "kind": "risk", "severity": "high"}, "t")
	require.NoError(t, err)
	_, err = p.Create("roadmap", service.PlannerItem{"title": "Building", "area": "gui", "horizon": "now", "status": "in-progress"}, "t")
	require.NoError(t, err)

	b, err := p.Board(service.PlannerBoardOptions{})
	require.NoError(t, err)
	assert.Empty(t, b.Notices)

	assert.Equal(t, []string{active, "RM-building", review}, boardIDs(b.Sections["in_flight"]))
	assert.Equal(t, "alpha", b.Sections["in_flight"][0].Workstream, "the ws-<name> tag names the workstream")
	assert.Equal(t, 2, b.Sections["in_flight"][0].Priority)
	assert.Equal(t, []string{idle, queued}, boardIDs(b.Sections["pre_flight"]), "a quiet doing task is preparing; queued follows")
	assert.Equal(t, []string{landed}, boardIDs(b.Sections["landed"]))
	assert.Equal(t, []string{"DEC-001", "RK-001", "IN-001"}, boardIDs(b.Sections["holds"]))

	all := boardIDs(b.Sections["in_flight"])
	for _, k := range []string{"landed", "pre_flight", "holds"} {
		all = append(all, boardIDs(b.Sections[k])...)
	}
	assert.NotContains(t, all, oldDone, "outside the recent window")
	assert.NotContains(t, all, backlog, "the todo backlog is never listed")

	assert.Equal(t, 3, b.Counts["in_flight"])
	assert.Equal(t, 3, b.Totals["holds"])
	assert.NotEmpty(t, b.GeneratedAt)
	assert.Contains(t, service.BoardText(*b), "PREPARING")
}

func TestPlannerBoardLimitsAndValidation(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	for i := 0; i < 5; i++ {
		boardTask(t, svc, "Doing", "doing", float64(i), 3)
	}
	b, err := svc.Planner.Board(service.PlannerBoardOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, b.Sections["in_flight"], 2)
	assert.Equal(t, 5, b.Totals["in_flight"], "the total counts rows beyond the limit")

	for name, opts := range map[string]service.PlannerBoardOptions{
		"limit":        {Limit: 500},
		"recent_hours": {RecentHours: -1},
		"active_hours": {ActiveHours: 100000},
	} {
		_, err := svc.Planner.Board(opts)
		var ve *service.ValidationError
		require.True(t, errors.As(err, &ve), name)
		assert.Equal(t, name, ve.Field)
	}
}

func TestPlannerBoardRequiresFeature(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Planner.Board(service.PlannerBoardOptions{})
	var fe *service.FeatureDisabledError
	require.True(t, errors.As(err, &fe))
}

func TestPlannerTaskTitles(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	id := boardTask(t, svc, "Resolve me", "doing", 1, 2)

	got, err := svc.Planner.TaskTitles([]string{id, "CW-20200101-9999"})
	require.NoError(t, err)
	require.NotNil(t, got[id])
	assert.Equal(t, "Resolve me", got[id].Title)
	assert.Equal(t, "doing", got[id].Status)
	assert.Equal(t, 2, got[id].Priority)
	assert.Nil(t, got["CW-20200101-9999"], "an unknown id is nil, not an error")
	assert.Contains(t, got, "CW-20200101-9999")

	_, err = svc.Planner.TaskTitles([]string{"not-a-task-id"})
	var ve *service.ValidationError
	require.True(t, errors.As(err, &ve))
	many := make([]string, 101)
	for i := range many {
		many[i] = id
	}
	_, err = svc.Planner.TaskTitles(many)
	require.True(t, errors.As(err, &ve))
}

func TestPlannerWorkstreamTasks(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("planner"))
	require.NoError(t, svc.Feature.Enable("projects"))
	proj, err := svc.Project.Create(service.ProjectCreateInput{Name: "Torque", RepoPath: t.TempDir()})
	require.NoError(t, err)

	mk := func(title, status string, hoursAgo float64, projectID string, tags ...string) string {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: title, Status: status, Tags: tags, ProjectID: projectID})
		require.NoError(t, err)
		at := time.Now().UTC().Add(-time.Duration(hoursAgo * float64(time.Hour))).Format("2006-01-02 15:04:05")
		_, err = svc.Store().DB().Exec(`UPDATE tasks SET updated_at = ? WHERE id = ?`, at, task.ID)
		require.NoError(t, err)
		return task.ID
	}
	a := mk("In project, doing", "doing", 1, proj.ID)
	b := mk("In project, queued", "queued", 3, proj.ID)
	mk("In project, done", "done", 2, proj.ID)
	mk("Elsewhere", "doing", 1, "")
	tagged := mk("Tagged only", "review", 2, "", "ws-alpha")

	_, err = svc.Planner.Create("workstreams", service.PlannerItem{"id": "WS-proj", "title": "By project", "torque_project_ids": []any{proj.ID}}, "t")
	require.NoError(t, err)
	_, err = svc.Planner.Create("workstreams", service.PlannerItem{"id": "WS-tag", "title": "By tag", "torque_tag": "ws-alpha"}, "t")
	require.NoError(t, err)
	_, err = svc.Planner.Create("workstreams", service.PlannerItem{"id": "WS-both", "title": "Both", "torque_project_ids": []any{proj.ID}, "torque_tag": "ws-alpha"}, "t")
	require.NoError(t, err)
	_, err = svc.Planner.Create("workstreams", service.PlannerItem{"id": "WS-none", "title": "No scope"}, "t")
	require.NoError(t, err)

	byProject, err := svc.Planner.WorkstreamTasks("WS-proj")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"doing": 1, "queued": 1, "done": 1}, byProject.Counts)
	assert.Equal(t, 3, byProject.Total)
	require.Len(t, byProject.Open, 2, "done is not open")
	assert.Equal(t, a, byProject.Open[0].ID, "newest first")
	assert.Equal(t, b, byProject.Open[1].ID)
	assert.Equal(t, 2, byProject.OpenTotal)

	byTag, err := svc.Planner.WorkstreamTasks("WS-tag")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"review": 1}, byTag.Counts)
	require.Len(t, byTag.Open, 1)
	assert.Equal(t, tagged, byTag.Open[0].ID)

	both, err := svc.Planner.WorkstreamTasks("WS-both")
	require.NoError(t, err)
	assert.Empty(t, both.Open, "a project and a tag narrow each other: no task is in the project and tagged")

	none, err := svc.Planner.WorkstreamTasks("WS-none")
	require.NoError(t, err)
	assert.Equal(t, 0, none.Total)
	assert.NotEmpty(t, none.Note)
	assert.NotNil(t, none.Open)

	_, err = svc.Planner.WorkstreamTasks("WS-missing")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
	_, err = svc.Planner.WorkstreamTasks("PR-01")
	require.Error(t, err, "only workstreams have Torque scope")
}
