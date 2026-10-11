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
