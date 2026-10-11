package judge

import (
	"math/rand"
	"sort"
)

// Item is one thing a set can be asked about, with the tags it already
// carries. Existing tags are the evaluation labels: applied by many agents and
// people over time, so they are real but noisy. Disagreements are therefore
// reported for review, not just counted as errors.
type Item struct {
	ID     string
	Fields map[string]string
	Tags   []string
}

// SampleOptions controls how labeled examples are drawn.
type SampleOptions struct {
	// N is the sample size per question (default 200). A Choice draws up to N
	// labeled items at random, so its accuracy reflects the natural mix of
	// kinds. A Noul draws up to N/2 positives and N/2 negatives: positives
	// (tasks carrying a rare tag) would otherwise be missing from a random
	// sample, so its accuracy is on a balanced mix, not the natural one.
	N    int
	Seed int64
}

// LabelExamples draws labeled examples for every question in the set from the
// items' existing tags. A question's label comes from its LabelTags:
//
//   - Choice: the item carries exactly one of the label tags, and that tag is
//     one of the question's option keys. Zero or several means unlabeled.
//   - Noul: yes when the item carries the tag, no when it carries other tags
//     but not this one. An item with no tags at all is unlabeled: an absent
//     tag on an untagged item says nothing.
//
// The same seed always draws the same sample.
func LabelExamples(set *Set, items []Item, opt SampleOptions) []Example {
	if opt.N <= 0 {
		opt.N = 200
	}
	sorted := append([]Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	byID := map[string]*Example{}
	label := func(it Item, key string, l Label) {
		ex := byID[it.ID]
		if ex == nil {
			ex = &Example{ID: it.ID, Fields: it.Fields, Labels: map[string]Label{}}
			byID[it.ID] = ex
		}
		ex.Labels[key] = l
	}

	for qi, q := range set.Questions {
		rng := rand.New(rand.NewSource(opt.Seed + int64(qi)*7919))
		switch q.Type {
		case Choice:
			options := map[string]bool{}
			for _, o := range q.Options {
				options[o.Key] = true
			}
			var pool []Item
			var tagOf []string
			for _, it := range sorted {
				if t, ok := onlyOne(it.Tags, q.LabelTags); ok && options[t] {
					pool = append(pool, it)
					tagOf = append(tagOf, t)
				}
			}
			for _, i := range draw(rng, len(pool), opt.N) {
				label(pool[i], q.Key, Label{Choice: tagOf[i]})
			}
		case Noul:
			if len(q.LabelTags) == 0 {
				continue
			}
			tag := q.LabelTags[0]
			var pos, neg []Item
			for _, it := range sorted {
				switch {
				case has(it.Tags, tag):
					pos = append(pos, it)
				case len(it.Tags) > 0:
					neg = append(neg, it)
				}
			}
			for _, i := range draw(rng, len(pos), opt.N/2) {
				label(pos[i], q.Key, Label{Yes: true})
			}
			for _, i := range draw(rng, len(neg), opt.N/2) {
				label(neg[i], q.Key, Label{Yes: false})
			}
		}
	}

	out := make([]Example, 0, len(byID))
	for _, ex := range byID {
		out = append(out, *ex)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// draw picks up to n distinct indices out of total, in a seeded random order.
func draw(rng *rand.Rand, total, n int) []int {
	idx := rng.Perm(total)
	if n < len(idx) {
		idx = idx[:n]
	}
	sort.Ints(idx)
	return idx
}

func has(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// onlyOne reports the single tag of tags that is in the wanted list, and
// whether there was exactly one.
func onlyOne(tags, wanted []string) (string, bool) {
	found := ""
	n := 0
	for _, w := range wanted {
		if has(tags, w) {
			found = w
			n++
		}
	}
	return found, n == 1
}
