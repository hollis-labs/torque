package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Planner storage (migration 040). One table per planner database, items
// exchanged as JSON-shaped maps so the service can validate against the
// per-database schema and an import from the portfolio-tracker JSON loses
// nothing: a key the table has no column for is kept in `extra`.

var (
	// ErrPlannerNotFound wraps a missing database or item.
	ErrPlannerNotFound = errors.New("planner item not found")
	// ErrPlannerExists is returned by PlannerCreate for an id already taken.
	ErrPlannerExists = errors.New("planner item already exists")
	// ErrPlannerConflict is returned by PlannerUpdate when the stored rev
	// differs from the rev the caller read.
	ErrPlannerConflict = errors.New("planner item rev conflict")
)

type plannerKind int

const (
	pText plannerKind = iota
	pInt
	pBool
	pJSON
)

type plannerCol struct {
	name string // JSON key
	col  string // SQL column, when it differs from the key
	kind plannerKind
}

func (c plannerCol) column() string {
	if c.col != "" {
		return c.col
	}
	return c.name
}

func text(names ...string) []plannerCol {
	out := make([]plannerCol, len(names))
	for i, n := range names {
		out[i] = plannerCol{name: n, kind: pText}
	}
	return out
}

func jsonCols(names ...string) []plannerCol {
	out := make([]plannerCol, len(names))
	for i, n := range names {
		out[i] = plannerCol{name: n, kind: pJSON}
	}
	return out
}

func cat(parts ...[]plannerCol) []plannerCol {
	var out []plannerCol
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// plannerCommon are the columns every planner table carries. rev, priority
// and order are handled apart because they are numeric and `order` is a SQL
// keyword (column ord). Items store them under the tracker's key names.
var plannerCommon = cat(
	text("title", "tldr", "notes", "source", "author", "created", "updated"),
	jsonCols("tags", "related_ids"),
)

var plannerSpecs = map[string][]plannerCol{
	"priorities": cat(
		[]plannerCol{{name: "rank", kind: pInt}},
		text("status", "summary"),
		jsonCols("sub_items", "open_questions", "roadmap_ids", "workstream_ids", "idea_ids"),
	),
	"workstreams": cat(
		text("status", "phase", "tether_workstream_id", "torque_tag", "owner", "goal", "state", "next_step"),
		jsonCols("torque_project_ids", "blockers", "priority_ids", "roadmap_ids"),
	),
	"roadmap": cat(
		text("area", "horizon", "status", "outcome"),
		[]plannerCol{{name: "mvp", kind: pBool}},
		jsonCols("depends_on", "priority_ids", "workstream_ids", "tasks"),
	),
	"ideas": cat(
		text("kind", "status", "summary", "context", "recommendation", "decision", "discuss_in"),
		jsonCols("options", "priority_ids", "roadmap_ids", "workstream_ids"),
	),
	"decisions": cat(
		text("status", "date", "decided_by", "area", "decision", "rationale", "supersedes", "superseded_by", "selected_option", "decided_at", "defer_note"),
		jsonCols("alternatives", "options"),
	),
	"risks": cat(
		text("kind", "severity", "status", "description", "impact", "mitigation", "owner", "raised"),
		jsonCols("roadmap_ids", "workstream_ids"),
	),
	"inbox": cat(
		text("kind", "status", "body", "path", "added_by"),
		jsonCols("promoted_to"),
	),
}

// PlannerDatabases lists the planner database names, sorted.
func PlannerDatabases() []string {
	names := make([]string, 0, len(plannerSpecs))
	for n := range plannerSpecs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PlannerKnownKeys reports the item keys that have a column in db, plus the
// structural keys handled apart. Anything else lands in `extra`.
func PlannerKnownKeys(db string) (map[string]bool, bool) {
	spec, ok := plannerSpecs[db]
	if !ok {
		return nil, false
	}
	keys := map[string]bool{"id": true, "rev": true, "order": true, "priority": true, "comments": true, "links": true}
	for _, c := range plannerCommon {
		keys[c.name] = true
	}
	for _, c := range spec {
		keys[c.name] = true
	}
	return keys, true
}

func plannerTable(db string) (string, []plannerCol, error) {
	spec, ok := plannerSpecs[db]
	if !ok {
		return "", nil, fmt.Errorf("%w: unknown planner database %q", ErrPlannerNotFound, db)
	}
	return "planner_" + db, cat(plannerCommon, spec), nil
}

// PlannerItem is one planner row as the JSON object the tracker used. Numbers
// are float64 after a JSON decode and int/float64 after a read; the store
// accepts either.
type PlannerItem = map[string]any

// plannerCols are the columns written by insert/update, in order: id, the
// typed columns, then rev, ord, priority, extra.
func plannerWriteCols(cols []plannerCol) []string {
	out := []string{"id"}
	for _, c := range cols {
		out = append(out, c.column())
	}
	return append(out, "rev", "ord", "priority", "extra")
}

func plannerArgs(db string, cols []plannerCol, item PlannerItem) ([]any, error) {
	id, _ := item["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("planner %s: item id is required", db)
	}
	args := []any{id}
	known, _ := PlannerKnownKeys(db)
	for _, c := range cols {
		v, present := item[c.name]
		switch c.kind {
		case pText:
			s, _ := v.(string)
			args = append(args, s)
		case pInt:
			args = append(args, toInt(v))
		case pBool:
			b, _ := v.(bool)
			args = append(args, boolInt(b))
		case pJSON:
			if !present || v == nil {
				// promoted_to has no array default; everything else is an array.
				if c.name == "promoted_to" {
					args = append(args, "null")
				} else {
					args = append(args, "[]")
				}
				continue
			}
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("planner %s.%s: %w", db, c.name, err)
			}
			args = append(args, string(b))
		}
	}
	rev := toInt(item["rev"])
	if rev < 1 {
		rev = 1
	}
	var ord, prio any
	if v, ok := item["order"]; ok && v != nil {
		ord = toFloat(v)
	}
	if v, ok := item["priority"]; ok && v != nil {
		prio = toInt(v)
	}
	extra := map[string]any{}
	for k, v := range item {
		if !known[k] {
			extra[k] = v
		}
	}
	eb, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	return append(args, rev, ord, prio, string(eb)), nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PlannerCreate inserts an item and its comments and links in one transaction.
// The caller supplies id, rev and timestamps.
func (s *Store) PlannerCreate(db string, item PlannerItem) error {
	table, cols, err := plannerTable(db)
	if err != nil {
		return err
	}
	args, err := plannerArgs(db, cols, item)
	if err != nil {
		return err
	}
	names := plannerWriteCols(cols)
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table,
		strings.Join(names, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")), args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key") {
			return fmt.Errorf("%w: %s", ErrPlannerExists, item["id"])
		}
		return err
	}
	id := item["id"].(string)
	if err := plannerWriteChildren(tx, db, id, item); err != nil {
		return err
	}
	return tx.Commit()
}

func plannerWriteChildren(tx *sql.Tx, db, id string, item PlannerItem) error {
	for _, c := range asMaps(item["comments"]) {
		text, _ := c["text"].(string)
		cid, _ := c["id"].(string)
		author, _ := c["author"].(string)
		created, _ := c["created"].(string)
		kind, _ := c["kind"].(string)
		if kind == "" {
			kind = "comment"
		}
		if _, err := tx.Exec(`INSERT INTO planner_comments (db, item_id, cid, author, kind, text, created)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, db, id, cid, author, kind, text, created); err != nil {
			return err
		}
	}
	for _, l := range asMaps(item["links"]) {
		kind, _ := l["kind"].(string)
		ref, _ := l["ref"].(string)
		label, _ := l["label"].(string)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO planner_links (db, item_id, kind, ref, label)
			VALUES (?, ?, ?, ?, ?)`, db, id, kind, ref, label); err != nil {
			return err
		}
	}
	return nil
}

func asMaps(v any) []map[string]any {
	switch xs := v.(type) {
	case []map[string]any:
		return xs
	case []any:
		out := make([]map[string]any, 0, len(xs))
		for _, x := range xs {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// PlannerUpdate rewrites an item's row (not its comments or links, which have
// their own writers). The write succeeds only when the stored rev equals
// expectedRev; the caller supplies the new rev inside item.
func (s *Store) PlannerUpdate(db string, item PlannerItem, expectedRev int) error {
	table, cols, err := plannerTable(db)
	if err != nil {
		return err
	}
	args, err := plannerArgs(db, cols, item)
	if err != nil {
		return err
	}
	names := plannerWriteCols(cols)
	sets := make([]string, 0, len(names)-1)
	for _, n := range names[1:] {
		sets = append(sets, n+" = ?")
	}
	res, err := s.db.Exec(fmt.Sprintf("UPDATE %s SET %s WHERE id = ? AND rev = ?", table, strings.Join(sets, ", ")),
		append(append([]any{}, args[1:]...), args[0], expectedRev)...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var cur int
		switch err := s.db.QueryRow(fmt.Sprintf("SELECT rev FROM %s WHERE id = ?", table), args[0]).Scan(&cur); {
		case err == sql.ErrNoRows:
			return fmt.Errorf("%w: %s/%s", ErrPlannerNotFound, db, args[0])
		case err != nil:
			return err
		}
		return fmt.Errorf("%w: %s/%s is at rev %d, caller read rev %d", ErrPlannerConflict, db, args[0], cur, expectedRev)
	}
	return nil
}

// PlannerGet returns one item with its comments and links.
func (s *Store) PlannerGet(db, id string) (PlannerItem, error) {
	items, err := s.plannerQuery(db, "WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: %s/%s", ErrPlannerNotFound, db, id)
	}
	return items[0], nil
}

// PlannerList returns every item in a database, manual order first (items
// with no order sort last, in insertion order), each with comments and links.
func (s *Store) PlannerList(db string) ([]PlannerItem, error) {
	return s.plannerQuery(db, "")
}

func (s *Store) plannerQuery(db, where string, args ...any) ([]PlannerItem, error) {
	table, cols, err := plannerTable(db)
	if err != nil {
		return nil, err
	}
	names := plannerWriteCols(cols)
	rows, err := s.ReadDB().Query(fmt.Sprintf(
		"SELECT %s FROM %s %s ORDER BY (ord IS NULL), ord, rowid", strings.Join(names, ", "), table, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PlannerItem
	for rows.Next() {
		vals := make([]sql.NullString, len(names))
		dest := make([]any, len(names))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		item := PlannerItem{"id": vals[0].String}
		for i, c := range cols {
			v := vals[i+1].String
			switch c.kind {
			case pText:
				if v != "" {
					item[c.name] = v
				}
			case pInt:
				item[c.name] = toInt(parseNumber(v))
			case pBool:
				if v == "1" || v == "true" {
					item[c.name] = true
				}
			case pJSON:
				if v == "" || v == "[]" || v == "null" {
					continue
				}
				var decoded any
				if err := json.Unmarshal([]byte(v), &decoded); err != nil {
					return nil, fmt.Errorf("planner %s.%s: %w", db, c.name, err)
				}
				item[c.name] = decoded
			}
		}
		n := len(cols) + 1
		item["rev"] = toInt(parseNumber(vals[n].String))
		if vals[n+1].Valid {
			item["order"] = toFloat(parseNumber(vals[n+1].String))
		}
		if vals[n+2].Valid {
			item["priority"] = toInt(parseNumber(vals[n+2].String))
		}
		var extra map[string]any
		if err := json.Unmarshal([]byte(vals[n+3].String), &extra); err == nil {
			for k, v := range extra {
				item[k] = v
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := s.plannerAttach(db, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func parseNumber(s string) json.Number { return json.Number(s) }

func (s *Store) plannerAttach(db string, item PlannerItem) error {
	id := item["id"].(string)
	crows, err := s.ReadDB().Query(`SELECT cid, author, kind, text, created FROM planner_comments
		WHERE db = ? AND item_id = ? ORDER BY pk`, db, id)
	if err != nil {
		return err
	}
	defer crows.Close()
	var comments []any
	for crows.Next() {
		var cid, author, kind, text, created string
		if err := crows.Scan(&cid, &author, &kind, &text, &created); err != nil {
			return err
		}
		c := map[string]any{"text": text}
		for k, v := range map[string]string{"id": cid, "author": author, "created": created} {
			if v != "" {
				c[k] = v
			}
		}
		if kind != "comment" {
			c["kind"] = kind
		}
		comments = append(comments, c)
	}
	if err := crows.Err(); err != nil {
		return err
	}
	if len(comments) > 0 {
		item["comments"] = comments
	}
	lrows, err := s.ReadDB().Query(`SELECT kind, ref, label FROM planner_links
		WHERE db = ? AND item_id = ? ORDER BY pk`, db, id)
	if err != nil {
		return err
	}
	defer lrows.Close()
	var links []any
	for lrows.Next() {
		var kind, ref, label string
		if err := lrows.Scan(&kind, &ref, &label); err != nil {
			return err
		}
		l := map[string]any{"kind": kind, "ref": ref}
		if label != "" {
			l["label"] = label
		}
		links = append(links, l)
	}
	if len(links) > 0 {
		item["links"] = links
	}
	return lrows.Err()
}

// PlannerAddComment appends a comment to an item. It does not touch rev;
// the service decides whether a comment bumps it.
func (s *Store) PlannerAddComment(db, itemID string, c map[string]any) error {
	if _, _, err := plannerTable(db); err != nil {
		return err
	}
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := plannerWriteChildren(tx, db, itemID, PlannerItem{"comments": []any{c}}); err != nil {
		return err
	}
	return tx.Commit()
}

// PlannerAddLink adds an external pointer. Returns false when the same
// kind and ref is already on the item.
func (s *Store) PlannerAddLink(db, itemID, kind, ref, label string) (bool, error) {
	if _, _, err := plannerTable(db); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO planner_links (db, item_id, kind, ref, label)
		VALUES (?, ?, ?, ?, ?)`, db, itemID, kind, ref, label)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PlannerRemoveLink removes an external pointer. Returns false when absent.
func (s *Store) PlannerRemoveLink(db, itemID, kind, ref string) (bool, error) {
	if _, _, err := plannerTable(db); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`DELETE FROM planner_links WHERE db = ? AND item_id = ? AND kind = ? AND ref = ?`,
		db, itemID, kind, ref)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PlannerIDs returns every id in a database. Id allocation reads this because
// ids are never reused and there is no delete.
func (s *Store) PlannerIDs(db string) ([]string, error) {
	table, _, err := plannerTable(db)
	if err != nil {
		return nil, err
	}
	rows, err := s.ReadDB().Query("SELECT id FROM " + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
