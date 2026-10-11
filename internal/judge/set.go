package judge

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Question sets are versioned files in the repo, reviewed like code. A set
// names the fields of an item that are sent as state, the questions with
// their criteria, how an answer maps to a suggested tag, and the threshold
// above which a suggestion is shown. Bump Version when the wording changes:
// the ledger records which version produced an answer.

//go:embed sets/*.yaml
var setFS embed.FS

// Limits from the TypeSafe API reference.
const (
	maxOptions = 255
	minLevels  = 2
	maxLevels  = 10
)

// StateField is one field of an item sent as state, cut to MaxChars.
type StateField struct {
	Name     string `yaml:"name"`
	MaxChars int    `yaml:"max_chars"`
}

// SetQuestion is a question as written in a set file.
type SetQuestion struct {
	Key          string       `yaml:"key"`
	Type         QuestionType `yaml:"type"`
	Instructions string       `yaml:"instructions"`
	Options      []Option     `yaml:"options"`
	Levels       []string     `yaml:"levels"`
	True         string       `yaml:"true"`
	False        string       `yaml:"false"`

	// Shuffle reorders a Choice's options per request (seeded), because the
	// model leans toward the first option.
	Shuffle bool `yaml:"shuffle"`

	// Tag maps an answer to a suggested tag: a Noul suggests Tag when it says
	// yes; a Choice suggests the tag named for the winning option in Tags
	// (default: the option key), except options listed in NoTag.
	Tag   string            `yaml:"tag"`
	Tags  map[string]string `yaml:"tags"`
	NoTag []string          `yaml:"no_tag"`

	// SuggestMin is the confidence at or above which a suggestion is shown.
	// Starting values are conservative guesses; the evaluation sets them.
	SuggestMin float64 `yaml:"suggest_min"`

	// LabelTags says how an evaluation label is read from the item's existing
	// tags (see docs/task-tagging-conventions.md): for a Choice, the single
	// option-key tag present; for a Noul, the question's Tag present or not.
	LabelTags []string `yaml:"label_tags"`
}

// Set is a versioned collection of questions about one kind of item.
type Set struct {
	ID          string        `yaml:"id"`
	Version     int           `yaml:"version"`
	Purpose     string        `yaml:"purpose"` // groups spend and audit in the gateway
	AppliesTo   string        `yaml:"applies_to"`
	Description string        `yaml:"description"`
	State       []StateField  `yaml:"state"`
	Questions   []SetQuestion `yaml:"questions"`
}

// Question returns the question with this key.
func (s *Set) Question(key string) (SetQuestion, bool) {
	for _, q := range s.Questions {
		if q.Key == key {
			return q, true
		}
	}
	return SetQuestion{}, false
}

// Validate checks a set against the provider's limits and the set's own rules.
func (s *Set) Validate() error {
	if s.ID == "" || s.Version < 1 || s.Purpose == "" {
		return fmt.Errorf("set %q: id, version >= 1 and purpose are required", s.ID)
	}
	if len(s.State) == 0 {
		return fmt.Errorf("set %s: at least one state field is required", s.ID)
	}
	for _, f := range s.State {
		if f.Name == "" || f.MaxChars < 1 {
			return fmt.Errorf("set %s: state field %q needs a name and max_chars >= 1", s.ID, f.Name)
		}
	}
	if len(s.Questions) == 0 {
		return fmt.Errorf("set %s: at least one question is required", s.ID)
	}
	seen := map[string]bool{}
	for _, q := range s.Questions {
		if q.Key == "" || seen[q.Key] {
			return fmt.Errorf("set %s: question key %q is empty or duplicated", s.ID, q.Key)
		}
		seen[q.Key] = true
		if strings.TrimSpace(q.Instructions) == "" {
			return fmt.Errorf("set %s question %s: instructions are required", s.ID, q.Key)
		}
		if q.SuggestMin < 0 || q.SuggestMin > 1 {
			return fmt.Errorf("set %s question %s: suggest_min must be 0..1", s.ID, q.Key)
		}
		switch q.Type {
		case Choice:
			if len(q.Options) < 2 || len(q.Options) > maxOptions {
				return fmt.Errorf("set %s question %s: a choice needs 2..%d options", s.ID, q.Key, maxOptions)
			}
			keys := map[string]bool{}
			for _, o := range q.Options {
				if o.Key == "" || o.Description == "" || keys[o.Key] {
					return fmt.Errorf("set %s question %s: option %q needs a unique key and a description", s.ID, q.Key, o.Key)
				}
				keys[o.Key] = true
			}
			for _, k := range q.NoTag {
				if !keys[k] {
					return fmt.Errorf("set %s question %s: no_tag names unknown option %q", s.ID, q.Key, k)
				}
			}
			for k := range q.Tags {
				if !keys[k] {
					return fmt.Errorf("set %s question %s: tags names unknown option %q", s.ID, q.Key, k)
				}
			}
		case Score:
			if len(q.Levels) < minLevels || len(q.Levels) > maxLevels {
				return fmt.Errorf("set %s question %s: a score needs %d..%d levels", s.ID, q.Key, minLevels, maxLevels)
			}
		case Noul:
			if q.Tag == "" && len(q.LabelTags) == 0 {
				// A noul with no tag still works; it just suggests nothing.
				break
			}
		default:
			return fmt.Errorf("set %s question %s: unknown type %q", s.ID, q.Key, q.Type)
		}
	}
	return nil
}

// Hash identifies the exact wording of a set, for the ledger.
func (s *Set) Hash() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// LoadSets reads and validates every embedded set, by id.
func LoadSets() (map[string]*Set, error) {
	entries, err := setFS.ReadDir("sets")
	if err != nil {
		return nil, err
	}
	out := map[string]*Set{}
	for _, e := range entries {
		raw, err := setFS.ReadFile("sets/" + e.Name())
		if err != nil {
			return nil, err
		}
		s, err := parseSet(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if _, dup := out[s.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate set id %q", e.Name(), s.ID)
		}
		out[s.ID] = s
	}
	return out, nil
}

// parseSet decodes and validates one set file.
func parseSet(raw []byte) (*Set, error) {
	var s Set
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // a misspelt key is an error, not a silently ignored setting
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// SetIDs lists the embedded set ids, sorted.
func SetIDs(sets map[string]*Set) []string {
	ids := make([]string, 0, len(sets))
	for id := range sets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BuildRequest renders one item into a Request: the state fields cut to their
// limits, and every question in the set. A Choice with Shuffle set has its
// options reordered by a permutation derived from seed, so the same seed
// always gives the same order and a different seed a different one.
func (s *Set) BuildRequest(fields map[string]string, seed int64) Request {
	state := make(map[string]string, len(s.State))
	for _, f := range s.State {
		state[f.Name] = truncate(Scrub(fields[f.Name]), f.MaxChars) // scrub first: a token cut by the limit would lose its tail and stop matching
	}
	req := Request{State: state, Purpose: s.Purpose}
	for _, sq := range s.Questions {
		q := Question{Key: sq.Key, Type: sq.Type, Instructions: sq.Instructions, Levels: sq.Levels, True: sq.True, False: sq.False}
		if sq.Type == Choice {
			q.Options = append([]Option(nil), sq.Options...)
			if sq.Shuffle {
				shuffleOptions(q.Options, seed, s.ID+"/"+sq.Key)
			}
		}
		req.Questions = append(req.Questions, q)
	}
	return req
}

func shuffleOptions(opts []Option, seed int64, salt string) {
	h := sha256.Sum256([]byte(salt))
	r := rand.New(rand.NewSource(seed ^ int64(binary.BigEndian.Uint64(h[:8]))))
	r.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// RequestOverheadTokens is the fixed per-request input cost the provider
// adds on top of the text we send. Observed on the first live call (2026-10-11):
// 282 input tokens for a ten-word state and one short question.
const RequestOverheadTokens = 250

// InputPricePerMTokUSD is JEV's price per million input tokens (docs, 2026-10);
// output is free.
const InputPricePerMTokUSD = 0.042

// CostUSD is the input cost of n tokens at the JEV price.
func CostUSD(inputTokens int) float64 {
	return float64(inputTokens) * InputPricePerMTokUSD / 1e6
}

// EstimateTokens is a rough input-token count for a request: about four
// characters per token of what we send, plus the provider's fixed overhead.
// Good enough to budget an evaluation; the provider reports the real figure.
func EstimateTokens(req Request) int {
	b, _ := json.Marshal(req)
	return (len(b)+3)/4 + RequestOverheadTokens
}

// SuggestedTags returns the tags an answer suggests under a question's
// mapping: a Noul at or above SuggestMin confidence that says yes, or a Choice
// at or above it whose winner is not excluded.
func (q SetQuestion) SuggestedTags(a Answer) []string {
	switch q.Type {
	case Noul:
		// A Noul answer has no confidence field of its own: derive it from the probability.
		if q.Tag != "" && a.Noul >= 0.5 && NoulConfidence(a.Noul) >= q.SuggestMin {
			return []string{q.Tag}
		}
	case Choice:
		if a.Confidence < q.SuggestMin || a.Choice == "" {
			return nil
		}
		for _, skip := range q.NoTag {
			if skip == a.Choice {
				return nil
			}
		}
		if t, ok := q.Tags[a.Choice]; ok {
			return []string{t}
		}
		return []string{a.Choice}
	}
	return nil
}
