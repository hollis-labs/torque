package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// The planner importer reads the portfolio-tracker's data/*.json and writes
// the items into Torque keeping ids, rev, order, created/updated, comments,
// links and cross-references exactly. It is the one write path that does not
// reset rev or timestamps.
//
// Re-running is safe: an item already in Torque is left alone when identical,
// refreshed when the source is newer (higher rev), and reported, not touched,
// when Torque's copy is newer or the two diverge at the same rev. Without
// Apply nothing is written; the report is the parity check.

// PlannerImportOptions controls one import.
type PlannerImportOptions struct {
	Dir   string // the portfolio-tracker data directory
	Apply bool   // false = report only
	Force bool   // overwrite items whose Torque copy is newer or diverged
}

// Import actions, per item.
const (
	PlannerImportCreate   = "create"
	PlannerImportUpdate   = "update"
	PlannerImportSame     = "unchanged"
	PlannerImportNewer    = "skip-torque-newer"
	PlannerImportDiverged = "skip-diverged"
	PlannerImportInvalid  = "invalid"
)

// PlannerImportItem is one item that needs attention (anything but a plain
// create or unchanged).
type PlannerImportItem struct {
	DB     string   `json:"db"`
	ID     string   `json:"id"`
	Action string   `json:"action"`
	Fields []string `json:"fields,omitempty"` // fields that differ
	Detail string   `json:"detail,omitempty"`
}

// PlannerImportDB is the per-database tally.
type PlannerImportDB struct {
	DB            string `json:"db"`
	File          string `json:"file"`
	Source        int    `json:"source"`
	InTorque      int    `json:"in_torque"` // before this run
	Create        int    `json:"create"`
	Update        int    `json:"update"`
	Unchanged     int    `json:"unchanged"`
	SkipNewer     int    `json:"skip_torque_newer"`
	SkipDiverged  int    `json:"skip_diverged"`
	Invalid       int    `json:"invalid"`
	Note          string `json:"note,omitempty"`
	CommentsInSrc int    `json:"comments"`
	LinksInSrc    int    `json:"links"`
}

// PlannerImportReport is the outcome: counts, the items that need a look, and
// (after Apply) the read-back verification.
type PlannerImportReport struct {
	Dir        string              `json:"dir"`
	Applied    bool                `json:"applied"`
	DBs        []PlannerImportDB   `json:"databases"`
	Attention  []PlannerImportItem `json:"attention"`
	Verified   bool                `json:"verified"`   // Apply only: every written item reads back equal to its source
	Mismatches []PlannerImportItem `json:"mismatches"` // Apply only
}

// Clean reports whether nothing needs attention: no invalid, skipped or
// mismatching items.
func (r *PlannerImportReport) Clean() bool {
	return len(r.Attention) == 0 && len(r.Mismatches) == 0
}

type plannerEnvelope struct {
	Schema string           `json:"schema"`
	Items  []map[string]any `json:"items"`
}

// plannerNormalize makes an item comparable across a round trip: numbers as
// float64, empty strings, empty arrays, nulls and mvp:false dropped (the store
// keeps no key for them), and a missing rev as 1 (the tracker's own migration rule).
func plannerNormalize(item PlannerItem) PlannerItem {
	b, _ := json.Marshal(item)
	var m PlannerItem
	_ = json.Unmarshal(b, &m)
	var strip func(v any) any
	strip = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, e := range x {
				if s := strip(e); s != nil {
					out[k] = s
				}
			}
			if len(out) == 0 {
				return nil
			}
			return out
		case []any:
			if len(x) == 0 {
				return nil
			}
			out := make([]any, 0, len(x))
			for _, e := range x {
				if s := strip(e); s != nil {
					out = append(out, s)
				} else {
					out = append(out, e)
				}
			}
			return out
		case string:
			if x == "" {
				return nil
			}
		case nil:
			return nil
		}
		return v
	}
	out := PlannerItem{}
	for k, v := range m {
		if s := strip(v); s != nil {
			out[k] = s
		}
	}
	if v, ok := out["mvp"].(bool); ok && !v {
		delete(out, "mvp") // a boolean column: false and absent are the same value
	}
	if r, ok := out["rev"].(float64); !ok || r < 1 {
		out["rev"] = float64(1)
	}
	if c, ok := out["comments"].([]any); ok { // a comment without a kind is a plain comment
		for _, e := range c {
			if cm, ok := e.(map[string]any); ok && cm["kind"] == "comment" {
				delete(cm, "kind")
			}
		}
	}
	return out
}

func plannerDiffFields(a, b PlannerItem) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ImportPlanner runs an import. See PlannerImportOptions.
func (s *PlannerService) ImportPlanner(opts PlannerImportOptions) (*PlannerImportReport, error) {
	if opts.Dir == "" {
		return nil, &ValidationError{Field: "dir", Message: "the portfolio-tracker data directory is required"}
	}
	report := &PlannerImportReport{Dir: opts.Dir}

	// Everything already in Torque, by id across databases (ids are unique across them).
	existing := map[string]struct {
		db   string
		item PlannerItem
	}{}
	inTorque := map[string]int{}
	for _, db := range sqlstore.PlannerDatabases() {
		items, err := s.store.PlannerList(db)
		if err != nil {
			return nil, err
		}
		inTorque[db] = len(items)
		for _, it := range items {
			existing[it["id"].(string)] = struct {
				db   string
				item PlannerItem
			}{db, it}
		}
	}

	type pending struct {
		write sqlstore.PlannerWrite
		src   PlannerItem
	}
	var writes []pending
	seenInSource := map[string]string{} // id -> db, for duplicate detection across files

	for _, db := range sqlstore.PlannerDatabases() {
		tally := PlannerImportDB{DB: db, File: filepath.Join(opts.Dir, db+".json"), InTorque: inTorque[db]}
		raw, err := os.ReadFile(tally.File)
		if errors.Is(err, os.ErrNotExist) {
			tally.Note = "no file; nothing to import"
			report.DBs = append(report.DBs, tally)
			continue
		}
		if err != nil {
			return nil, err
		}
		var env plannerEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, &ValidationError{Field: db, Message: fmt.Sprintf("%s: %v", tally.File, err)}
		}
		if want := "portfolio/" + db + "@1"; env.Schema != want {
			return nil, &ValidationError{Field: db, Message: fmt.Sprintf("%s: schema is %q, want %q", tally.File, env.Schema, want)}
		}
		tally.Source = len(env.Items)

		for _, src := range env.Items {
			id, _ := src["id"].(string)
			attention := func(action, detail string, fields ...string) {
				report.Attention = append(report.Attention, PlannerImportItem{DB: db, ID: id, Action: action, Fields: fields, Detail: detail})
			}
			if id == "" {
				tally.Invalid++
				attention(PlannerImportInvalid, "item has no id")
				continue
			}
			if other, dup := seenInSource[id]; dup {
				tally.Invalid++
				attention(PlannerImportInvalid, "id also appears in "+other)
				continue
			}
			seenInSource[id] = db
			if errs, err := PlannerValidateItem(db, src); err != nil {
				return nil, err
			} else if len(errs) > 0 {
				tally.Invalid++
				attention(PlannerImportInvalid, (&PlannerValidationError{Message: "fails the " + db + " schema", Errors: errs}).Error())
				continue
			}
			tally.CommentsInSrc += len(asAnySlice(src["comments"]))
			tally.LinksInSrc += len(asAnySlice(src["links"]))

			cur, found := existing[id]
			if found && cur.db != db {
				tally.Invalid++
				attention(PlannerImportInvalid, "id already exists in Torque's "+cur.db+" database")
				continue
			}
			if !found {
				tally.Create++
				writes = append(writes, pending{write: sqlstore.PlannerWrite{DB: db, Item: src, Create: true}, src: src})
				continue
			}
			want, have := plannerNormalize(src), plannerNormalize(cur.item)
			diff := plannerDiffFields(want, have)
			if len(diff) == 0 {
				tally.Unchanged++
				continue
			}
			srcRev, curRev := plannerRev(want), plannerRev(have)
			switch {
			case srcRev > curRev || opts.Force:
				tally.Update++
				w := sqlstore.PlannerWrite{DB: db, Item: src, ExpectedRev: plannerRev(cur.item)}
				if plannerRev(src) < 1 { // the tracker counts a missing rev as 0; the store starts at 1
					src = plannerClone(src)
					src["rev"] = 1
					w.Item = src
				}
				writes = append(writes, pending{write: w, src: src})
			case srcRev < curRev:
				tally.SkipNewer++
				attention(PlannerImportNewer, fmt.Sprintf("Torque is at rev %d, source at rev %d", curRev, srcRev), diff...)
			default:
				tally.SkipDiverged++
				attention(PlannerImportDiverged, fmt.Sprintf("both at rev %d but the content differs", curRev), diff...)
			}
		}
		report.DBs = append(report.DBs, tally)
	}

	if !opts.Apply {
		return report, nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	batch := make([]sqlstore.PlannerWrite, len(writes))
	for i, w := range writes {
		batch[i] = w.write
	}
	if len(batch) > 0 {
		if err := s.store.PlannerApply(batch); err != nil {
			return nil, fmt.Errorf("planner import rolled back, nothing written: %w", err)
		}
	}
	report.Applied = true

	// Read every written item back and compare it with its source.
	report.Verified = true
	for _, w := range writes {
		id, _ := w.src["id"].(string)
		got, err := s.store.PlannerGet(w.write.DB, id)
		if err != nil {
			report.Verified = false
			report.Mismatches = append(report.Mismatches, PlannerImportItem{DB: w.write.DB, ID: id, Action: "missing-after-write", Detail: err.Error()})
			continue
		}
		if diff := plannerDiffFields(plannerNormalize(w.src), plannerNormalize(got)); len(diff) > 0 {
			report.Verified = false
			report.Mismatches = append(report.Mismatches, PlannerImportItem{DB: w.write.DB, ID: id, Action: "differs-after-write", Fields: diff})
		}
	}
	return report, nil
}

func asAnySlice(v any) []any {
	s, _ := v.([]any)
	return s
}
