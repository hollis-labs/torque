package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Suggestions become tags only when a person (or a measured, recorded rule)
// accepts them. Accepting adds the suggested tags to the task and closes the
// ledger row; it never removes a tag and never touches other fields.

// Suggestion is an open ledger row that proposes tags, with the task it is
// about, for the triage list and the task page.
type Suggestion struct {
	sqlstore.JudgmentRecord
	TaskTitle string   `json:"task_title,omitempty"`
	TaskTags  []string `json:"task_tags,omitempty"`
}

// SuggestionFilter narrows the triage list.
type SuggestionFilter struct {
	TaskID        string
	SetID         string
	QuestionKey   string
	MinConfidence float64 // inclusive
	MaxConfidence float64 // exclusive; 0 = no upper bound
	Limit         int
}

// openSuggestions lists suggested ledger rows that actually propose a tag,
// highest confidence first.
func (s *JudgeService) openSuggestions(f SuggestionFilter) ([]sqlstore.JudgmentRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.store.ListJudgments(sqlstore.JudgmentFilter{
		SubjectType: "task", SubjectID: f.TaskID, SetID: f.SetID, QuestionKey: f.QuestionKey,
		Disposition: sqlstore.JudgmentSuggested, Limit: 5000,
	})
	if err != nil {
		return nil, err
	}
	var out []sqlstore.JudgmentRecord
	for _, r := range rows {
		if len(r.Suggested) == 0 || r.Confidence < f.MinConfidence || (f.MaxConfidence > 0 && r.Confidence >= f.MaxConfidence) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Suggestions returns open tag suggestions with their task's title and tags.
func (s *JudgeService) Suggestions(f SuggestionFilter) ([]Suggestion, error) {
	rows, err := s.openSuggestions(f)
	if err != nil {
		return nil, err
	}
	out := make([]Suggestion, 0, len(rows))
	for _, r := range rows {
		sg := Suggestion{JudgmentRecord: r}
		if t, err := s.store.GetTask(r.SubjectID); err == nil {
			sg.TaskTitle = t.Title
		}
		if tags, err := s.store.ListTaskTags(r.SubjectID); err == nil {
			for _, tg := range tags {
				sg.TaskTags = append(sg.TaskTags, tg.Slug)
			}
		}
		out = append(out, sg)
	}
	return out, nil
}

// Accept adds a suggestion's tags to its task and records it accepted. It
// returns the tags that were added (those the task did not already carry).
func (s *JudgeService) Accept(id int64, by string) ([]string, error) {
	rec, err := s.store.GetJudgment(id)
	if err != nil {
		return nil, mapJudgmentErr(err)
	}
	if rec.Disposition != sqlstore.JudgmentSuggested {
		return nil, &ConflictError{Message: fmt.Sprintf("judgment %d is already %s", id, rec.Disposition)}
	}
	if rec.SubjectType != "task" {
		return nil, &ValidationError{Field: "subject_type", Message: "only task suggestions can be applied here"}
	}
	added, err := s.applyTags(rec.SubjectID, rec.Suggested)
	if err != nil {
		return nil, err
	}
	if err := s.store.DecideJudgment(id, sqlstore.JudgmentAccepted, by); err != nil {
		return added, mapJudgmentErr(err)
	}
	return added, nil
}

// Reject records a suggestion rejected; the task is not changed.
func (s *JudgeService) Reject(id int64, by string) error {
	return s.Decide(id, false, by)
}

// applyTags adds slugs to a task's tags, keeping the ones it has.
func (s *JudgeService) applyTags(taskID string, slugs []string) ([]string, error) {
	if _, err := s.store.GetTask(taskID); err != nil {
		return nil, &NotFoundError{Entity: "task", ID: taskID}
	}
	cur, err := s.store.ListTaskTags(taskID)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	var all []string
	for _, t := range cur {
		have[t.Slug] = true
		all = append(all, t.Slug)
	}
	resolved, err := s.tags.ResolveNames(slugs)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, sl := range resolved {
		if !have[sl] {
			have[sl] = true
			all = append(all, sl)
			added = append(added, sl)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	return added, s.store.SetTaskTags(taskID, all)
}

// BulkResult is the outcome of deciding many suggestions at once.
type BulkResult struct {
	Decided int      `json:"decided"`
	Failed  []string `json:"failed,omitempty"`
}

// BulkDecide accepts or rejects every open suggestion matching the filter (a
// confidence band, a set or question, or one task), recording each in the
// ledger. A limit is required so one call cannot sweep the whole backlog by
// accident.
func (s *JudgeService) BulkDecide(f SuggestionFilter, accept bool, by string) (BulkResult, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		return BulkResult{}, &ValidationError{Field: "limit", Message: "limit is required, 1..500"}
	}
	rows, err := s.openSuggestions(f)
	if err != nil {
		return BulkResult{}, err
	}
	var res BulkResult
	for _, r := range rows {
		var derr error
		if accept {
			_, derr = s.Accept(r.ID, by)
		} else {
			derr = s.Reject(r.ID, by)
		}
		if derr != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%d: %v", r.ID, derr))
			continue
		}
		res.Decided++
	}
	return res, nil
}

// SetDecider installs the decider used for enrichment on create and by the
// HTTP enrich call. nil removes it.
func (s *JudgeService) SetDecider(d judge.Decider) {
	s.dmu.Lock()
	s.decider = d
	s.dmu.Unlock()
}

func (s *JudgeService) currentDecider() judge.Decider {
	s.dmu.RLock()
	defer s.dmu.RUnlock()
	return s.decider
}

// Status reports whether live enrichment is on.
type JudgeStatus struct {
	FeatureEnabled    bool   `json:"feature_enabled"`
	DeciderConfigured bool   `json:"decider_configured"`
	Decider           string `json:"decider,omitempty"`
	Active            bool   `json:"active"`
}

// Status returns the enrichment switch state: it runs only when the `judge`
// feature is on AND a decider is installed.
func (s *JudgeService) Status() JudgeStatus {
	st := JudgeStatus{FeatureEnabled: s.feature.IsEnabled("judge")}
	if d := s.currentDecider(); d != nil {
		st.DeciderConfigured, st.Decider = true, d.Name()
	}
	st.Active = st.FeatureEnabled && st.DeciderConfigured
	return st
}

// EnrichNow asks every task set about a task with the installed decider and
// records the suggestions. It needs the decider but not the feature flag: it
// is an explicit request.
func (s *JudgeService) EnrichNow(ctx context.Context, taskID string) ([]sqlstore.JudgmentRecord, error) {
	d := s.currentDecider()
	if d == nil {
		return nil, &ValidationError{Field: "decider", Message: "no decider is configured (set TORQUE_TETHER_URL for torque serve)"}
	}
	return s.enrichAllSets(ctx, d, taskID)
}

func (s *JudgeService) enrichAllSets(ctx context.Context, d judge.Decider, taskID string) ([]sqlstore.JudgmentRecord, error) {
	sets, err := s.loadSets()
	if err != nil {
		return nil, err
	}
	var out []sqlstore.JudgmentRecord
	var errs []error
	for _, id := range judge.SetIDs(sets) {
		if sets[id].AppliesTo != "task" {
			continue
		}
		recs, err := s.EnrichTask(ctx, d, id, taskID, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		out = append(out, recs...)
	}
	return out, errors.Join(errs...)
}

// EnrichOnCreate is the hook for new tasks: when enrichment is active it
// queues the task and returns at once. Nothing here can fail or slow a create;
// a full queue drops the task (backfill picks it up later).
func (s *JudgeService) EnrichOnCreate(taskID string) {
	if !s.Status().Active {
		return
	}
	s.qonce.Do(func() {
		s.queue = make(chan string, 64)
		go s.drain()
	})
	select {
	case s.queue <- taskID:
	default:
		slog.Warn("[judge] enrichment queue full; task left for backfill", "task", taskID)
	}
}

func (s *JudgeService) drain() {
	for id := range s.queue {
		d := s.currentDecider()
		if d == nil || !s.feature.IsEnabled("judge") {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if _, err := s.enrichAllSets(ctx, d, id); err != nil {
			slog.Warn("[judge] enrichment failed", "task", id, "err", err)
		}
		cancel()
	}
}

// BackfillOptions controls Backfill.
type BackfillOptions struct {
	SetID       string  // required
	Limit       int     // tasks to ask about at most (0 = all)
	IncludeDone bool    // default: open tasks only
	Concurrency int     // default 4
	MaxCostUSD  float64 // stop before this much input spend (0 = no limit)
}

// BackfillPlan counts what a backfill would ask.
type BackfillPlan struct {
	Candidates  int     `json:"candidates"`
	Calls       int     `json:"calls"`
	EstTokens   int     `json:"estimated_input_tokens"`
	EstCostUSD  float64 `json:"estimated_cost_usd"`
	AlreadyDone int     `json:"already_judged"`
}

func (s *JudgeService) backfillCandidates(set *judge.Set, opts BackfillOptions) ([]judge.Item, int, error) {
	tasks, err := s.store.JudgeTaskCorpus()
	if err != nil {
		return nil, 0, err
	}
	var out []judge.Item
	done := 0
	for _, t := range tasks {
		if !opts.IncludeDone && (t.Status == "done" || t.Status == "archived") {
			continue
		}
		if !set.Needs(t.Tags) {
			continue
		}
		prior, err := s.store.ListJudgments(sqlstore.JudgmentFilter{SubjectType: "task", SubjectID: t.ID, SetID: set.ID, Limit: 50})
		if err != nil {
			return nil, 0, err
		}
		seen := false
		for _, p := range prior {
			if p.SetHash == set.Hash() && p.Disposition != sqlstore.JudgmentSuperseded {
				seen = true
				break
			}
		}
		if seen {
			done++
			continue
		}
		out = append(out, judge.Item{ID: t.ID, Fields: map[string]string{"title": t.Title, "description": t.Description}, Tags: t.Tags})
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, done, nil
}

// PlanBackfill estimates a backfill without calling anything.
func (s *JudgeService) PlanBackfill(opts BackfillOptions) (BackfillPlan, error) {
	set, err := s.Set(opts.SetID)
	if err != nil {
		return BackfillPlan{}, err
	}
	items, done, err := s.backfillCandidates(set, opts)
	if err != nil {
		return BackfillPlan{}, err
	}
	plan := BackfillPlan{Candidates: len(items), Calls: len(items), AlreadyDone: done}
	for _, it := range items {
		if req, ok := set.BuildSuggestionRequest(it.Fields, 1); ok {
			plan.EstTokens += judge.EstimateTokens(req)
		}
	}
	plan.EstCostUSD = float64(plan.EstTokens) / 1e6 * judge.InputPricePerMTokUSD
	return plan, nil
}

// BackfillResult is what a backfill did.
type BackfillResult struct {
	Asked       int      `json:"asked"`
	Suggestions int      `json:"suggestions"` // rows that propose a tag
	Failed      int      `json:"failed"`
	Truncated   bool     `json:"truncated"`
	Errors      []string `json:"errors,omitempty"`
}

// Backfill asks a set about the open tasks that carry none of its tags, and
// records the answers as suggestions. Re-running skips tasks already judged by
// this wording of the set.
func (s *JudgeService) Backfill(ctx context.Context, d judge.Decider, opts BackfillOptions) (BackfillResult, error) {
	set, err := s.Set(opts.SetID)
	if err != nil {
		return BackfillResult{}, err
	}
	items, _, err := s.backfillCandidates(set, opts)
	if err != nil {
		return BackfillResult{}, err
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = 4
	}
	var (
		mu    sync.Mutex
		res   BackfillResult
		spent float64
		wg    sync.WaitGroup
		sem   = make(chan struct{}, conc)
	)
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		req, ok := set.BuildSuggestionRequest(it.Fields, 1)
		if !ok {
			continue
		}
		est := float64(judge.EstimateTokens(req)) / 1e6 * judge.InputPricePerMTokUSD
		mu.Lock()
		if opts.MaxCostUSD > 0 && spent+est > opts.MaxCostUSD {
			res.Truncated = true
			mu.Unlock()
			break
		}
		spent += est
		mu.Unlock()
		sem <- struct{}{}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			recs, err := s.EnrichTask(ctx, d, set.ID, id, true)
			mu.Lock()
			defer mu.Unlock()
			res.Asked++
			if err != nil {
				res.Failed++
				if len(res.Errors) < 5 {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", id, err))
				}
				return
			}
			for _, r := range recs {
				if len(r.Suggested) > 0 {
					res.Suggestions++
				}
			}
		}(it.ID)
	}
	wg.Wait()
	return res, nil
}

// Counts returns, per question, how many ledger rows are in each disposition.
func (s *JudgeService) Counts(setID string) (map[string]map[string]int, error) {
	return s.store.JudgmentCounts(setID)
}
