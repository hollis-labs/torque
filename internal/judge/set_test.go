package judge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSetsAreValid(t *testing.T) {
	sets, err := LoadSets()
	require.NoError(t, err)
	assert.Equal(t, []string{"task-decision-state", "task-kind", "task-layer"}, SetIDs(sets))
	for id, s := range sets {
		assert.NoError(t, s.Validate(), id)
		assert.Equal(t, "task_enrichment", s.Purpose, id)
	}
	kind := sets["task-kind"].Questions[0]
	assert.Equal(t, Choice, kind.Type)
	assert.True(t, kind.Shuffle, "the model leans toward the first option, so kind shuffles")
	assert.Contains(t, kind.NoTag, "other")
	// Every kind in the convention is an option, and the labels are the same six.
	var keys []string
	for _, o := range kind.Options {
		keys = append(keys, o.Key)
	}
	assert.ElementsMatch(t, []string{"bug", "feature", "chore", "cleanup", "audit", "docs", "other"}, keys)
	assert.ElementsMatch(t, []string{"bug", "feature", "chore", "cleanup", "audit", "docs"}, kind.LabelTags)
}

func TestSetHashChangesWithTheWording(t *testing.T) {
	sets, err := LoadSets()
	require.NoError(t, err)
	a := sets["task-kind"]
	h := a.Hash()
	assert.Equal(t, h, a.Hash(), "stable")
	b := *a
	b.Questions = append([]SetQuestion(nil), a.Questions...)
	b.Questions[0].Instructions += " (reworded)"
	assert.NotEqual(t, h, b.Hash())
}

func TestHashTracksWhatTheModelSeesAndNothingElse(t *testing.T) {
	sets, err := LoadSets()
	require.NoError(t, err)
	base := sets["task-layer"]
	h := base.Hash()

	clone := func() *Set {
		c := *base
		c.State = append([]StateField(nil), base.State...)
		c.Questions = append([]SetQuestion(nil), base.Questions...)
		return &c
	}
	// Settings Torque owns do not change what the model sees.
	c := clone()
	c.Questions[0].SuggestMin = 0.99
	c.Questions[0].LabelTags = []string{"x"}
	c.Questions[0].NegativeTags = []string{"y"}
	c.Questions[0].Tag = "renamed"
	c.Description = "different"
	assert.Equal(t, h, c.Hash(), "thresholds, labels, tag mapping and description are not wording")

	// Anything the model reads does.
	for name, mutate := range map[string]func(*Set){
		"instructions": func(c *Set) { c.Questions[0].Instructions += "?" },
		"true text":    func(c *Set) { c.Questions[0].True += "." },
		"state limit":  func(c *Set) { c.State[0].MaxChars++ },
		"purpose":      func(c *Set) { c.Purpose = "other" },
		"order":        func(c *Set) { c.Questions[0], c.Questions[1] = c.Questions[1], c.Questions[0] },
	} {
		c := clone()
		mutate(c)
		assert.NotEqual(t, h, c.Hash(), name)
	}
}

func TestValidateRefusesBadSets(t *testing.T) {
	good := `
id: s
version: 1
purpose: p
state: [{name: title, max_chars: 100}]
questions:
  - key: q
    type: noul
    instructions: "Is ` + "`title`" + ` urgent?"
`
	_, err := parseSet([]byte(good))
	require.NoError(t, err)

	opts := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString("      - {key: o" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ", description: d}\n")
		}
		return b.String()
	}
	cases := map[string]string{
		"no id":               strings.Replace(good, "id: s\n", "", 1),
		"version zero":        strings.Replace(good, "version: 1", "version: 0", 1),
		"no purpose":          strings.Replace(good, "purpose: p\n", "", 1),
		"no state":            strings.Replace(good, "state: [{name: title, max_chars: 100}]", "state: []", 1),
		"state without limit": strings.Replace(good, "max_chars: 100", "max_chars: 0", 1),
		"no questions":        "id: s\nversion: 1\npurpose: p\nstate: [{name: t, max_chars: 1}]\nquestions: []\n",
		"empty instructions":  strings.Replace(good, `"Is `+"`title`"+` urgent?"`, `"  "`, 1),
		"unknown type":        strings.Replace(good, "type: noul", "type: essay", 1),
		"suggest_min over 1":  strings.Replace(good, "type: noul", "type: noul\n    suggest_min: 1.5", 1),
		"duplicate key":       good + "  - key: q\n    type: noul\n    instructions: again\n",
		"one option":          strings.Replace(good, "type: noul", "type: choice\n    options:\n      - {key: a, description: d}", 1),
		"256 options":         strings.Replace(good, "type: noul", "type: choice\n    options:\n"+opts(256), 1),
		"duplicate option":    strings.Replace(good, "type: noul", "type: choice\n    options:\n      - {key: a, description: d}\n      - {key: a, description: e}", 1),
		"option no desc":      strings.Replace(good, "type: noul", "type: choice\n    options:\n      - {key: a, description: d}\n      - {key: b, description: ''}", 1),
		"no_tag unknown":      strings.Replace(good, "type: noul", "type: choice\n    no_tag: [zzz]\n    options:\n      - {key: a, description: d}\n      - {key: b, description: e}", 1),
		"one level":           strings.Replace(good, "type: noul", "type: score\n    levels: [low]", 1),
		"eleven levels":       strings.Replace(good, "type: noul", "type: score\n    levels: [a,b,c,d,e,f,g,h,i,j,k]", 1),
		"misspelt key":        strings.Replace(good, "purpose: p", "purpose: p\nsuggest_mim: 0.5", 1),
	}
	for name, src := range cases {
		_, err := parseSet([]byte(src))
		assert.Error(t, err, name)
	}
	// The limits themselves are allowed.
	_, err = parseSet([]byte(strings.Replace(good, "type: noul", "type: choice\n    options:\n"+opts(255), 1)))
	assert.NoError(t, err, "255 options")
	_, err = parseSet([]byte(strings.Replace(good, "type: noul", "type: score\n    levels: [a,b,c,d,e,f,g,h,i,j]", 1)))
	assert.NoError(t, err, "10 levels")
}

func TestBuildRequestCutsStateAndKeepsEveryQuestion(t *testing.T) {
	sets, _ := LoadSets()
	s := sets["task-kind"]
	req := s.BuildRequest(map[string]string{"title": "T", "description": strings.Repeat("é", 5000), "ignored": "x"}, 1)
	state := req.State.(map[string]string)
	assert.Equal(t, "T", state["title"])
	assert.Len(t, []rune(state["description"]), 4000, "cut to the field's limit, by characters")
	assert.NotContains(t, state, "ignored", "only the set's fields are sent")
	require.Len(t, req.Questions, 1)
	assert.Equal(t, "task_enrichment", req.Purpose)
	assert.Positive(t, EstimateTokens(req))
}

func TestShuffleIsDeterministicPerSeedAndOnlyReorders(t *testing.T) {
	sets, _ := LoadSets()
	s := sets["task-kind"]
	order := func(seed int64) []string {
		var keys []string
		for _, o := range s.BuildRequest(nil, seed).Questions[0].Options {
			keys = append(keys, o.Key)
		}
		return keys
	}
	assert.Equal(t, order(1), order(1), "the same seed gives the same order")
	differs := false
	for seed := int64(2); seed < 12; seed++ {
		if strings.Join(order(seed), ",") != strings.Join(order(1), ",") {
			differs = true
		}
		assert.ElementsMatch(t, order(1), order(seed), "same options, only reordered")
	}
	assert.True(t, differs, "different seeds give different orders")

	// The set's own order is never mutated by building a request.
	assert.Equal(t, "bug", s.Questions[0].Options[0].Key)

	// A question that does not shuffle keeps its written order.
	noShuffle := *s
	noShuffle.Questions = append([]SetQuestion(nil), s.Questions...)
	noShuffle.Questions[0].Shuffle = false
	assert.Equal(t, "bug", noShuffle.BuildRequest(nil, 7).Questions[0].Options[0].Key)
}

func TestSuggestedTags(t *testing.T) {
	sets, _ := LoadSets()
	kind := sets["task-kind"].Questions[0]
	assert.Equal(t, []string{"bug"}, kind.SuggestedTags(Answer{Type: Choice, Choice: "bug", Confidence: 0.8}))
	assert.Nil(t, kind.SuggestedTags(Answer{Type: Choice, Choice: "bug", Confidence: 0.3}), "below suggest_min")
	assert.Nil(t, kind.SuggestedTags(Answer{Type: Choice, Choice: "other", Confidence: 0.9}), "other suggests no tag")

	layer := sets["task-layer"].Questions[0] // frontend, noul, suggest_min 0.6
	assert.Equal(t, []string{"frontend"}, layer.SuggestedTags(Answer{Type: Noul, Noul: 0.9, Confidence: NoulConfidence(0.9)}))
	assert.Nil(t, layer.SuggestedTags(Answer{Type: Noul, Noul: 0.7, Confidence: NoulConfidence(0.7)}), "yes, but not confident enough")
	assert.Nil(t, layer.SuggestedTags(Answer{Type: Noul, Noul: 0.05, Confidence: NoulConfidence(0.05)}), "a confident no suggests nothing")

	// The provider's Noul answers carry no confidence field: it must come from the probability.
	assert.Equal(t, []string{"frontend"}, layer.SuggestedTags(Answer{Type: Noul, Noul: 0.92}), "no confidence field, still suggests")
	assert.Nil(t, layer.SuggestedTags(Answer{Type: Noul, Noul: 0.7}), "and still honours the threshold")

	mapped := SetQuestion{Type: Choice, Tags: map[string]string{"doc": "docs"}, SuggestMin: 0.1}
	assert.Equal(t, []string{"docs"}, mapped.SuggestedTags(Answer{Type: Choice, Choice: "doc", Confidence: 0.5}), "an option can map to a different tag")
}

func TestDisabledQuestionsAreAskedByEvalButNeverSuggested(t *testing.T) {
	sets, err := LoadSets()
	require.NoError(t, err)
	set := sets["task-decision-state"]
	fields := map[string]string{"title": "t", "description": "d"}

	all := set.BuildRequest(fields, 1)
	sug, ok := set.BuildSuggestionRequest(fields, 1)
	require.True(t, ok)
	assert.Len(t, all.Questions, 3)
	require.Len(t, sug.Questions, 1)
	assert.Equal(t, "discuss_first", sug.Questions[0].Key)

	q, _ := set.Question("needs_review")
	assert.True(t, q.Disabled)
	assert.Empty(t, q.SuggestedTags(Answer{Type: Noul, Noul: 0.99}))
}
