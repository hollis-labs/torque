package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func kindSet(t *testing.T) *Set {
	sets, err := LoadSets()
	require.NoError(t, err)
	return sets["task-kind"]
}

// answerFor builds a Choice answer that puts `conf` of the way from uniform to certain on `key`.
func answerFor(q Question, key string, conf float64) Answer {
	n := float64(len(q.Options))
	pmax := 1/n + conf*(1-1/n)
	probs := map[string]float64{}
	for _, o := range q.Options {
		probs[o.Key] = (1 - pmax) / (n - 1)
	}
	probs[key] = pmax
	return Answer{Type: Choice, Choice: key, Probabilities: probs, Confidence: ChoiceConfidence(probs)}
}

func TestEvaluateScoresAccuracyBandsAndTheSweep(t *testing.T) {
	set := kindSet(t)
	// Ten examples. The decider is right and confident on e0-e5, and wrong and unsure on e6-e9.
	var examples []Example
	for i := 0; i < 10; i++ {
		examples = append(examples, Example{ID: fmt.Sprintf("e%d", i), Fields: map[string]string{"title": fmt.Sprintf("e%d", i)}, Labels: map[string]Label{"kind": {Choice: "bug"}}})
	}
	d := Scripted{Label: "scripted", Fn: func(req Request) (Response, error) {
		var idx int
		fmt.Sscanf(req.State.(map[string]string)["title"], "e%d", &idx)
		q := req.Questions[0]
		a := answerFor(q, "bug", 0.9)
		if idx >= 6 {
			a = answerFor(q, "docs", 0.15)
		}
		return Response{Model: "m", Answers: map[string]Answer{"kind": a}, Usage: Usage{InputTokens: 100, OutputTokens: 2}}, nil
	}}
	rep, err := Evaluate(context.Background(), set, d, examples, EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	require.Len(t, rep.Questions, 1)
	q := rep.Questions[0]
	assert.Equal(t, 10, q.Labeled)
	assert.Equal(t, 6, q.Correct)
	assert.InDelta(t, 0.6, q.Accuracy, 1e-9)

	// 6 confident-and-right land in the top band, 4 unsure-and-wrong in the lowest.
	assert.Equal(t, 6, q.Bands[4].N)
	assert.Equal(t, 6, q.Bands[4].Correct)
	assert.Equal(t, 4, q.Bands[0].N)
	assert.Equal(t, 0, q.Bands[0].Correct)
	assert.InDelta(t, 1.0, q.Bands[4].Accuracy(), 1e-9)

	// Acting only on confident answers: covers 60%, all right. Acting on everything: 100% covered, 60% right.
	sweep := map[float64]SweepPoint{}
	for _, p := range q.Sweep {
		sweep[p.Threshold] = p
	}
	assert.InDelta(t, 1.0, sweep[0.0].Coverage, 1e-9)
	assert.InDelta(t, 0.6, sweep[0.0].Accuracy, 1e-9)
	assert.InDelta(t, 0.6, sweep[0.5].Coverage, 1e-9)
	assert.InDelta(t, 1.0, sweep[0.5].Accuracy, 1e-9)

	assert.Len(t, q.Disagreements, 4)
	assert.Equal(t, "docs", q.Disagreements[0].Predicted)
	assert.Equal(t, "bug", q.Disagreements[0].Label)

	assert.Equal(t, 10, rep.Calls)
	assert.Equal(t, 1000, rep.InputTokens)
	assert.Positive(t, rep.EstimatedTokens)
	assert.Equal(t, "m", rep.Model)
	assert.NotEmpty(t, rep.SetHash)
}

func TestEvaluateMeasuresOptionOrderFlips(t *testing.T) {
	set := kindSet(t)
	var examples []Example
	for i := 0; i < 20; i++ {
		examples = append(examples, Example{ID: fmt.Sprint(i), Labels: map[string]Label{"kind": {Choice: "bug"}}})
	}
	// A decider that always answers with the FIRST option it was shown: pure position bias.
	biased := Scripted{Fn: func(req Request) (Response, error) {
		q := req.Questions[0]
		return Response{Answers: map[string]Answer{"kind": answerFor(q, q.Options[0].Key, 0.9)}}, nil
	}}
	rep, err := Evaluate(context.Background(), set, biased, examples, EvalOptions{Seeds: []int64{1, 2, 3}})
	require.NoError(t, err)
	assert.Greater(t, rep.Questions[0].FlipRate, 0.5, "an answer that follows the option order flips when the order does")

	// A decider that ignores position never flips.
	steady := Scripted{Fn: func(req Request) (Response, error) {
		return Response{Answers: map[string]Answer{"kind": answerFor(req.Questions[0], "bug", 0.9)}}, nil
	}}
	rep, err = Evaluate(context.Background(), set, steady, examples, EvalOptions{Seeds: []int64{1, 2, 3}})
	require.NoError(t, err)
	assert.Equal(t, 0, rep.Questions[0].Flips)
	assert.Equal(t, 1.0, rep.Questions[0].Accuracy)
}

func TestEvaluateCountsErrorsAndSkipsThoseExamples(t *testing.T) {
	set := kindSet(t)
	examples := []Example{
		{ID: "ok", Fields: map[string]string{"title": "ok"}, Labels: map[string]Label{"kind": {Choice: "bug"}}},
		{ID: "boom", Fields: map[string]string{"title": "boom"}, Labels: map[string]Label{"kind": {Choice: "bug"}}},
	}
	d := Scripted{Fn: func(req Request) (Response, error) {
		if req.State.(map[string]string)["title"] == "boom" {
			return Response{}, errors.New("429")
		}
		return Response{Answers: map[string]Answer{"kind": answerFor(req.Questions[0], "bug", 0.9)}}, nil
	}}
	rep, err := Evaluate(context.Background(), set, d, examples, EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Errors)
	assert.Equal(t, 1, rep.Questions[0].Labeled, "a failed example is not scored as wrong")
}

func TestEvaluateScoresNoulsByYesNoAndConfidence(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-layer"]
	examples := []Example{
		{ID: "a", Labels: map[string]Label{"frontend": {Yes: true}, "backend": {Yes: false}}},
		{ID: "b", Labels: map[string]Label{"frontend": {Yes: false}, "backend": {Yes: true}}},
	}
	d := Scripted{Fn: func(req Request) (Response, error) {
		// Always: frontend yes (0.9), backend no (0.1).
		return Response{Answers: map[string]Answer{
			"frontend": {Type: Noul, Noul: 0.9},
			"backend":  {Type: Noul, Noul: 0.1},
		}}, nil
	}}
	rep, err := Evaluate(context.Background(), set, d, examples, EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	byKey := map[string]QuestionReport{}
	for _, q := range rep.Questions {
		byKey[q.Key] = q
	}
	assert.Equal(t, 1, byKey["frontend"].Positives)
	assert.Equal(t, 1, byKey["frontend"].Negatives)
	assert.Equal(t, 1, byKey["frontend"].Correct, "right on a, wrong on b")
	assert.Equal(t, 1, byKey["backend"].Correct)
	assert.InDelta(t, 0.8, byKey["frontend"].MeanConf, 1e-9, "noul confidence is |2p-1|")
	assert.Equal(t, "yes", byKey["frontend"].Disagreements[0].Predicted)
	assert.Equal(t, "no", byKey["frontend"].Disagreements[0].Label)
}

func TestEvaluateOnlyScoresQuestionsThatHaveALabel(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-layer"]
	examples := []Example{{ID: "a", Labels: map[string]Label{"frontend": {Yes: true}}}}
	rep, err := Evaluate(context.Background(), set, Baseline{}, examples, EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	byKey := map[string]QuestionReport{}
	for _, q := range rep.Questions {
		byKey[q.Key] = q
	}
	assert.Equal(t, 1, byKey["frontend"].Labeled)
	assert.Equal(t, 0, byKey["backend"].Labeled)
	assert.Contains(t, rep.Text(), "no labels for this question")
}

func TestReportTextIsReadable(t *testing.T) {
	set := kindSet(t)
	examples := []Example{{ID: "t1", Fields: map[string]string{"title": "Fix the bug", "description": "broken"}, Labels: map[string]Label{"kind": {Choice: "bug"}}}}
	rep, err := Evaluate(context.Background(), set, Baseline{}, examples, EvalOptions{})
	require.NoError(t, err)
	text := rep.Text()
	for _, want := range []string{"Set task-kind v1", "keyword-baseline", "kind (choice)", "confidence band", "act above", "option-order flips"} {
		assert.Contains(t, text, want)
	}
	assert.False(t, strings.Contains(text, "NaN"))
}

func TestEvaluateStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Evaluate(ctx, kindSet(t), Baseline{}, []Example{{ID: "a"}}, EvalOptions{})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReportWarnsWhenAYesNoQuestionHasTooFewPositives(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-layer"]
	var examples []Example
	for i := 0; i < 40; i++ {
		examples = append(examples, Example{ID: fmt.Sprint(i), Labels: map[string]Label{"frontend": {Yes: i < 3}}}) // 3 positives, 37 negatives
	}
	always := Scripted{Fn: func(Request) (Response, error) {
		return Response{Answers: map[string]Answer{"frontend": {Type: Noul, Noul: 0.01}, "backend": {Type: Noul, Noul: 0.01}}}, nil
	}}
	rep, err := Evaluate(context.Background(), set, always, examples, EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	q := rep.Questions[0]
	assert.Equal(t, 3, q.Positives)
	assert.Equal(t, 37, q.Negatives)
	assert.InDelta(t, 0.925, q.Accuracy, 1e-9, "an answer that is always no scores 92.5% here, which is exactly the trap")
	text := rep.Text()
	assert.Contains(t, text, "3 positive / 37 negative labels")
	assert.Contains(t, text, "WARNING: only 3 positive labels")

	// With enough positives the warning goes away.
	for i := 3; i < 25; i++ {
		examples[i].Labels["frontend"] = Label{Yes: true}
	}
	rep, _ = Evaluate(context.Background(), set, always, examples, EvalOptions{Seeds: []int64{1}})
	assert.NotContains(t, rep.Text(), "WARNING: only")
}

func labeledExamples(n int) []Example {
	var out []Example
	for i := 0; i < n; i++ {
		out = append(out, Example{ID: fmt.Sprintf("e%03d", i), Fields: map[string]string{"title": fmt.Sprintf("e%d", i)}, Labels: map[string]Label{"kind": {Choice: "bug"}}})
	}
	return out
}

// mixedDecider is right on every third example and uses a little time, so a
// concurrent run interleaves.
func mixedDecider() Scripted {
	return Scripted{Label: "mixed", Fn: func(req Request) (Response, error) {
		var idx int
		fmt.Sscanf(req.State.(map[string]string)["title"], "e%d", &idx)
		key := "bug"
		if idx%3 == 0 {
			key = "docs"
		}
		return Response{Model: "m", Usage: Usage{InputTokens: 100, OutputTokens: 1}, Answers: map[string]Answer{"kind": answerFor(req.Questions[0], key, 0.2+float64(idx%7)/10)}}, nil
	}}
}

func TestConcurrencyDoesNotChangeTheReport(t *testing.T) {
	set := kindSet(t)
	ex := labeledExamples(60)
	one, err := Evaluate(context.Background(), set, mixedDecider(), ex, EvalOptions{Seeds: []int64{1, 2}, Concurrency: 1})
	require.NoError(t, err)
	many, err := Evaluate(context.Background(), set, mixedDecider(), ex, EvalOptions{Seeds: []int64{1, 2}, Concurrency: 8})
	require.NoError(t, err)
	one.Latency, many.Latency = 0, 0
	assert.Equal(t, one, many, "the same report at any concurrency")
	assert.Equal(t, 120, many.Calls)
}

func TestMaxCallsTruncatesAndSaysSo(t *testing.T) {
	set := kindSet(t)
	rep, err := Evaluate(context.Background(), set, mixedDecider(), labeledExamples(50), EvalOptions{Seeds: []int64{1, 2}, MaxCalls: 20})
	require.NoError(t, err)
	assert.LessOrEqual(t, rep.Calls, 20, "never spends past the cap")
	assert.Equal(t, 10, rep.Calls/2, "ten examples fit under twenty calls of two seeds each")
	assert.True(t, rep.Truncated)
	assert.Equal(t, 40, rep.NotRun)
	assert.Equal(t, 10, rep.Questions[0].Labeled, "only the examples that ran are scored")
	assert.Contains(t, rep.Text(), "TRUNCATED")
}

func TestMaxCostStopsTheRun(t *testing.T) {
	set := kindSet(t)
	// 100 tokens a call at $0.042/M is $0.0000042; a $0.00002 cap allows about five calls.
	rep, err := Evaluate(context.Background(), set, mixedDecider(), labeledExamples(50), EvalOptions{Seeds: []int64{1}, MaxCostUSD: 0.00002})
	require.NoError(t, err)
	assert.True(t, rep.Truncated)
	assert.GreaterOrEqual(t, rep.Calls, 5)
	assert.Less(t, rep.Calls, 50)
	assert.InDelta(t, CostUSD(rep.InputTokens), rep.CostUSD, 1e-12)
}

func TestNoLimitsRunsEverythingAndReportsCost(t *testing.T) {
	rep, err := Evaluate(context.Background(), kindSet(t), mixedDecider(), labeledExamples(10), EvalOptions{Seeds: []int64{1}})
	require.NoError(t, err)
	assert.False(t, rep.Truncated)
	assert.Equal(t, 0, rep.NotRun)
	assert.Equal(t, 1000, rep.InputTokens)
	assert.InDelta(t, 0.000042, rep.CostUSD, 1e-9)
	assert.Contains(t, rep.Text(), "input cost $0.0000")
	assert.NotContains(t, rep.Text(), "TRUNCATED")
}

func TestEstimateEvalMatchesWhatTheRunSends(t *testing.T) {
	set := kindSet(t)
	ex := labeledExamples(25)
	est := EstimateEval(set, ex, EvalOptions{Seeds: []int64{1, 2}})
	assert.Equal(t, 25, est.Examples)
	assert.Equal(t, 2, est.CallsPerExample)
	assert.Equal(t, 50, est.Calls)
	assert.Greater(t, est.EstimatedTokens, 50*RequestOverheadTokens, "the provider's fixed overhead is counted on every call")
	assert.InDelta(t, CostUSD(est.EstimatedTokens), est.EstimatedUSD, 1e-12)

	// The Baseline reports the same estimate as its usage, so a real run's estimate field must equal this.
	rep, err := Evaluate(context.Background(), set, Baseline{}, ex, EvalOptions{Seeds: []int64{1, 2}})
	require.NoError(t, err)
	assert.Equal(t, est.EstimatedTokens, rep.EstimatedTokens)
	assert.Equal(t, est.Calls, rep.Calls)
}

func TestEstimateDefaultsToTwoAsksPerExample(t *testing.T) {
	assert.Equal(t, 2, EstimateEval(kindSet(t), labeledExamples(1), EvalOptions{}).CallsPerExample)
}
