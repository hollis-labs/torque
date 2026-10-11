package judge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaselineAnswersAndAgreesWithItself(t *testing.T) {
	sets, _ := LoadSets()
	kind := sets["task-kind"]
	req := kind.BuildRequest(map[string]string{
		"title":       "Fix crash when the config file is missing",
		"description": "The service is broken: it crashes on start if the file is missing. This bug must be fixed.",
	}, 1)
	resp, err := Baseline{}.Decide(context.Background(), req)
	require.NoError(t, err)
	a := resp.Answers["kind"]
	assert.Equal(t, "bug", a.Choice)
	sum := 0.0
	for _, p := range a.Probabilities {
		sum += p
	}
	assert.InDelta(t, 1.0, sum, 1e-9, "probabilities sum to one")
	assert.InDelta(t, ChoiceConfidence(a.Probabilities), a.Confidence, 1e-9)
	assert.Positive(t, resp.Usage.InputTokens)

	again, _ := Baseline{}.Decide(context.Background(), req)
	assert.Equal(t, resp.Answers, again.Answers, "deterministic")
}

func TestBaselineSaysOtherWhenNothingMatches(t *testing.T) {
	sets, _ := LoadSets()
	req := sets["task-kind"].BuildRequest(map[string]string{"title": "zzz", "description": "qqq"}, 1)
	resp, err := Baseline{}.Decide(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "other", resp.Answers["kind"].Choice)
}

func TestBaselineNoulMovesWithEvidence(t *testing.T) {
	sets, _ := LoadSets()
	s := sets["task-decision-state"]
	open := s.BuildRequest(map[string]string{"title": "x", "description": "An open question: an unresolved choice between approaches, discuss or decide first."}, 1)
	quiet := s.BuildRequest(map[string]string{"title": "x", "description": "Rename the variable."}, 1)
	a, _ := Baseline{}.Decide(context.Background(), open)
	b, _ := Baseline{}.Decide(context.Background(), quiet)
	assert.Greater(t, a.Answers["discuss_first"].Noul, b.Answers["discuss_first"].Noul)
	assert.Less(t, b.Answers["discuss_first"].Noul, 0.5)
}
