package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgeTask(t *testing.T, svc *service.Service, title, description string, tags ...string) string {
	t.Helper()
	task, err := svc.Task.Create(service.TaskCreateInput{Title: title, Description: description, Tags: tags})
	require.NoError(t, err)
	return task.ID
}

func TestJudgeServiceListsEmbeddedSets(t *testing.T) {
	svc := setupService(t)
	sets, err := svc.Judge.Sets()
	require.NoError(t, err)
	assert.Contains(t, sets, "task-kind")
	_, err = svc.Judge.Set("nope")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestTaskCorpusCarriesTextAndTagsAndLeavesOutInternalTasks(t *testing.T) {
	svc := setupService(t)
	a := judgeTask(t, svc, "Fix the crash", "It is broken.", "bug", "backend")
	judgeTask(t, svc, "No tags", "text")
	items, err := svc.Judge.TaskItems()
	require.NoError(t, err)
	byID := map[string]judge.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	assert.Equal(t, "Fix the crash", byID[a].Fields["title"])
	assert.Equal(t, "It is broken.", byID[a].Fields["description"])
	assert.ElementsMatch(t, []string{"bug", "backend"}, byID[a].Tags)
	assert.Len(t, items, 2)
}

func TestEvaluateRunsASetAgainstTheTagsTasksCarry(t *testing.T) {
	svc := setupService(t)
	for i := 0; i < 6; i++ {
		judgeTask(t, svc, fmt.Sprintf("Fix crash %d", i), "This is broken and must be fixed: a bug.", "bug")
	}
	for i := 0; i < 6; i++ {
		judgeTask(t, svc, fmt.Sprintf("Write the guide %d", i), "Documentation only: write the docs and README.", "docs")
	}
	rep, err := svc.Judge.Evaluate(context.Background(), "task-kind", judge.Baseline{}, service.JudgeEvalOptions{N: 50, Seed: 1})
	require.NoError(t, err)
	require.Len(t, rep.Questions, 1)
	q := rep.Questions[0]
	assert.Equal(t, 12, q.Labeled)
	assert.Greater(t, q.Accuracy, 0.9, "the baseline gets this blunt fixture right")
	assert.Equal(t, 24, rep.Calls, "each example asked twice, for the option-order check")

	noFlips, err := svc.Judge.Evaluate(context.Background(), "task-kind", judge.Baseline{}, service.JudgeEvalOptions{N: 50, NoFlips: true})
	require.NoError(t, err)
	assert.Equal(t, 12, noFlips.Calls)

	_, err = svc.Judge.Evaluate(context.Background(), "nope", judge.Baseline{}, service.JudgeEvalOptions{})
	require.Error(t, err)
}

func TestEnrichTaskRecordsSuggestionsOnceAndNeverSuggestsTagsThatExist(t *testing.T) {
	svc := setupService(t)
	id := judgeTask(t, svc, "Fix the crash on start", "It is broken: a bug. This must be fixed.", "backend")

	// The decider is sure it is a bug.
	d := judge.Scripted{Label: "scripted", Fn: func(req judge.Request) (judge.Response, error) {
		return judge.Response{Model: "m-1", Usage: judge.Usage{InputTokens: 80, OutputTokens: 2}, Answers: map[string]judge.Answer{
			"kind": {Type: judge.Choice, Choice: "bug", Probabilities: map[string]float64{"bug": 0.9, "docs": 0.1}, Confidence: 0.8},
		}}, nil
	}}

	// A dry run records nothing.
	dry, err := svc.Judge.EnrichTask(context.Background(), d, "task-kind", id, false)
	require.NoError(t, err)
	require.Len(t, dry, 1)
	assert.Equal(t, []string{"bug"}, dry[0].Suggested)
	none, _ := svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id})
	assert.Empty(t, none)

	recs, err := svc.Judge.EnrichTask(context.Background(), d, "task-kind", id, true)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	again, err := svc.Judge.EnrichTask(context.Background(), d, "task-kind", id, true)
	require.NoError(t, err)
	require.Len(t, again, 1)
	rows, err := svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id})
	require.NoError(t, err)
	require.Len(t, rows, 1, "the same state and wording are one row")
	assert.Equal(t, "scripted", rows[0].Provider)
	assert.Equal(t, "m-1", rows[0].Model)
	assert.Equal(t, 80, rows[0].InputTokens)
	assert.Equal(t, "task_enrichment", rows[0].Purpose)
	assert.Equal(t, sqlstore.JudgmentSuggested, rows[0].Disposition)

	// A task that already carries the tag gets no suggestion for it.
	has := judgeTask(t, svc, "Fix it", "bug broken", "bug")
	recs, err = svc.Judge.EnrichTask(context.Background(), d, "task-kind", has, false)
	require.NoError(t, err)
	assert.Empty(t, recs[0].Suggested)

	// Editing the task changes the state: the earlier suggestion is superseded.
	err = svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Description: ptr("Now it is a feature request.")}})
	require.NoError(t, err)
	_, err = svc.Judge.EnrichTask(context.Background(), d, "task-kind", id, true)
	require.NoError(t, err)
	rows, _ = svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id})
	require.Len(t, rows, 2)
	assert.Equal(t, sqlstore.JudgmentSuggested, rows[0].Disposition)
	assert.Equal(t, sqlstore.JudgmentSuperseded, rows[1].Disposition)
}

func TestEnrichTaskScoresNoulConfidenceAndReportsErrors(t *testing.T) {
	svc := setupService(t)
	id := judgeTask(t, svc, "Style the page", "Change the components and CSS.")
	d := judge.Scripted{Fn: func(req judge.Request) (judge.Response, error) {
		return judge.Response{Answers: map[string]judge.Answer{
			"frontend": {Type: judge.Noul, Noul: 0.92},
			"backend":  {Type: judge.Noul, Noul: 0.04},
		}}, nil
	}}
	recs, err := svc.Judge.EnrichTask(context.Background(), d, "task-layer", id, true)
	require.NoError(t, err)
	byKey := map[string]sqlstore.JudgmentRecord{}
	for _, r := range recs {
		byKey[r.QuestionKey] = r
	}
	assert.Equal(t, []string{"frontend"}, byKey["frontend"].Suggested)
	assert.InDelta(t, 0.84, byKey["frontend"].Confidence, 1e-9, "a noul's confidence is |2p-1|")
	assert.Empty(t, byKey["backend"].Suggested, "a confident no suggests nothing")

	boom := judge.Scripted{Fn: func(judge.Request) (judge.Response, error) { return judge.Response{}, errors.New("529 overloaded") }}
	_, err = svc.Judge.EnrichTask(context.Background(), boom, "task-layer", id, true)
	require.Error(t, err)
	rows, _ := svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id, SetID: "task-layer"})
	assert.Len(t, rows, 2, "a failed ask records nothing new")

	_, err = svc.Judge.EnrichTask(context.Background(), d, "task-layer", "CW-20200101-9999", true)
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestDecidingASuggestion(t *testing.T) {
	svc := setupService(t)
	id := judgeTask(t, svc, "x", "bug broken")
	d := judge.Scripted{Fn: func(req judge.Request) (judge.Response, error) {
		return judge.Response{Answers: map[string]judge.Answer{"kind": {Type: judge.Choice, Choice: "bug", Probabilities: map[string]float64{"bug": 1}, Confidence: 1}}}, nil
	}}
	recs, _ := svc.Judge.EnrichTask(context.Background(), d, "task-kind", id, true)
	rows, _ := svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id})
	require.Len(t, rows, 1)
	_ = recs

	require.NoError(t, svc.Judge.Decide(rows[0].ID, true, "chrispian"))
	err := svc.Judge.Decide(rows[0].ID, false, "chrispian")
	var ce *service.ConflictError
	require.True(t, errors.As(err, &ce), "a decided suggestion cannot be decided again")
	err = svc.Judge.Decide(9999, true, "x")
	var nf *service.NotFoundError
	require.True(t, errors.As(err, &nf))
}

func ptr[T any](v T) *T { return &v }
