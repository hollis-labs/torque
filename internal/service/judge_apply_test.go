package service_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindDecider answers every task-kind question with a fixed choice and confidence.
func kindDecider(choice string, confidence float64, calls *atomic.Int32) judge.Decider {
	return judge.Scripted{Label: "scripted", Fn: func(req judge.Request) (judge.Response, error) {
		if calls != nil {
			calls.Add(1)
		}
		answers := map[string]judge.Answer{}
		for _, q := range req.Questions {
			switch q.Key {
			case "kind":
				answers[q.Key] = judge.Answer{Type: judge.Choice, Choice: choice, Probabilities: map[string]float64{choice: confidence}, Confidence: confidence}
			default:
				answers[q.Key] = judge.Answer{Type: judge.Noul, Noul: 0.5}
			}
		}
		return judge.Response{Model: "m", Answers: answers}, nil
	}}
}

func tagsOf(t *testing.T, svc *service.Service, id string) []string {
	t.Helper()
	tags, err := svc.Task.ListTags(id)
	require.NoError(t, err)
	var out []string
	for _, tg := range tags {
		out = append(out, tg.Slug)
	}
	return out
}

func TestAcceptAddsTheTagsAndKeepsTheOnesTheTaskHas(t *testing.T) {
	svc := setupService(t)
	id := judgeTask(t, svc, "Fix the crash", "broken, must be fixed", "backend")
	_, err := svc.Judge.EnrichTask(context.Background(), kindDecider("bug", 0.9, nil), "task-kind", id, true)
	require.NoError(t, err)

	open, err := svc.Judge.Suggestions(service.SuggestionFilter{TaskID: id})
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, "Fix the crash", open[0].TaskTitle)

	added, err := svc.Judge.Accept(open[0].ID, "chrispian")
	require.NoError(t, err)
	assert.Equal(t, []string{"bug"}, added)
	assert.ElementsMatch(t, []string{"backend", "bug"}, tagsOf(t, svc, id))

	rec, err := svc.Judge.Judgments(sqlstore.JudgmentFilter{SubjectID: id})
	require.NoError(t, err)
	assert.Equal(t, sqlstore.JudgmentAccepted, rec[0].Disposition)
	assert.Equal(t, "chrispian", rec[0].DecidedBy)

	_, err = svc.Judge.Accept(open[0].ID, "chrispian")
	var ce *service.ConflictError
	assert.ErrorAs(t, err, &ce, "a decided suggestion cannot be accepted again")
}

func TestRejectLeavesTheTaskAloneAndAcceptedRowsLeaveTheList(t *testing.T) {
	svc := setupService(t)
	id := judgeTask(t, svc, "Add dark mode", "a new capability")
	_, err := svc.Judge.EnrichTask(context.Background(), kindDecider("feature", 0.95, nil), "task-kind", id, true)
	require.NoError(t, err)
	open, _ := svc.Judge.Suggestions(service.SuggestionFilter{})
	require.Len(t, open, 1)
	require.NoError(t, svc.Judge.Reject(open[0].ID, "chrispian"))
	assert.Empty(t, tagsOf(t, svc, id))
	open, _ = svc.Judge.Suggestions(service.SuggestionFilter{})
	assert.Empty(t, open)
}

func TestAQuestionAPersonAlreadyAnsweredGetsNoSuggestion(t *testing.T) {
	svc := setupService(t)
	// Already a feature: a "bug" answer must not add a second kind tag.
	id := judgeTask(t, svc, "Thing", "text", "feature")
	recs, err := svc.Judge.EnrichTask(context.Background(), kindDecider("bug", 0.99, nil), "task-kind", id, true)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Empty(t, recs[0].Suggested)
	open, _ := svc.Judge.Suggestions(service.SuggestionFilter{})
	assert.Empty(t, open, "an answer that proposes nothing is not a suggestion")
}

func TestBulkDecideByConfidenceBandNeedsALimit(t *testing.T) {
	svc := setupService(t)
	high := judgeTask(t, svc, "High", "h")
	mid := judgeTask(t, svc, "Mid", "m")
	_, err := svc.Judge.EnrichTask(context.Background(), kindDecider("bug", 0.95, nil), "task-kind", high, true)
	require.NoError(t, err)
	_, err = svc.Judge.EnrichTask(context.Background(), kindDecider("bug", 0.85, nil), "task-kind", mid, true)
	require.NoError(t, err)

	_, err = svc.Judge.BulkDecide(service.SuggestionFilter{MinConfidence: 0.9}, true, "chrispian")
	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve, "no limit, no sweep")

	res, err := svc.Judge.BulkDecide(service.SuggestionFilter{MinConfidence: 0.9, Limit: 10}, true, "chrispian")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Decided)
	assert.Equal(t, []string{"bug"}, tagsOf(t, svc, high))
	assert.Empty(t, tagsOf(t, svc, mid), "below the band: untouched")
	left, _ := svc.Judge.Suggestions(service.SuggestionFilter{})
	require.Len(t, left, 1)
	assert.Equal(t, mid, left[0].SubjectID)
}

func TestBackfillAsksOnlyOpenTasksWithoutTheSetsTagsAndOnlyOnce(t *testing.T) {
	svc := setupService(t)
	untagged := judgeTask(t, svc, "Untagged", "x")
	tagged := judgeTask(t, svc, "Tagged", "x", "bug")
	done := judgeTask(t, svc, "Done one", "x")
	require.NoError(t, svc.Task.Transition(context.Background(), done, "done"))

	var calls atomic.Int32
	opts := service.BackfillOptions{SetID: "task-kind", Concurrency: 1}
	plan, err := svc.Judge.PlanBackfill(opts)
	require.NoError(t, err)
	assert.Equal(t, 1, plan.Candidates)

	res, err := svc.Judge.Backfill(context.Background(), kindDecider("chore", 0.9, &calls), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Asked)
	assert.Equal(t, 1, res.Suggestions)
	assert.EqualValues(t, 1, calls.Load())
	open, _ := svc.Judge.Suggestions(service.SuggestionFilter{})
	require.Len(t, open, 1)
	assert.Equal(t, untagged, open[0].SubjectID)
	_ = tagged

	res, err = svc.Judge.Backfill(context.Background(), kindDecider("chore", 0.9, &calls), opts)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Asked, "already judged by this wording")
	assert.EqualValues(t, 1, calls.Load())
}

func TestBackfillStopsAtTheCostCap(t *testing.T) {
	svc := setupService(t)
	for i := 0; i < 5; i++ {
		judgeTask(t, svc, "Task", "some text")
	}
	var calls atomic.Int32
	res, err := svc.Judge.Backfill(context.Background(), kindDecider("chore", 0.9, &calls), service.BackfillOptions{SetID: "task-kind", Concurrency: 1, MaxCostUSD: 0.00002})
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Less(t, int(calls.Load()), 5)
}

func TestEnrichOnCreateNeedsTheFeatureAndADeciderAndNeverBlocksCreate(t *testing.T) {
	svc := setupService(t)
	var calls atomic.Int32
	svc.Judge.SetDecider(kindDecider("bug", 0.9, &calls))

	judgeTask(t, svc, "Before the feature is on", "x")
	time.Sleep(100 * time.Millisecond)
	assert.EqualValues(t, 0, calls.Load(), "feature off: nothing asked")
	assert.False(t, svc.Judge.Status().Active)

	require.NoError(t, svc.Feature.Enable("judge"))
	assert.True(t, svc.Judge.Status().Active)
	id := judgeTask(t, svc, "After", "x")
	require.Eventually(t, func() bool {
		open, _ := svc.Judge.Suggestions(service.SuggestionFilter{TaskID: id})
		return len(open) == 1
	}, 3*time.Second, 20*time.Millisecond)

	// A failing decider does not fail the create.
	svc.Judge.SetDecider(judge.Scripted{Fn: func(judge.Request) (judge.Response, error) { return judge.Response{}, assert.AnError }})
	_, err := svc.Task.Create(service.TaskCreateInput{Title: "Still created", Description: "x"})
	require.NoError(t, err)
}
