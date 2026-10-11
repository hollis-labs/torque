package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// PlannerService is the portfolio planner: the operations of the
// portfolio-tracker service (its CONTRACT.md), over Torque's store. Every
// method gates on the "planner" feature flag. Items are JSON-shaped maps
// validated against the tracker's schemas (plannerschema/).
type PlannerService struct {
	store   *sqlstore.Store
	feature *FeatureService
	now     func() time.Time

	// writeMu queues this process's writes; the rev-checked retry loops stay
	// for contention from another process (a CLI beside the server).
	writeMu sync.Mutex
}

// PlannerItem is one planner item as a JSON object.
type PlannerItem = map[string]any

// DefaultPlannerAuthor is the author when neither the call nor the caller names one.
const DefaultPlannerAuthor = "portfolio-manager"

// plannerRetries bounds the re-read-and-retry loop when a concurrent write
// bumps an item between our read and our write.
const plannerRetries = 4

type plannerDB struct {
	prefix string
	seq    bool // sequential ids (PR-01); otherwise prefix + slug of the title
}

var plannerDBs = map[string]plannerDB{
	"priorities":  {"PR-", true},
	"workstreams": {"WS-", false},
	"roadmap":     {"RM-", false},
	"ideas":       {"ID-", false},
	"decisions":   {"DEC-", true},
	"risks":       {"RK-", true},
	"inbox":       {"IN-", true},
}

var plannerDefaults = map[string]PlannerItem{
	"priorities":  {"status": "queued"},
	"workstreams": {"status": "planned"},
	"roadmap":     {"status": "idea"},
	"ideas":       {"status": "new"},
	"decisions":   {"status": "needs-decision"},
	"risks":       {"status": "open"},
	"inbox":       {"status": "new", "kind": "topic"},
}

var plannerPromoteDefaults = map[string]PlannerItem{
	"ideas":   {"kind": "idea"},
	"risks":   {"kind": "risk", "severity": "medium"},
	"roadmap": {"area": "process", "horizon": "someday"},
}

var (
	plannerForbiddenPatch = []string{"id", "created", "comments", "rev"}
	plannerRefField       = regexp.MustCompile(`^(related_ids|depends_on|[a-z][a-z_]*_ids)$`)
	plannerTorqueID       = regexp.MustCompile(`^[A-Z]{2,6}-\d{8}-\d{3,6}$`)
	plannerURL            = regexp.MustCompile(`(?i)^https?://\S+$`)
)

// PlannerValidationError carries every schema failure of a write. It unwraps
// to a ValidationError so generic handlers still map it.
type PlannerValidationError struct {
	Message string
	Errors  []PlannerFieldError
}

func (e *PlannerValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, f := range e.Errors {
		parts[i] = f.Path + " " + f.Message
	}
	return e.Message + ": " + strings.Join(parts, "; ")
}

func (e *PlannerValidationError) Unwrap() error {
	field := ""
	if len(e.Errors) > 0 {
		field = e.Errors[0].Path
	}
	return &ValidationError{Field: field, Message: e.Error()}
}

// PlannerConflictError is a conflict that carries the item as it now is
// (a stale rev, or a duplicate link). It unwraps to a ConflictError.
type PlannerConflictError struct {
	Message string
	Current PlannerItem
}

func (e *PlannerConflictError) Error() string { return "conflict: " + e.Message }
func (e *PlannerConflictError) Unwrap() error { return &ConflictError{Message: e.Message} }

func (s *PlannerService) gate() error { return s.feature.Require("planner") }

func (s *PlannerService) today() string { return s.now().UTC().Format("2006-01-02") }

func plannerClone(v PlannerItem) PlannerItem {
	b, _ := json.Marshal(v)
	var out PlannerItem
	_ = json.Unmarshal(b, &out)
	return out
}

func plannerRev(item PlannerItem) int {
	switch n := item["rev"].(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func (s *PlannerService) bump(item PlannerItem) {
	item["rev"] = plannerRev(item) + 1
	item["updated"] = s.today()
}

func plannerAuthor(actor string, explicit ...string) string {
	for _, a := range explicit {
		if a != "" {
			return a
		}
	}
	if actor != "" {
		return actor
	}
	return DefaultPlannerAuthor
}

func (s *PlannerService) db(name string) error {
	if _, ok := plannerDBs[name]; !ok {
		return &NotFoundError{Entity: "planner database", ID: name}
	}
	return nil
}

func (s *PlannerService) check(db string, item PlannerItem) error {
	errs, err := PlannerValidateItem(db, item)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		id, _ := item["id"].(string)
		if id == "" {
			id = "(new)"
		}
		return &PlannerValidationError{Message: fmt.Sprintf("%s item %s failed validation", db, id), Errors: errs}
	}
	return nil
}

func (s *PlannerService) fetch(db, id string) (PlannerItem, error) {
	if err := s.db(db); err != nil {
		return nil, err
	}
	item, err := s.store.PlannerGet(db, id)
	if errors.Is(err, sqlstore.ErrPlannerNotFound) {
		return nil, &NotFoundError{Entity: "planner item", ID: db + "/" + id}
	}
	if err != nil {
		return nil, err
	}
	return plannerClone(item), nil
}

// mapWriteErr turns store write errors into service errors; current is
// re-read for conflicts so the caller can see what won.
func (s *PlannerService) mapWriteErr(db, id string, err error) error {
	switch {
	case errors.Is(err, sqlstore.ErrPlannerNotFound):
		return &NotFoundError{Entity: "planner item", ID: db + "/" + id}
	case errors.Is(err, sqlstore.ErrPlannerConflict):
		cur, _ := s.fetch(db, id)
		return &PlannerConflictError{Message: err.Error(), Current: cur}
	}
	return err
}

// mutate reads an item, applies fn, bumps rev, validates and writes it with
// its comments and links, retrying from a fresh read when another write got
// in between. fn must be a pure function of the item it is given.
func (s *PlannerService) mutate(db, id string, fn func(PlannerItem) error) (PlannerItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for attempt := 0; ; attempt++ {
		item, err := s.fetch(db, id)
		if err != nil {
			return nil, err
		}
		read := plannerRev(item)
		if err := fn(item); err != nil {
			return nil, err
		}
		s.bump(item)
		if err := s.check(db, item); err != nil {
			return nil, err
		}
		err = s.store.PlannerApply([]sqlstore.PlannerWrite{{DB: db, Item: item, ExpectedRev: read}})
		if errors.Is(err, sqlstore.ErrPlannerConflict) && attempt < plannerRetries {
			continue
		}
		if err != nil {
			return nil, s.mapWriteErr(db, id, err)
		}
		return plannerClone(item), nil
	}
}

// ---------- reads ----------

// PlannerDatabaseInfo describes one database for the databases listing.
type PlannerDatabaseInfo struct {
	Name    string `json:"name"`
	Prefix  string `json:"prefix"`
	Schema  string `json:"schema"`
	Updated string `json:"updated"`
	Count   int    `json:"count"`
}

// Databases lists the databases with id prefix, item count and the latest
// updated date of their items.
func (s *PlannerService) Databases() ([]PlannerDatabaseInfo, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	var out []PlannerDatabaseInfo
	for _, name := range sqlstore.PlannerDatabases() {
		items, err := s.store.PlannerList(name)
		if err != nil {
			return nil, err
		}
		info := PlannerDatabaseInfo{Name: name, Prefix: plannerDBs[name].prefix, Schema: "portfolio/" + name + "@1", Count: len(items)}
		for _, it := range items {
			if u, _ := it["updated"].(string); u > info.Updated {
				info.Updated = u
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func plannerMatches(item PlannerItem, filters map[string]any) bool {
	for k, want := range filters {
		v, ok := item[k]
		if !ok {
			return false
		}
		if arr, isArr := v.([]any); isArr {
			found := false
			for _, e := range arr {
				if fmt.Sprint(e) == fmt.Sprint(want) {
					found = true
				}
			}
			if !found {
				return false
			}
			continue
		}
		if fmt.Sprint(v) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

// List returns the items of a database in manual order (unordered last),
// optionally filtered (top-level fields match exactly, array fields match
// membership) and sorted by a field ("-field" for descending).
func (s *PlannerService) List(db string, filters map[string]any, sortBy string) ([]PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if err := s.db(db); err != nil {
		return nil, err
	}
	all, err := s.store.PlannerList(db)
	if err != nil {
		return nil, err
	}
	items := make([]PlannerItem, 0, len(all))
	for _, it := range all {
		it = plannerClone(it)
		if plannerMatches(it, filters) {
			items = append(items, it)
		}
	}
	if sortBy != "" {
		desc := strings.HasPrefix(sortBy, "-")
		key := strings.TrimPrefix(sortBy, "-")
		less := func(a, b PlannerItem) bool {
			av, aok := a[key]
			bv, bok := b[key]
			switch {
			case !aok || !bok:
				return aok && !bok // a missing value sorts last, either direction
			}
			if af, ok := av.(float64); ok {
				if bf, ok := bv.(float64); ok {
					return af < bf
				}
			}
			return fmt.Sprint(av) < fmt.Sprint(bv)
		}
		sort.SliceStable(items, func(i, j int) bool {
			_, iok := items[i][key]
			_, jok := items[j][key]
			if iok != jok {
				return iok
			}
			if desc {
				return less(items[j], items[i])
			}
			return less(items[i], items[j])
		})
	}
	return items, nil
}

// Get returns one item with its comments and links.
func (s *PlannerService) Get(db, id string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.fetch(db, id)
}

// Schema describes one database's item schema: required fields, every known
// field, the enums by field and the accepted link kinds.
func (s *PlannerService) Schema(db string) (*PlannerSchemaInfo, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if err := s.db(db); err != nil {
		return nil, err
	}
	return plannerDescribe(db)
}

// PlannerHit is one search result.
type PlannerHit struct {
	DB     string `json:"db"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
}

// Search is a case-insensitive search: every term must appear in the item's
// text fields (strings, string arrays, and the text or label of object
// arrays such as comments and options). db is optional.
func (s *PlannerService) Search(q, db string) ([]PlannerHit, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil, &ValidationError{Field: "q", Message: "q is empty"}
	}
	names := sqlstore.PlannerDatabases()
	if db != "" {
		if err := s.db(db); err != nil {
			return nil, err
		}
		names = []string{db}
	}
	var hits []PlannerHit
	for _, name := range names {
		items, err := s.store.PlannerList(name)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			var parts []string
			for _, v := range it {
				switch x := v.(type) {
				case string:
					parts = append(parts, x)
				case []any:
					for _, e := range x {
						switch y := e.(type) {
						case string:
							parts = append(parts, y)
						case map[string]any:
							t, _ := y["text"].(string)
							l, _ := y["label"].(string)
							parts = append(parts, t, l)
						}
					}
				}
			}
			hay := strings.ToLower(strings.Join(parts, "\n"))
			ok := true
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					ok = false
					break
				}
			}
			if ok {
				h := PlannerHit{DB: name, ID: it["id"].(string)}
				h.Title, _ = it["title"].(string)
				h.Status, _ = it["status"].(string)
				hits = append(hits, h)
			}
		}
	}
	return hits, nil
}

// ---------- creating ----------

func plannerSlug(prefix, title string, taken map[string]bool) string {
	slug := strings.ToLower(title)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	slug = strings.TrimRight(slug, "-")
	if slug == "" {
		slug = "item"
	}
	id := prefix + slug
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s%s-%d", prefix, slug, n)
	}
	return id
}

func plannerNextSeq(db string, ids []string) string {
	prefix := plannerDBs[db].prefix
	re := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `(\d+)$`)
	max, width := 0, 3
	if db == "priorities" {
		width = 2
	}
	for _, id := range ids {
		if m := re.FindStringSubmatch(id); m != nil {
			var n int
			fmt.Sscan(m[1], &n)
			if n > max {
				max = n
			}
			if len(m[1]) > width {
				width = len(m[1])
			}
		}
	}
	return fmt.Sprintf("%s%0*d", prefix, width, max+1)
}

// build makes and validates a new item for db (without saving it).
func (s *PlannerService) build(db string, input PlannerItem, author string, extraDefaults PlannerItem) (PlannerItem, error) {
	taken := map[string]bool{}
	for _, name := range sqlstore.PlannerDatabases() {
		ids, err := s.store.PlannerIDs(name)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			taken[id] = true
		}
	}
	src := plannerClone(input)
	if src == nil {
		src = PlannerItem{}
	}
	for _, d := range []PlannerItem{plannerDefaults[db], extraDefaults} {
		for k, v := range d {
			if _, ok := src[k]; !ok {
				src[k] = v
			}
		}
	}
	delete(src, "comments")
	id, _ := src["id"].(string)
	if id != "" {
		if taken[id] {
			return nil, &PlannerConflictError{Message: "id already exists: " + id}
		}
	} else {
		title, _ := src["title"].(string)
		if title == "" {
			return nil, &PlannerValidationError{Message: "title is required", Errors: []PlannerFieldError{{Path: "title", Message: "is required"}}}
		}
		if plannerDBs[db].seq {
			ids, _ := s.store.PlannerIDs(db)
			id = plannerNextSeq(db, ids)
		} else {
			id = plannerSlug(plannerDBs[db].prefix, title, taken)
		}
	}
	item := src
	item["id"] = id
	item["rev"] = 1
	if c, _ := src["created"].(string); c == "" {
		item["created"] = s.today()
	}
	item["updated"] = s.today()
	if db == "inbox" {
		if a, _ := src["added_by"].(string); a == "" {
			item["added_by"] = author
		}
	} else if a, _ := src["author"].(string); a == "" {
		item["author"] = author
	}
	if _, ok := item["order"].(float64); !ok {
		existing, err := s.store.PlannerList(db)
		if err != nil {
			return nil, err
		}
		max, any := 0.0, false
		for _, e := range existing {
			if o, ok := e["order"].(float64); ok {
				if !any || o > max {
					max = o
				}
				any = true
			}
		}
		if any {
			item["order"] = max + 10
		}
	}
	if err := s.check(db, item); err != nil {
		return nil, err
	}
	return item, nil
}

// withCreateRetry retries a create whose generated id lost a race.
func withCreateRetry(fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if errors.Is(err, sqlstore.ErrPlannerExists) && attempt < plannerRetries {
			continue
		}
		return err
	}
}

// Create makes an item. id is generated when missing (sequential for
// priorities, decisions, risks and inbox; a slug of the title for workstreams,
// roadmap and ideas). rev is 1; created, updated, author and order (end of
// the list) are defaulted.
func (s *PlannerService) Create(db string, input PlannerItem, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if err := s.db(db); err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var out PlannerItem
	err := withCreateRetry(func() error {
		author := plannerAuthor(actor, strField(input, "author"))
		item, err := s.build(db, input, author, nil)
		if err != nil {
			return err
		}
		if err := s.store.PlannerCreate(db, item); err != nil {
			return err
		}
		out = plannerClone(item)
		return nil
	})
	if errors.Is(err, sqlstore.ErrPlannerExists) {
		return nil, &PlannerConflictError{Message: err.Error()}
	}
	return out, err
}

func strField(m PlannerItem, k string) string {
	s, _ := m[k].(string)
	return s
}

// ---------- updating ----------

// Update merges patch into an item (top-level fields; arrays are replaced;
// null removes a field). id, created, comments and rev cannot be patched.
// When rev is non-nil and differs from the item's current rev the update
// fails with a PlannerConflictError carrying the current item; a nil rev is
// last-write-wins.
func (s *PlannerService) Update(db, id string, patch PlannerItem, rev *int) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	var bad []string
	for _, k := range plannerForbiddenPatch {
		if _, ok := patch[k]; ok {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		return nil, &ValidationError{Field: bad[0], Message: "cannot patch: " + strings.Join(bad, ", ")}
	}
	patch = plannerClone(patch)
	apply := func(item PlannerItem) error {
		for k, v := range patch {
			if v == nil {
				delete(item, k)
			} else {
				item[k] = v
			}
		}
		return nil
	}
	if rev == nil {
		return s.mutate(db, id, apply)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	item, err := s.fetch(db, id)
	if err != nil {
		return nil, err
	}
	if cur := plannerRev(item); cur != *rev {
		return nil, &PlannerConflictError{Message: fmt.Sprintf("rev mismatch: you sent %d, current is %d", *rev, cur), Current: item}
	}
	read := plannerRev(item)
	_ = apply(item)
	s.bump(item)
	if err := s.check(db, item); err != nil {
		return nil, err
	}
	if err := s.store.PlannerApply([]sqlstore.PlannerWrite{{DB: db, Item: item, ExpectedRev: read}}); err != nil {
		return nil, s.mapWriteErr(db, id, err)
	}
	return plannerClone(item), nil
}

func plannerAddComment(item PlannerItem, author, text, kind string, now time.Time) {
	comments, _ := item["comments"].([]any)
	re := regexp.MustCompile(`^c-(\d+)$`)
	max := 0
	for _, c := range comments {
		m, _ := c.(map[string]any)
		id, _ := m["id"].(string)
		if sub := re.FindStringSubmatch(id); sub != nil {
			var n int
			fmt.Sscan(sub[1], &n)
			if n > max {
				max = n
			}
		}
	}
	c := map[string]any{
		"id": fmt.Sprintf("c-%d", max+1), "author": author, "text": text,
		"created": now.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if kind != "" {
		c["kind"] = kind
	}
	item["comments"] = append(comments, c)
}

// Comment appends a comment {id c-<n>, author, text, created, kind?} and
// returns the updated item. kind is "comment" (default) or "decision".
func (s *PlannerService) Comment(db, id, text, author, kind, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, &PlannerValidationError{Message: "text is required", Errors: []PlannerFieldError{{Path: "text", Message: "is required"}}}
	}
	if kind != "" && kind != "comment" && kind != "decision" {
		return nil, &ValidationError{Field: "kind", Message: "kind must be comment or decision"}
	}
	who := plannerAuthor(actor, author)
	return s.mutate(db, id, func(item PlannerItem) error {
		plannerAddComment(item, who, text, kind, s.now())
		return nil
	})
}

// PlannerRef names one item.
type PlannerRef struct {
	DB string `json:"db"`
	ID string `json:"id"`
}

// Link adds (add=true) or removes (add=false) to.ID in from's id-list field
// (default related_ids). Both ends must exist; the call is idempotent.
func (s *PlannerService) Link(from, to PlannerRef, field string, add bool) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if field == "" {
		field = "related_ids"
	}
	if !plannerRefField.MatchString(field) {
		return nil, &ValidationError{Field: "field", Message: "field must be related_ids, depends_on or *_ids: " + field}
	}
	if _, err := s.fetch(to.DB, to.ID); err != nil {
		return nil, err
	}
	item, err := s.fetch(from.DB, from.ID)
	if err != nil {
		return nil, err
	}
	list, _ := item[field].([]any)
	has := false
	for _, e := range list {
		if e == to.ID {
			has = true
		}
	}
	if add == has {
		return item, nil // already as asked
	}
	return s.mutate(from.DB, from.ID, func(it PlannerItem) error {
		cur, _ := it[field].([]any)
		if add {
			for _, e := range cur {
				if e == to.ID {
					return nil
				}
			}
			it[field] = append(cur, to.ID)
			return nil
		}
		keep := make([]any, 0, len(cur))
		for _, e := range cur {
			if e != to.ID {
				keep = append(keep, e)
			}
		}
		if len(keep) == 0 {
			delete(it, field)
		} else {
			it[field] = keep
		}
		return nil
	})
}

// PlannerLink is an external pointer {kind, ref, label?}.
type PlannerLink struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label,omitempty"`
}

func cleanPlannerLink(db string, in PlannerLink) (PlannerLink, error) {
	var errs []PlannerFieldError
	bad := func(p, m string) { errs = append(errs, PlannerFieldError{Path: "link." + p, Message: m}) }
	kinds, err := PlannerLinkKinds(db)
	if err != nil {
		return PlannerLink{}, err
	}
	known := false
	for _, k := range kinds {
		if k == in.Kind {
			known = true
		}
	}
	if !known {
		bad("kind", "must be one of "+strings.Join(kinds, ", "))
	}
	ref := strings.TrimSpace(in.Ref)
	switch {
	case ref == "":
		bad("ref", "is required (a non-empty string)")
	case in.Kind == "url" && !plannerURL.MatchString(ref):
		bad("ref", "must be an http or https URL")
	case in.Kind == "torque" && !plannerTorqueID.MatchString(ref):
		bad("ref", "must be a Torque task id such as CW-20261003-0137")
	}
	if len(errs) > 0 {
		return PlannerLink{}, &PlannerValidationError{Message: "link failed validation", Errors: errs}
	}
	return PlannerLink{Kind: in.Kind, Ref: ref, Label: strings.TrimSpace(in.Label)}, nil
}

// LinkAdd adds an external pointer to an item's links. A duplicate kind+ref
// is a conflict. A file ref is a plain pointer and is never read.
func (s *PlannerService) LinkAdd(db, id string, link PlannerLink) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	clean, err := cleanPlannerLink(db, link)
	if err != nil {
		return nil, err
	}
	return s.mutate(db, id, func(item PlannerItem) error {
		list, _ := item["links"].([]any)
		for _, e := range list {
			if m, ok := e.(map[string]any); ok && m["kind"] == clean.Kind && m["ref"] == clean.Ref {
				return &PlannerConflictError{Message: fmt.Sprintf("%s already has a %s link to %s", id, clean.Kind, clean.Ref), Current: plannerClone(item)}
			}
		}
		l := map[string]any{"kind": clean.Kind, "ref": clean.Ref}
		if clean.Label != "" {
			l["label"] = clean.Label
		}
		item["links"] = append(list, l)
		return nil
	})
}

// LinkRemove removes the external pointer with this kind and ref.
func (s *PlannerService) LinkRemove(db, id, kind, ref string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.mutate(db, id, func(item PlannerItem) error {
		list, _ := item["links"].([]any)
		keep := make([]any, 0, len(list))
		for _, e := range list {
			if m, ok := e.(map[string]any); ok && m["kind"] == kind && m["ref"] == ref {
				continue
			}
			keep = append(keep, e)
		}
		if len(keep) == len(list) {
			return &NotFoundError{Entity: "planner link", ID: fmt.Sprintf("%s/%s %s:%s", db, id, kind, ref)}
		}
		if len(keep) == 0 {
			delete(item, "links") // an empty list is stored as no key; match what a later read returns
		} else {
			item["links"] = keep
		}
		return nil
	})
}

// PlannerOrdered is one reorder result.
type PlannerOrdered struct {
	ID    string  `json:"id"`
	Order float64 `json:"order"`
	Rev   int     `json:"rev"`
}

// Reorder sets order = 10, 20, 30 ... for the listed ids in that sequence.
// Other items that had an order follow them in their current relative order;
// items with none stay unordered (after everything). All writes land together.
func (s *PlannerService) Reorder(db string, ids []string) ([]PlannerOrdered, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if err := s.db(db); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, &ValidationError{Field: "ids", Message: "ids contains duplicates"}
		}
		seen[id] = true
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for attempt := 0; ; attempt++ {
		all, err := s.store.PlannerList(db)
		if err != nil {
			return nil, err
		}
		byID := map[string]PlannerItem{}
		for _, it := range all {
			byID[it["id"].(string)] = plannerClone(it)
		}
		var missing []string
		for _, id := range ids {
			if byID[id] == nil {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, &NotFoundError{Entity: "planner item", ID: db + "/" + strings.Join(missing, ", ")}
		}
		var sequence []PlannerItem
		for _, id := range ids {
			sequence = append(sequence, byID[id])
		}
		for _, it := range all { // PlannerList is already in manual order
			id := it["id"].(string)
			if _, ordered := it["order"]; ordered && !seen[id] {
				sequence = append(sequence, byID[id])
			}
		}
		var writes []sqlstore.PlannerWrite
		for n, it := range sequence {
			want := float64((n + 1) * 10)
			if cur, _ := it["order"].(float64); cur == want {
				if _, has := it["order"]; has {
					continue
				}
			}
			read := plannerRev(it)
			it["order"] = want
			s.bump(it)
			writes = append(writes, sqlstore.PlannerWrite{DB: db, Item: it, ExpectedRev: read})
		}
		if err := s.store.PlannerApply(writes); err != nil {
			if errors.Is(err, sqlstore.ErrPlannerConflict) && attempt < plannerRetries {
				continue
			}
			return nil, err
		}
		out := make([]PlannerOrdered, len(sequence))
		for i, it := range sequence {
			out[i] = PlannerOrdered{ID: it["id"].(string), Order: it["order"].(float64), Rev: plannerRev(it)}
		}
		return out, nil
	}
}

// ---------- decisions ----------

// Decide decides a needs-decision (or deferred) decision: option must exist;
// it sets selected_option, status decided, decided_at and decided_by, and
// adds the comment as kind "decision" when given.
func (s *PlannerService) Decide(id, option, comment, author, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	who := plannerAuthor(actor, author)
	return s.mutate("decisions", id, func(item PlannerItem) error {
		opts, _ := item["options"].([]any)
		var chosen map[string]any
		var names []string
		for _, o := range opts {
			if m, ok := o.(map[string]any); ok {
				names = append(names, fmt.Sprint(m["id"]))
				if m["id"] == option {
					chosen = m
				}
			}
		}
		if chosen == nil {
			return &PlannerValidationError{Message: fmt.Sprintf("option %s does not exist on %s", option, id),
				Errors: []PlannerFieldError{{Path: "option", Message: "must be one of " + strings.Join(names, ", ")}}}
		}
		item["selected_option"] = option
		item["status"] = "decided"
		item["decided_at"] = s.today()
		item["decided_by"] = who
		delete(item, "defer_note")
		if strField(item, "decision") == "" {
			item["decision"] = chosen["label"]
		}
		if strField(item, "date") == "" {
			item["date"] = s.today()
		}
		if comment != "" {
			plannerAddComment(item, who, comment, "decision", s.now())
		}
		return nil
	})
}

// Defer sets a decision to deferred, with an optional note that is also
// recorded as a comment.
func (s *PlannerService) Defer(id, note, author, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	who := plannerAuthor(actor, author)
	return s.mutate("decisions", id, func(item PlannerItem) error {
		item["status"] = "deferred"
		if note != "" {
			item["defer_note"] = note
			plannerAddComment(item, who, note, "comment", s.now())
		}
		return nil
	})
}

// Reopen returns a decision to needs-decision, clearing selected_option,
// decided_at, decided_by and defer_note.
func (s *PlannerService) Reopen(id string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.mutate("decisions", id, func(item PlannerItem) error {
		item["status"] = "needs-decision"
		for _, k := range []string{"selected_option", "decided_at", "defer_note", "decided_by"} {
			delete(item, k)
		}
		return nil
	})
}

// ---------- inbox ----------

// InboxAdd adds an inbox item. path is a filesystem pointer only; the
// service never reads it.
func (s *PlannerService) InboxAdd(fields PlannerItem, addedBy, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	fields = plannerClone(fields)
	delete(fields, "added_by")
	return s.Create("inbox", fields, plannerAuthor(actor, addedBy))
}

// InboxPromote creates an item in toDB from an inbox item (title; body
// becomes notes; fields override; path becomes a file link; related_ids point
// back), then marks the inbox item promoted with promoted_to. The new item
// and the inbox update land together.
func (s *PlannerService) InboxPromote(id, toDB string, fields PlannerItem, actor string) (inbox, item PlannerItem, err error) {
	if err := s.gate(); err != nil {
		return nil, nil, err
	}
	if err := s.db(toDB); err != nil {
		return nil, nil, err
	}
	if toDB == "inbox" {
		return nil, nil, &ValidationError{Field: "to_db", Message: "cannot promote into the inbox"}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for attempt := 0; ; attempt++ {
		in, err := s.fetch("inbox", id)
		if err != nil {
			return nil, nil, err
		}
		if st := strField(in, "status"); st == "promoted" || st == "dismissed" {
			return nil, nil, &PlannerConflictError{Message: fmt.Sprintf("%s is already %s", id, st), Current: in}
		}
		read := plannerRev(in)
		base := PlannerItem{"title": in["title"], "related_ids": []any{id}}
		if b := strField(in, "body"); b != "" {
			base["notes"] = b
		}
		if p := strField(in, "path"); p != "" {
			base["links"] = []any{map[string]any{"kind": "file", "ref": p}}
		}
		extra := plannerClone(plannerPromoteDefaults[toDB])
		if toDB == "priorities" {
			extra = PlannerItem{}
			rank := 0.0
			all, err := s.store.PlannerList("priorities")
			if err != nil {
				return nil, nil, err
			}
			for _, p := range all {
				if r, ok := p["rank"].(int); ok && float64(r) > rank {
					rank = float64(r)
				}
			}
			extra["rank"] = rank + 1
		}
		merged := plannerClone(base)
		for k, v := range plannerClone(fields) {
			merged[k] = v
		}
		created, err := s.build(toDB, merged, plannerAuthor(actor), extra)
		if err != nil {
			return nil, nil, err
		}
		in["status"] = "promoted"
		in["promoted_to"] = map[string]any{"db": toDB, "id": created["id"]}
		rel, _ := in["related_ids"].([]any)
		have := false
		for _, r := range rel {
			if r == created["id"] {
				have = true
			}
		}
		if !have {
			in["related_ids"] = append(rel, created["id"])
		}
		s.bump(in)
		if err := s.check("inbox", in); err != nil {
			return nil, nil, err
		}
		err = s.store.PlannerApply([]sqlstore.PlannerWrite{
			{DB: toDB, Item: created, Create: true},
			{DB: "inbox", Item: in, ExpectedRev: read},
		})
		if (errors.Is(err, sqlstore.ErrPlannerExists) || errors.Is(err, sqlstore.ErrPlannerConflict)) && attempt < plannerRetries {
			continue
		}
		if err != nil {
			return nil, nil, s.mapWriteErr("inbox", id, err)
		}
		return plannerClone(in), plannerClone(created), nil
	}
}

// InboxDismiss marks an inbox item dismissed; an optional note is recorded as
// a comment.
func (s *PlannerService) InboxDismiss(id, note, author, actor string) (PlannerItem, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	who := plannerAuthor(actor, author)
	return s.mutate("inbox", id, func(item PlannerItem) error {
		item["status"] = "dismissed"
		if note != "" {
			plannerAddComment(item, who, note, "", s.now())
		}
		return nil
	})
}
