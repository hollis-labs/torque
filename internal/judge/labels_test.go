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

func TestNoulLabelsTreatAnUntaggedItemAsUnlabeled(t *testing.T) {
	sets, _ := LoadSets()
	set := sets["task-layer"] // frontend and backend, label tags [frontend] and [backend]
	ex := LabelExamples(set, items(map[string][]string{
		"fe":       {"frontend", "ui"},
		"be":       {"backend"},
		"other":    {"docs"},
		"untagged": nil,
	}), SampleOptions{N: 100, Seed: 1})
	labels := map[string]map[string]bool{}
	for _, e := range ex {
		for k, l := range e.Labels {
			if labels[k] == nil {
				labels[k] = map[string]bool{}
			}
			labels[k][e.ID] = l.Yes
		}
	}
	assert.Equal(t, map[string]bool{"fe": true, "be": false, "other": false}, labels["frontend"], "an untagged item is not a negative")
	assert.Equal(t, map[string]bool{"be": true, "fe": false, "other": false}, labels["backend"])
	for _, e := range ex {
		assert.NotEqual(t, "untagged", e.ID)
	}
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
