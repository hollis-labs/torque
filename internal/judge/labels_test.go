package judge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func items(spec map[string][]string) []Item {
	var out []Item
	for id, tags := range spec {
		out = append(out, Item{ID: id, Fields: map[string]string{"title": id}, Tags: tags})
	}
	return out
}

func TestChoiceLabelsNeedExactlyOneKindTag(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-kind"]
	ex := LabelExamples(set, items(map[string][]string{
		"one-bug":     {"bug", "backend"},
		"one-docs":    {"docs"},
		"two-kinds":   {"bug", "feature"}, // ambiguous: unlabeled
		"no-kind":     {"backend", "nanite"},
		"untagged":    nil,
		"not-a-kind":  {"other"}, // "other" is an option but not a label tag
		"another-bug": {"bug"},
	}), SampleOptions{N: 100, Seed: 1})
	got := map[string]string{}
	for _, e := range ex {
		got[e.ID] = e.Labels["kind"].Choice
	}
	assert.Equal(t, map[string]string{"one-bug": "bug", "one-docs": "docs", "another-bug": "bug"}, got)
}

func labelsFor(t *testing.T, setID string, spec map[string][]string) map[string]map[string]bool {
	sets, err := LoadSets()
	require.NoError(t, err)
	out := map[string]map[string]bool{}
	for _, e := range LabelExamples(sets[setID], items(spec), SampleOptions{N: 100, Seed: 1}) {
		for k, l := range e.Labels {
			if out[k] == nil {
				out[k] = map[string]bool{}
			}
			out[k][e.ID] = l.Yes
		}
	}
	return out
}

func TestLayerLabelsUseRealSynonymsAndTheOppositeLayerAsTheNegative(t *testing.T) {
	labels := labelsFor(t, "task-layer", map[string][]string{
		"fe":        {"frontend", "ui"},
		"fe-short":  {"fe"},
		"ui-only":   {"ui"},
		"be":        {"backend"},
		"be-short":  {"be"},
		"api":       {"api", "pagination"},
		"fullstack": {"frontend", "backend"}, // both layers: positive for each
		"docs":      {"docs"},                // neither layer: unlabeled, not a negative
		"scheduler": {"scheduler"},           // no layer tag at all: unlabeled
		"untagged":  nil,
	})
	assert.Equal(t, map[string]bool{
		"fe": true, "fe-short": true, "ui-only": true, "fullstack": true, // fe, ui and frontend all mean frontend
		"be": false, "be-short": false, "api": false, // an explicit backend-only task is a deliberate no
	}, labels["frontend"])
	assert.Equal(t, map[string]bool{
		"be": true, "be-short": true, "api": true, "fullstack": true,
		"fe": false, "fe-short": false, "ui-only": false,
	}, labels["backend"])
}

func TestDecisionStateLabelsAcceptTheCorpusSynonyms(t *testing.T) {
	labels := labelsFor(t, "task-decision-state", map[string][]string{
		"canonical":  {"discuss-first"},
		"discussion": {"needs-discussion"},
		"decision":   {"needs-decision", "mcp"},
		"design":     {"design-first"},
		"other":      {"backend"}, // another tag, none of the synonyms: a negative (no negative_tags on this question)
		"untagged":   nil,         // unlabeled
	})
	assert.Equal(t, map[string]bool{"canonical": true, "discussion": true, "decision": true, "design": true, "other": false}, labels["discuss_first"])
}

func TestNoulSamplesAreBalancedEvenWhenPositivesAreRare(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-decision-state"]
	spec := map[string][]string{}
	for i := 0; i < 500; i++ {
		spec[fmt.Sprintf("t%03d", i)] = []string{"backend"}
	}
	for i := 0; i < 12; i++ {
		spec[fmt.Sprintf("p%02d", i)] = []string{"discuss-first", "backend"}
	}
	ex := LabelExamples(set, items(spec), SampleOptions{N: 100, Seed: 3})
	pos, neg := 0, 0
	for _, e := range ex {
		if l, ok := e.Labels["discuss_first"]; ok {
			if l.Yes {
				pos++
			} else {
				neg++
			}
		}
	}
	assert.Equal(t, 12, pos, "every rare positive is used")
	assert.Equal(t, 50, neg, "negatives fill the other half")
}

func TestSamplingIsDeterministicPerSeedAndCapped(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-kind"]
	spec := map[string][]string{}
	for i := 0; i < 300; i++ {
		spec[fmt.Sprintf("t%03d", i)] = []string{[]string{"bug", "feature", "docs"}[i%3]}
	}
	a := LabelExamples(set, items(spec), SampleOptions{N: 50, Seed: 1})
	b := LabelExamples(set, items(spec), SampleOptions{N: 50, Seed: 1})
	c := LabelExamples(set, items(spec), SampleOptions{N: 50, Seed: 2})
	require.Len(t, a, 50)
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	for i := 1; i < len(a); i++ {
		assert.Less(t, a[i-1].ID, a[i].ID, "sorted by id")
	}
	assert.Len(t, LabelExamples(set, items(spec), SampleOptions{}), 200, "the default sample is 200")
}

func TestLabelExamplesOnAnEmptyCorpus(t *testing.T) {
	sets, _ := LoadSets()
	assert.Empty(t, LabelExamples(sets["task-kind"], nil, SampleOptions{}))
}
