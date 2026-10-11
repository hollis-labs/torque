package judge

import (
	"math"
	"sort"
	"strconv"
)

// The confidence formulas are TypeSafe's (docs.typesafe.ai/confidence): a 0..1
// statistic of how concentrated the distribution is. It is NOT the chance the
// answer is right, and it is not permission to act: thresholds come from
// evaluating on our own data.

// ChoiceConfidence is (pmax - 1/n) / (1 - 1/n): 1 when all probability is on
// one option, 0 when it is spread evenly.
func ChoiceConfidence(probs map[string]float64) float64 {
	n := len(probs)
	if n <= 1 {
		return 1
	}
	pmax := 0.0
	for _, p := range probs {
		pmax = math.Max(pmax, p)
	}
	inv := 1 / float64(n)
	return clamp01((pmax - inv) / (1 - inv))
}

// ScoreConfidence is max(0, 1 - sum_i p_i|i-m| / MAD), with m the most likely
// level and MAD the average distance from the middle level under an even
// spread. Probability on a neighbouring level costs less than on a far one.
func ScoreConfidence(levels []float64) float64 {
	n := len(levels)
	if n <= 1 {
		return 1
	}
	m := 0
	for i, p := range levels {
		if p > levels[m] {
			m = i
		}
	}
	mid := float64(n-1) / 2
	mad := 0.0
	for i := 0; i < n; i++ {
		mad += math.Abs(float64(i) - mid)
	}
	mad /= float64(n)
	spread := 0.0
	for i, p := range levels {
		spread += p * math.Abs(float64(i-m))
	}
	return clamp01(1 - spread/mad)
}

// NoulConfidence is |2p - 1|: 1 for a sure yes or no, 0 at 0.5. Noul answers
// carry no confidence field of their own; this makes them comparable.
func NoulConfidence(p float64) float64 { return clamp01(math.Abs(2*p - 1)) }

// ScoreLevels orders a Score answer's probability map ("0".."n-1") into a slice.
func ScoreLevels(probs map[string]float64) []float64 {
	keys := make([]int, 0, len(probs))
	for k := range probs {
		if i, err := strconv.Atoi(k); err == nil {
			keys = append(keys, i)
		}
	}
	sort.Ints(keys)
	out := make([]float64, len(keys))
	for i, k := range keys {
		out[i] = probs[strconv.Itoa(k)]
	}
	return out
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }
