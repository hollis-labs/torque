package judge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The examples are the ones printed on docs.typesafe.ai/confidence.
func TestChoiceConfidenceMatchesTheDocsExamples(t *testing.T) {
	assert.InDelta(t, 0.4, ChoiceConfidence(map[string]float64{"a": 0.6, "b": 0.3, "c": 0.1}), 1e-9)
	assert.InDelta(t, 0.4, ChoiceConfidence(map[string]float64{"a": 0.6, "b": 0.2, "c": 0.2}), 1e-9, "only the top probability matters")
	assert.InDelta(t, 1.0, ChoiceConfidence(map[string]float64{"a": 1, "b": 0, "c": 0}), 1e-9)
	assert.InDelta(t, 0.0, ChoiceConfidence(map[string]float64{"a": 0.25, "b": 0.25, "c": 0.25, "d": 0.25}), 1e-9)
	assert.InDelta(t, 1.0, ChoiceConfidence(map[string]float64{"only": 1}), 1e-9, "a single option is certain")
}

func TestScoreConfidenceMatchesTheDocsExamples(t *testing.T) {
	assert.InDelta(t, 0.355, ScoreConfidence([]float64{0, 0.57, 0.43}), 0.005)
	assert.InDelta(t, 0.25, ScoreConfidence([]float64{0, 0.5, 0.5}), 1e-9)
	assert.InDelta(t, 0.0, ScoreConfidence([]float64{0.5, 0, 0.5}), 1e-9, "probability on the far ends is no confidence at all")
	assert.InDelta(t, 1.0, ScoreConfidence([]float64{0, 1, 0}), 1e-9)
}

func TestNoulConfidence(t *testing.T) {
	assert.InDelta(t, 1.0, NoulConfidence(1), 1e-9)
	assert.InDelta(t, 1.0, NoulConfidence(0), 1e-9)
	assert.InDelta(t, 0.0, NoulConfidence(0.5), 1e-9, "0.5 means yes and no are equally likely, not medium intensity")
	assert.InDelta(t, 0.6, NoulConfidence(0.8), 1e-9)
}

func TestScoreLevelsOrdersByLevelIndexNotByString(t *testing.T) {
	probs := map[string]float64{"10": 0.1, "2": 0.2, "0": 0.3, "1": 0.4}
	assert.Equal(t, []float64{0.3, 0.4, 0.2, 0.1}, ScoreLevels(probs))
}
