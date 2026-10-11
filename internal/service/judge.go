package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// JudgeService asks structured questions about Torque's own things, records
// the answers in the enrichment ledger, and evaluates question sets against
// the tags tasks already carry. JEV (through Tether's Decide) is one Decider;
// the keyword baseline is another. Nothing here changes a task: an answer
// becomes a suggestion in the ledger and a person or a measured rule decides.
type JudgeService struct {
	store *sqlstore.Store

	once sync.Once
	sets map[string]*judge.Set
	err  error
}

// loadSets reads the embedded question sets once.
func (s *JudgeService) loadSets() (map[string]*judge.Set, error) {
	s.once.Do(func() { s.sets, s.err = judge.LoadSets() })
	return s.sets, s.err
}

// Sets returns the question sets by id.
func (s *JudgeService) Sets() (map[string]*judge.Set, error) { return s.loadSets() }

// Set returns one question set.
func (s *JudgeService) Set(id string) (*judge.Set, error) {
	sets, err := s.loadSets()
	if err != nil {
		return nil, err
	}
	set, ok := sets[id]
	if !ok {
		return nil, &NotFoundError{Entity: "question set", ID: id}
	}
	return set, nil
}

// TaskItems reads the task corpus: every user-facing task with its text and tags.
func (s *JudgeService) TaskItems() ([]judge.Item, error) {
	tasks, err := s.store.JudgeTaskCorpus()
	if err != nil {
		return nil, err
	}
	items := make([]judge.Item, len(tasks))
	for i, t := range tasks {
		items[i] = judge.Item{ID: t.ID, Fields: map[string]string{"title": t.Title, "description": t.Description}, Tags: t.Tags}
	}
	return items, nil
}

// JudgeEvalOptions tunes JudgeService.Evaluate.
type JudgeEvalOptions struct {
	N           int     // sample size per question (default 200)
	Seed        int64   // sampling seed
	NoFlips     bool    // skip the second, reordered ask (halves the calls)
	Concurrency int     // examples asked at once (default 1)
	MaxCalls    int     // stop before more calls than this (0 = no limit)
	MaxCostUSD  float64 // stop at this much input spend (0 = no limit)
}

func (o JudgeEvalOptions) evalOptions() judge.EvalOptions {
	eo := judge.EvalOptions{Concurrency: o.Concurrency, MaxCalls: o.MaxCalls, MaxCostUSD: o.MaxCostUSD}
	if o.NoFlips {
		eo.Seeds = []int64{1}
	}
	return eo
}

// EstimateEval works out what Evaluate would ask and cost, calling nothing.
func (s *JudgeService) EstimateEval(setID string, opts JudgeEvalOptions) (judge.Estimate, error) {
	set, err := s.Set(setID)
	if err != nil {
		return judge.Estimate{}, err
	}
	items, err := s.TaskItems()
	if err != nil {
		return judge.Estimate{}, err
	}
	examples := judge.LabelExamples(set, items, judge.SampleOptions{N: opts.N, Seed: opts.Seed})
	return judge.EstimateEval(set, examples, opts.evalOptions()), nil
}

// Evaluate scores a question set against the tags tasks already carry.
func (s *JudgeService) Evaluate(ctx context.Context, setID string, d judge.Decider, opts JudgeEvalOptions) (*judge.Report, error) {
	set, err := s.Set(setID)
	if err != nil {
		return nil, err
	}
	items, err := s.TaskItems()
	if err != nil {
		return nil, err
	}
	examples := judge.LabelExamples(set, items, judge.SampleOptions{N: opts.N, Seed: opts.Seed})
	return judge.Evaluate(ctx, set, d, examples, opts.evalOptions())
}

// stateHash identifies the exact state sent, without keeping the text.
func stateHash(req judge.Request) string {
	b, _ := json.Marshal(req.State)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// seedFor derives a stable shuffle seed from a subject id, so asking about
// the same task twice shuffles the same way.
func seedFor(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64() >> 1)
}

// EnrichTask asks a set's questions about a task and records each answer in
// the ledger as a suggestion, with the tags it would suggest (never ones the
// task already carries). With record=false it only returns what would be
// recorded. A task whose state and the set's wording are unchanged is not
// recorded twice.
func (s *JudgeService) EnrichTask(ctx context.Context, d judge.Decider, setID, taskID string, record bool) ([]sqlstore.JudgmentRecord, error) {
	set, err := s.Set(setID)
	if err != nil {
		return nil, err
	}
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, &NotFoundError{Entity: "task", ID: taskID}
	}
	existing := map[string]bool{}
	if tags, err := s.store.ListTaskTags(taskID); err == nil {
		for _, t := range tags {
			existing[t.Slug] = true
		}
	}
	req, any := set.BuildSuggestionRequest(map[string]string{"title": task.Title, "description": task.Description}, seedFor(taskID))
	if !any {
		return nil, nil // every question in the set is disabled
	}
	resp, err := d.Decide(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("decide: %w", err)
	}
	hash := stateHash(req)
	var out []sqlstore.JudgmentRecord
	for _, q := range set.Questions {
		a, ok := resp.Answers[q.Key]
		if !ok || q.Disabled {
			continue
		}
		var suggested []string
		for _, t := range q.SuggestedTags(a) {
			if !existing[t] {
				suggested = append(suggested, t)
			}
		}
		rec := sqlstore.JudgmentRecord{
			SubjectType: "task", SubjectID: taskID, SetID: set.ID, SetVersion: set.Version, SetHash: set.Hash(),
			QuestionKey: q.Key, AnswerType: string(a.Type), Choice: a.Choice, Probabilities: a.Probabilities,
			Confidence: a.Confidence, Suggested: suggested, StateHash: hash,
			Provider: d.Name(), Model: resp.Model, Purpose: set.Purpose,
			InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, LatencyMS: int(resp.Latency.Milliseconds()),
		}
		switch a.Type {
		case judge.Noul:
			v := a.Noul
			rec.Noul = &v
			rec.Confidence = judge.NoulConfidence(a.Noul) // a Noul carries no confidence of its own
		case judge.Score:
			v := a.Score
			rec.Score = &v
		}
		if record {
			if _, err := s.store.RecordJudgment(&rec); err != nil {
				return nil, err
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

// Decide accepts or rejects a suggestion; accepting a suggestion does not
// itself tag the task (the caller applies the suggested tags it chooses).
func (s *JudgeService) Decide(id int64, accept bool, by string) error {
	disp := sqlstore.JudgmentRejected
	if accept {
		disp = sqlstore.JudgmentAccepted
	}
	err := s.store.DecideJudgment(id, disp, by)
	return mapJudgmentErr(err)
}

func mapJudgmentErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sqlstore.ErrJudgmentNotFound):
		return &NotFoundError{Entity: "judgment", ID: err.Error()}
	case errors.Is(err, sqlstore.ErrJudgmentDecided):
		return &ConflictError{Message: err.Error()}
	}
	return err
}

// Judgments lists ledger rows.
func (s *JudgeService) Judgments(f sqlstore.JudgmentFilter) ([]sqlstore.JudgmentRecord, error) {
	return s.store.ListJudgments(f)
}
