package judge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// Concurrency is how many examples are asked at once (default 1). Results
	// are aggregated in example order, so the report is the same at any value.
	Concurrency int
	// MaxCalls and MaxCostUSD stop the run before it spends past them (0 = no
	// limit). The report says it was truncated and how many examples ran.
	// Reported input tokens are priced at InputPricePerMTokUSD.
	MaxCalls   int
	MaxCostUSD float64
}

// MinPositives is how many positive labels a yes/no question needs before its
// accuracy means anything. With fewer, accuracy is mostly the rate of saying no.
const MinPositives = 20

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
	Positives     int     // Noul: labeled examples whose label is yes
	Negatives     int     // Noul: labeled examples whose label is no
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
	EstimatedTokens int     // our own estimate of the requests sent, for budgeting
	CostUSD         float64 // reported input tokens at the provider's price
	NotRun          int     // examples skipped because MaxCalls or MaxCostUSD was reached
	Truncated       bool
	Latency         time.Duration
	Questions       []QuestionReport
}

// exampleRun is everything asked about one example, collected without
// touching shared state so examples can run concurrently.
type exampleRun struct {
	ran       bool // false: skipped by a limit
	failed    bool // a call errored; the example is not scored
	calls     int
	estimated int
	usage     Usage
	latency   time.Duration
	model     string
	primary   Response
	winners   map[string][]string // question key -> winning option per seed
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
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	rep := &Report{SetID: set.ID, SetVersion: set.Version, SetHash: set.Hash(), Decider: d.Name(), Examples: len(examples)}

	// Phase 1: ask. Workers write only their own slot of runs.
	runs := make([]exampleRun, len(examples))
	var calls, inTokens int64
	var wg sync.WaitGroup
	jobs := make(chan int)
	over := func() bool {
		if opts.MaxCalls > 0 && int(atomic.LoadInt64(&calls))+len(opts.Seeds) > opts.MaxCalls {
			return true
		}
		return opts.MaxCostUSD > 0 && CostUSD(int(atomic.LoadInt64(&inTokens))) >= opts.MaxCostUSD
	}
	for w := 0; w < opts.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil || over() {
					continue // leave the slot as not run
				}
				run := exampleRun{ran: true, winners: map[string][]string{}}
				for si, seed := range opts.Seeds {
					req := set.BuildRequest(examples[i].Fields, seed)
					run.estimated += EstimateTokens(req)
					resp, err := d.Decide(ctx, req)
					atomic.AddInt64(&calls, 1)
					run.calls++
					if err != nil {
						run.failed = true
						break
					}
					atomic.AddInt64(&inTokens, int64(resp.Usage.InputTokens))
					run.usage.InputTokens += resp.Usage.InputTokens
					run.usage.OutputTokens += resp.Usage.OutputTokens
					run.latency += resp.Latency
					if run.model == "" {
						run.model = resp.Model
					}
					if si == 0 {
						run.primary = resp
					}
					for _, q := range set.Questions {
						if q.Type == Choice {
							run.winners[q.Key] = append(run.winners[q.Key], resp.Answers[q.Key].Choice)
						}
					}
				}
				runs[i] = run
			}
		}()
	}
	for i := range examples {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 2: score, in example order, so the report does not depend on timing.
	type scored struct {
		conf    float64
		correct bool
	}
	scores := map[string][]scored{}
	positives := map[string]int{}
	negatives := map[string]int{}
	dis := map[string][]Disagreement{}
	flips := map[string]int{}
	compared := map[string]int{}

	for i, ex := range examples {
		run := runs[i]
		if !run.ran {
			rep.NotRun++
			continue
		}
		rep.Calls += run.calls
		rep.EstimatedTokens += run.estimated
		rep.InputTokens += run.usage.InputTokens
		rep.OutputTokens += run.usage.OutputTokens
		rep.Latency += run.latency
		if rep.Model == "" {
			rep.Model = run.model
		}
		if run.failed {
			rep.Errors++
			continue
		}
		for _, q := range set.Questions {
			ans, has := run.primary.Answers[q.Key]
			if !has {
				continue
			}
			if w := run.winners[q.Key]; len(w) > 1 {
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
			if q.Type == Noul {
				if label.Yes {
					positives[q.Key]++
				} else {
					negatives[q.Key]++
				}
			}
			if !correct {
				dis[q.Key] = append(dis[q.Key], Disagreement{ExampleID: ex.ID, Predicted: pred, Label: labelString(q, label), Confidence: conf})
			}
		}
	}
	rep.CostUSD = CostUSD(rep.InputTokens)
	rep.Truncated = rep.NotRun > 0

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
		qr.Positives, qr.Negatives = positives[q.Key], negatives[q.Key]
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

// Estimate is what an evaluation would ask and cost, worked out without
// calling anything.
type Estimate struct {
	Examples        int
	CallsPerExample int
	Calls           int
	EstimatedTokens int
	EstimatedUSD    float64
}

// EstimateEval works out the calls and input tokens an evaluation of these
// examples would use, with the same request building and overhead as the real
// run. Use it to approve a spend before making it.
func EstimateEval(set *Set, examples []Example, opts EvalOptions) Estimate {
	seeds := len(opts.Seeds)
	if seeds == 0 {
		seeds = 2
	}
	e := Estimate{Examples: len(examples), CallsPerExample: seeds, Calls: len(examples) * seeds}
	for _, ex := range examples {
		e.EstimatedTokens += EstimateTokens(set.BuildRequest(ex.Fields, 1)) * seeds
	}
	e.EstimatedUSD = CostUSD(e.EstimatedTokens)
	return e
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
	fmt.Fprintf(&b, "%d examples, %d calls, %d errors; input tokens %d (estimated %d), output %d, input cost $%.4f; model time %s\n",
		r.Examples, r.Calls, r.Errors, r.InputTokens, r.EstimatedTokens, r.OutputTokens, r.CostUSD, r.Latency.Round(time.Millisecond))
	if r.Truncated {
		fmt.Fprintf(&b, "TRUNCATED: stopped at the call or spend limit; %d of %d examples were not asked, so every figure below is on a partial sample.\n", r.NotRun, r.Examples)
	}
	for _, q := range r.Questions {
		fmt.Fprintf(&b, "\n%s (%s): %d labeled, %.1f%% right, mean confidence %.2f", q.Key, q.Type, q.Labeled, q.Accuracy*100, q.MeanConf)
		if q.Type == Choice {
			fmt.Fprintf(&b, ", option-order flips %.1f%% (%d)", q.FlipRate*100, q.Flips)
		}
		if q.Type == Noul {
			fmt.Fprintf(&b, ", %d positive / %d negative labels", q.Positives, q.Negatives)
		}
		b.WriteString("\n")
		if q.Type == Noul && q.Labeled > 0 && q.Positives < MinPositives {
			fmt.Fprintf(&b, "  WARNING: only %d positive labels (need %d). Accuracy here is mostly how often the answer is no; it says nothing about finding the real cases. Label some by hand before judging this question.\n", q.Positives, MinPositives)
		}
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
