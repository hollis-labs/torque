package judge

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Baseline is a bag-of-words Decider: no model, no network. It scores a
// Choice option by how many of the option's own words appear in the state, and
// a Noul by overlap with its yes and no descriptions. It exists for two
// reasons: to exercise the evaluation harness end to end without a key, and as
// the floor a real model has to beat. If JEV cannot beat this on a question
// set, that set is not worth a model call.
type Baseline struct{}

// Name implements Decider.
func (Baseline) Name() string { return "keyword-baseline" }

// Decide implements Decider.
func (Baseline) Decide(_ context.Context, req Request) (Response, error) {
	start := time.Now()
	state := stateWords(req.State)
	resp := Response{Model: "keyword-baseline", Answers: map[string]Answer{}}
	for _, q := range req.Questions {
		switch q.Type {
		case Choice:
			scores := make([]float64, len(q.Options))
			for i, o := range q.Options {
				scores[i] = overlap(state, words(o.Key+" "+o.Description))
				if state[strings.ToLower(o.Key)] {
					scores[i] += 3 // the option's own name appearing outright is a strong sign
				}
				if o.Key == "other" {
					scores[i] = 0.5 // a weak prior, so "none of these" wins when nothing matches
				}
			}
			probs := softmax(scores, 1.2)
			a := Answer{Type: Choice, Probabilities: map[string]float64{}}
			best := 0
			for i, o := range q.Options {
				a.Probabilities[o.Key] = probs[i]
				if probs[i] > probs[best] {
					best = i
				}
			}
			a.Choice = q.Options[best].Key
			a.Confidence = ChoiceConfidence(a.Probabilities)
			resp.Answers[q.Key] = a
		case Noul:
			yes := overlap(state, words(q.True))
			no := overlap(state, words(q.False))
			p := 1 / (1 + math.Exp(-(yes - no - 1.0)))
			resp.Answers[q.Key] = Answer{Type: Noul, Noul: p, Confidence: NoulConfidence(p)}
		case Score:
			n := len(q.Levels)
			probs := make(map[string]float64, n)
			levels := make([]float64, n)
			for i := range levels {
				levels[i] = 1 / float64(n)
				probs[fmt.Sprint(i)] = levels[i]
			}
			resp.Answers[q.Key] = Answer{Type: Score, Score: float64(n-1) / 2, Probabilities: probs, Confidence: ScoreConfidence(levels)}
		default:
			return Response{}, fmt.Errorf("baseline: unknown question type %q", q.Type)
		}
	}
	resp.Usage = Usage{InputTokens: EstimateTokens(req)}
	resp.Latency = time.Since(start)
	return resp, nil
}

// Scripted is a Decider backed by a function, for tests.
type Scripted struct {
	Label string
	Fn    func(Request) (Response, error)
}

// Name implements Decider.
func (s Scripted) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return "scripted"
}

// Decide implements Decider.
func (s Scripted) Decide(_ context.Context, req Request) (Response, error) { return s.Fn(req) }

func stateWords(state any) map[string]bool {
	out := map[string]bool{}
	switch v := state.(type) {
	case map[string]string:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for w := range words(v[k]) {
				out[w] = true
			}
		}
	case string:
		for w := range words(v) {
			out[w] = true
		}
	}
	return out
}

var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "not": true, "are": true, "from": true, "than": true, "has": true, "have": true, "will": true, "its": true, "into": true, "any": true, "but": true, "does": true, "can": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= 3 && !stop[w] {
			out[w] = true
		}
	}
	return out
}

func overlap(state, w map[string]bool) float64 {
	n := 0.0
	for k := range w {
		if state[k] {
			n++
		}
	}
	return n
}

func softmax(scores []float64, sharpness float64) []float64 {
	max := scores[0]
	for _, s := range scores {
		max = math.Max(max, s)
	}
	out := make([]float64, len(scores))
	sum := 0.0
	for i, s := range scores {
		out[i] = math.Exp((s - max) * sharpness)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}
