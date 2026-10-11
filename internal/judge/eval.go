package judge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Label is the known-correct answer to one question for one example: a
// Choice's option key, or a Noul's yes/no.
type Label struct {
	Choice string
	Yes    bool
}

// Example is one labeled item: the fields a set reads, and the labels it has
// for some of the set's questions (a question with no label is not scored).
type Example struct {
	ID     string
	Fields map[string]string
	Labels map[string]Label
}

// EvalOptions tunes an evaluation. The zero value is usable.
type EvalOptions struct {
	// Seeds are Choice-order shuffle seeds. The first gives the answers that are
	// scored; every other seed is a re-ask with the options reordered, and a
	// winner that changes with the order counts as a flip. Default {1, 2}.
	Seeds []int64
	// Thresholds are the confidence cut-offs of the coverage/accuracy sweep.
	// Default 0.0 to 0.9 in steps of 0.1.
	Thresholds []float64
	// MaxDisagreements caps the wrong answers listed per question. Default 8.
	MaxDisagreements int
}

// Band is accuracy within a confidence range.
type Band struct {
	Lo, Hi  float64
	N       int
	Correct int
}

// Accuracy of the band, 0 when it is empty.
func (b Band) Accuracy() float64 { return ratio(b.Correct, b.N) }

// SweepPoint is what acting only above a confidence threshold would cover and
// get right.
type SweepPoint struct {
	Threshold float64
	Covered   int
	Correct   int
	Coverage  float64 // covered / labeled
	Accuracy  float64 // correct / covered
}

// Disagreement is a wrong answer worth looking at: when the label is itself
// noisy (an old tag), these are the cases to review.
type Disagreement struct {
	ExampleID  string
	Predicted  string
	Label      string
	Confidence float64
}

// QuestionReport scores one question over the labeled examples.
type QuestionReport struct {
	Key           string
	Type          QuestionType
	Labeled       int
	Correct       int
	Accuracy      float64
	MeanConf      float64
	Bands         []Band
	Sweep         []SweepPoint
	Flips         int     // Choice: examples whose winner changed with option order
	FlipRate      float64 // Flips / examples compared
	Disagreements []Disagreement
}

// Report is the outcome of evaluating one set with one decider.
type Report struct {
	SetID           string
	SetVersion      int
	SetHash         string
	Decider         string
	Model           string
	Examples        int
	Errors          int
	Calls           int
	InputTokens     int
	OutputTokens    int
	EstimatedTokens int // our own estimate of the requests sent, for budgeting
	Latency         time.Duration
	Questions       []QuestionReport
}

// Evaluate asks every example's questions of the decider and scores the
// answers against the labels. It is the way a threshold earns trust: nothing
// is shown or applied on a confidence the evaluation has not looked at.
func Evaluate(ctx context.Context, set *Set, d Decider, examples []Example, opts EvalOptions) (*Report, error) {
	if len(opts.Seeds) == 0 {
		opts.Seeds = []int64{1, 2}
	}
	if len(opts.Thresholds) == 0 {
		for i := 0; i < 10; i++ {
			opts.Thresholds = append(opts.Thresholds, float64(i)/10)
		}
	}
	if opts.MaxDisagreements == 0 {
		opts.MaxDisagreements = 8
	}
	rep := &Report{SetID: set.ID, SetVersion: set.Version, SetHash: set.Hash(), Decider: d.Name(), Examples: len(examples)}

	type scored struct {
		conf    float64
		correct bool
	}
	scores := map[string][]scored{}
	dis := map[string][]Disagreement{}
	flips := map[string]int{}
	compared := map[string]int{}

	for _, ex := range examples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var primary Response
		ok := true
		winners := map[string][]string{} // question key -> winner per seed
		for si, seed := range opts.Seeds {
			req := set.BuildRequest(ex.Fields, seed)
			rep.EstimatedTokens += EstimateTokens(req)
			resp, err := d.Decide(ctx, req)
			rep.Calls++
			if err != nil {
				rep.Errors++
				ok = false
				break
			}
			rep.InputTokens += resp.Usage.InputTokens
			rep.OutputTokens += resp.Usage.OutputTokens
			rep.Latency += resp.Latency
			if rep.Model == "" {
				rep.Model = resp.Model
			}
			if si == 0 {
				primary = resp
			}
			for _, q := range set.Questions {
				if q.Type == Choice {
					winners[q.Key] = append(winners[q.Key], resp.Answers[q.Key].Choice)
				}
			}
		}
		if !ok {
			continue
		}
		for _, q := range set.Questions {
			ans, has := primary.Answers[q.Key]
			if !has {
				continue
			}
			if w := winners[q.Key]; len(w) > 1 {
				compared[q.Key]++
				for _, x := range w[1:] {
					if x != w[0] {
						flips[q.Key]++
						break
					}
				}
			}
			label, labeled := ex.Labels[q.Key]
			if !labeled {
				continue
			}
			pred, conf, correct := judgeAnswer(q, ans, label)
			scores[q.Key] = append(scores[q.Key], scored{conf: conf, correct: correct})
			if !correct {
				dis[q.Key] = append(dis[q.Key], Disagreement{ExampleID: ex.ID, Predicted: pred, Label: labelString(q, label), Confidence: conf})
			}
		}
	}

	for _, q := range set.Questions {
		qr := QuestionReport{Key: q.Key, Type: q.Type, Labeled: len(scores[q.Key])}
		sum := 0.0
		for _, s := range scores[q.Key] {
			if s.correct {
				qr.Correct++
			}
			sum += s.conf
		}
		qr.Accuracy = ratio(qr.Correct, qr.Labeled)
		if qr.Labeled > 0 {
			qr.MeanConf = sum / float64(qr.Labeled)
		}
		for i := 0; i < 5; i++ {
			qr.Bands = append(qr.Bands, Band{Lo: float64(i) / 5, Hi: float64(i+1) / 5})
		}
		for _, s := range scores[q.Key] {
			b := int(s.conf * 5)
			if b > 4 {
				b = 4
			}
			qr.Bands[b].N++
			if s.correct {
				qr.Bands[b].Correct++
			}
		}
		for _, t := range opts.Thresholds {
			p := SweepPoint{Threshold: t}
			for _, s := range scores[q.Key] {
				if s.conf >= t {
					p.Covered++
					if s.correct {
						p.Correct++
					}
				}
			}
			p.Coverage = ratio(p.Covered, qr.Labeled)
			p.Accuracy = ratio(p.Correct, p.Covered)
			qr.Sweep = append(qr.Sweep, p)
		}
		qr.Flips = flips[q.Key]
		qr.FlipRate = ratio(flips[q.Key], compared[q.Key])
		d := dis[q.Key]
		sort.SliceStable(d, func(i, j int) bool { return d[i].Confidence > d[j].Confidence }) // confident mistakes first
		if len(d) > opts.MaxDisagreements {
			d = d[:opts.MaxDisagreements]
		}
		qr.Disagreements = d
		rep.Questions = append(rep.Questions, qr)
	}
	return rep, nil
}

// judgeAnswer reads a decider's answer against a label: what it predicted,
// how confident it was (Noul by |2p-1|), and whether it was right.
func judgeAnswer(q SetQuestion, a Answer, l Label) (pred string, conf float64, correct bool) {
	switch q.Type {
	case Noul:
		yes := a.Noul >= 0.5
		pred = "no"
		if yes {
			pred = "yes"
		}
		return pred, NoulConfidence(a.Noul), yes == l.Yes
	default:
		return a.Choice, a.Confidence, a.Choice == l.Choice
	}
}

func labelString(q SetQuestion, l Label) string {
	if q.Type == Noul {
		if l.Yes {
			return "yes"
		}
		return "no"
	}
	return l.Choice
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// Text renders the report as plain text: per question, the accuracy overall,
// by confidence band, and the coverage/accuracy trade-off at each threshold.
func (r *Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Set %s v%d (%s)  decider %s  model %s\n", r.SetID, r.SetVersion, r.SetHash, r.Decider, orDash(r.Model))
	fmt.Fprintf(&b, "%d examples, %d calls, %d errors; input tokens %d (estimated %d), output %d; model time %s\n",
		r.Examples, r.Calls, r.Errors, r.InputTokens, r.EstimatedTokens, r.OutputTokens, r.Latency.Round(time.Millisecond))
	for _, q := range r.Questions {
		fmt.Fprintf(&b, "\n%s (%s): %d labeled, %.1f%% right, mean confidence %.2f", q.Key, q.Type, q.Labeled, q.Accuracy*100, q.MeanConf)
		if q.Type == Choice {
			fmt.Fprintf(&b, ", option-order flips %.1f%% (%d)", q.FlipRate*100, q.Flips)
		}
		b.WriteString("\n")
		if q.Labeled == 0 {
			b.WriteString("  no labels for this question\n")
			continue
		}
		b.WriteString("  confidence band   n    right\n")
		for _, bd := range q.Bands {
			fmt.Fprintf(&b, "  %.1f - %.1f      %5d  %s\n", bd.Lo, bd.Hi, bd.N, pct(bd.N, bd.Accuracy()))
		}
		b.WriteString("  act above   covers   right among covered\n")
		for _, p := range q.Sweep {
			fmt.Fprintf(&b, "  %.1f        %5.1f%%   %s\n", p.Threshold, p.Coverage*100, pct(p.Covered, p.Accuracy))
		}
		if len(q.Disagreements) > 0 {
			b.WriteString("  most confident disagreements (review: the label may be the noisy side):\n")
			for _, d := range q.Disagreements {
				fmt.Fprintf(&b, "    %s  said %s (conf %.2f), label %s\n", d.ExampleID, d.Predicted, d.Confidence, d.Label)
			}
		}
	}
	return b.String()
}

func pct(n int, acc float64) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", acc*100)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
