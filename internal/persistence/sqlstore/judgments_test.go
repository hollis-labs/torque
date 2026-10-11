package sqlstore_test

import (
	"errors"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgment(subject, hash string, over ...func(*sqlstore.JudgmentRecord)) *sqlstore.JudgmentRecord {
	r := &sqlstore.JudgmentRecord{
		SubjectType: "task", SubjectID: subject, SetID: "task-kind", SetVersion: 1, SetHash: "set-a",
		QuestionKey: "kind", AnswerType: "choice", Choice: "bug", Probabilities: map[string]float64{"bug": 0.8, "docs": 0.2},
		Confidence: 0.6, Suggested: []string{"bug"}, StateHash: hash, Provider: "jev", Model: "jev-1.13.0", Purpose: "task_enrichment",
		InputTokens: 120, OutputTokens: 3, LatencyMS: 90,
	}
	for _, o := range over {
		o(r)
	}
	return r
}

func TestRecordJudgmentRoundTripsEveryField(t *testing.T) {
	store := setupTestStore(t)
	yes := 0.93
	r := judgment("CW-1", "h1", func(r *sqlstore.JudgmentRecord) {
		r.AnswerType, r.Choice, r.Noul, r.QuestionKey = "noul", "", &yes, "discuss_first"
		r.Probabilities, r.Suggested = nil, []string{"discuss-first"}
	})
	created, err := store.RecordJudgment(r)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Positive(t, r.ID)

	got, err := store.GetJudgment(r.ID)
	require.NoError(t, err)
	assert.Equal(t, "CW-1", got.SubjectID)
	assert.Equal(t, "discuss_first", got.QuestionKey)
	require.NotNil(t, got.Noul)
	assert.InDelta(t, 0.93, *got.Noul, 1e-9)
	assert.Nil(t, got.Score)
	assert.Equal(t, []string{"discuss-first"}, got.Suggested)
	assert.Equal(t, map[string]float64{}, got.Probabilities)
	assert.Equal(t, "jev-1.13.0", got.Model)
	assert.Equal(t, 120, got.InputTokens)
	assert.Equal(t, sqlstore.JudgmentSuggested, got.Disposition)
	assert.Nil(t, got.DecidedAt)
	assert.False(t, got.CreatedAt.IsZero())
}

func TestRecordJudgmentIsIdempotentOnSubjectWordingQuestionAndState(t *testing.T) {
	store := setupTestStore(t)
	first := judgment("CW-1", "h1")
	created, err := store.RecordJudgment(first)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, store.DecideJudgment(first.ID, sqlstore.JudgmentAccepted, "chrispian"))

	again := judgment("CW-1", "h1", func(r *sqlstore.JudgmentRecord) { r.Confidence = 0.99 })
	created, err = store.RecordJudgment(again)
	require.NoError(t, err)
	assert.False(t, created, "the same question about the same state is not recorded twice")
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, sqlstore.JudgmentAccepted, again.Disposition, "the existing decision is reported back")
	all, _ := store.ListJudgments(sqlstore.JudgmentFilter{SubjectID: "CW-1"})
	assert.Len(t, all, 1)
	assert.InDelta(t, 0.6, all[0].Confidence, 1e-9, "the first answer stands")
}

func TestANewStateSupersedesOpenSuggestionsButNotDecidedOnes(t *testing.T) {
	store := setupTestStore(t)
	open := judgment("CW-1", "old-state")
	_, err := store.RecordJudgment(open)
	require.NoError(t, err)
	decided := judgment("CW-1", "older-state", func(r *sqlstore.JudgmentRecord) { r.Choice = "docs" })
	_, err = store.RecordJudgment(decided)
	require.NoError(t, err)
	// recording "decided" superseded "open" (a newer state for the same question); now decide it.
	require.NoError(t, store.DecideJudgment(decided.ID, sqlstore.JudgmentRejected, "chrispian"))
	other := judgment("CW-1", "x", func(r *sqlstore.JudgmentRecord) { r.QuestionKey = "other_question" })
	_, err = store.RecordJudgment(other)
	require.NoError(t, err)

	fresh := judgment("CW-1", "new-state")
	created, err := store.RecordJudgment(fresh)
	require.NoError(t, err)
	require.True(t, created)

	byID := func(id int64) string {
		j, err := store.GetJudgment(id)
		require.NoError(t, err)
		return j.Disposition
	}
	assert.Equal(t, sqlstore.JudgmentSuperseded, byID(open.ID))
	assert.Equal(t, sqlstore.JudgmentRejected, byID(decided.ID), "a decided row never changes")
	assert.Equal(t, sqlstore.JudgmentSuggested, byID(other.ID), "another question is untouched")
	assert.Equal(t, sqlstore.JudgmentSuggested, byID(fresh.ID))
}

func TestRewordedSetSupersedesTheOldSuggestionForTheSameState(t *testing.T) {
	store := setupTestStore(t)
	v1 := judgment("CW-1", "same-state")
	_, err := store.RecordJudgment(v1)
	require.NoError(t, err)
	v2 := judgment("CW-1", "same-state", func(r *sqlstore.JudgmentRecord) { r.SetHash, r.SetVersion = "set-b", 2 })
	created, err := store.RecordJudgment(v2)
	require.NoError(t, err)
	assert.True(t, created, "new wording is a new row even for the same state")
	old, _ := store.GetJudgment(v1.ID)
	assert.Equal(t, sqlstore.JudgmentSuperseded, old.Disposition)
}

func TestDecideJudgmentOnlyFromSuggested(t *testing.T) {
	store := setupTestStore(t)
	r := judgment("CW-1", "h")
	_, err := store.RecordJudgment(r)
	require.NoError(t, err)

	require.NoError(t, store.DecideJudgment(r.ID, sqlstore.JudgmentAccepted, "chrispian"))
	got, _ := store.GetJudgment(r.ID)
	assert.Equal(t, "chrispian", got.DecidedBy)
	require.NotNil(t, got.DecidedAt)

	err = store.DecideJudgment(r.ID, sqlstore.JudgmentRejected, "someone")
	assert.True(t, errors.Is(err, sqlstore.ErrJudgmentDecided))
	got, _ = store.GetJudgment(r.ID)
	assert.Equal(t, sqlstore.JudgmentAccepted, got.Disposition)

	assert.True(t, errors.Is(store.DecideJudgment(999, sqlstore.JudgmentAccepted, "x"), sqlstore.ErrJudgmentNotFound))
	assert.Error(t, store.DecideJudgment(r.ID, "superseded", "x"), "superseded is the store's to set, not a decision")
	assert.Error(t, store.DecideJudgment(r.ID, "bogus", "x"))
	_, err = store.GetJudgment(999)
	assert.True(t, errors.Is(err, sqlstore.ErrJudgmentNotFound))
}

func TestListJudgmentsFiltersAndCountsByDisposition(t *testing.T) {
	store := setupTestStore(t)
	a := judgment("CW-1", "h")
	b := judgment("CW-2", "h")
	c := judgment("CW-3", "h", func(r *sqlstore.JudgmentRecord) { r.SetID = "task-layer"; r.QuestionKey = "frontend" })
	for _, r := range []*sqlstore.JudgmentRecord{a, b, c} {
		_, err := store.RecordJudgment(r)
		require.NoError(t, err)
	}
	require.NoError(t, store.DecideJudgment(a.ID, sqlstore.JudgmentAccepted, "x"))
	require.NoError(t, store.DecideJudgment(b.ID, sqlstore.JudgmentRejected, "x"))

	kind, err := store.ListJudgments(sqlstore.JudgmentFilter{SetID: "task-kind"})
	require.NoError(t, err)
	assert.Len(t, kind, 2)
	assert.Equal(t, "CW-2", kind[0].SubjectID, "newest first")
	open, _ := store.ListJudgments(sqlstore.JudgmentFilter{Disposition: sqlstore.JudgmentSuggested})
	assert.Len(t, open, 1)
	one, _ := store.ListJudgments(sqlstore.JudgmentFilter{SetID: "task-kind", Limit: 1})
	assert.Len(t, one, 1)

	counts, err := store.JudgmentCounts("task-kind")
	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]int{"kind": {"accepted": 1, "rejected": 1}}, counts)
}

func TestRecordJudgmentRequiresItsKey(t *testing.T) {
	store := setupTestStore(t)
	for name, mut := range map[string]func(*sqlstore.JudgmentRecord){
		"subject": func(r *sqlstore.JudgmentRecord) { r.SubjectID = "" },
		"set":     func(r *sqlstore.JudgmentRecord) { r.SetHash = "" },
		"state":   func(r *sqlstore.JudgmentRecord) { r.StateHash = "" },
	} {
		_, err := store.RecordJudgment(judgment("CW-1", "h", mut))
		assert.Error(t, err, name)
	}
}
