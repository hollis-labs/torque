// Package pagination holds the shared, entity-agnostic primitives PRIM-001
// (cursor pagination) and PRIM-002 (sort_by/sort_dir) implement, per
// DEC-001's binding spec (tasks/phase-0-decisions/DEC-001-pagination-strategy.md
// §Outcome). Task's `torque_task_list` is the reference implementation
// (internal/mcpadapter/task_tools.go); the other 6 in-scope entities (Comment,
// Project, Epic, Sprint, Issue, Plan) adopt this same package in Phase 4.
//
// Why this lives in internal/service rather than internal/mcpadapter or
// internal/persistence/sqlstore: it depends on neither. Cursor encode/decode
// is pure string/JSON manipulation with no knowledge of MCP request shapes or
// SQL column types — entity-specific code (in mcpadapter, which decodes the
// request and validates the cursor, and in sqlstore, which type-converts the
// decoded sort value into a SQL bind argument for its own columns) is the
// only place that needs those specifics. Keeping this package dependency-free
// makes it trivially unit-testable and avoids any import-cycle risk between
// mcpadapter and sqlstore.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// cursorVersion is stamped into every encoded cursor's `v` field. Bump this
// (and reject old versions in Decode) if the wire shape ever changes
// incompatibly.
const cursorVersion = 1

// Cursor is DEC-001's opaque pagination token: enough state to resume a
// keyset-paginated list exactly where the previous page left off, tuple-
// comparing (sort column, id) so ties in the sort column don't produce
// duplicate/missing rows across pages.
//
// Wire encoding: base64url(JSON{v,sb,sd,sv,id}) — see Encode/Decode. Callers
// (MCP clients) must treat the string as opaque; only this package's
// Encode/Decode construct or interpret one.
type Cursor struct {
	// V is the cursor format version. Always cursorVersion on encode;
	// Decode rejects any other value.
	V int `json:"v"`
	// SortBy is the sort_by field the cursor was issued for.
	SortBy string `json:"sb"`
	// SortDir is "asc" or "desc" — the sort_dir the cursor was issued for.
	SortDir string `json:"sd"`
	// SortValue is the string-encoded value of the sort column on the last
	// row of the previous page. Producing a stable, unambiguous string
	// encoding per sort column (e.g. RFC3339Nano for timestamps, decimal
	// for integers) is the entity layer's responsibility — this package
	// treats it as an opaque string.
	SortValue string `json:"sv"`
	// ID is the last row's primary key — the universal tiebreak per
	// DEC-001, ascending regardless of the primary sort direction.
	ID string `json:"id"`
}

// Encode returns the opaque, base64url-encoded cursor token for the given
// sort_by/sort_dir/sort_value/id. Marshal error is unreachable (Cursor has
// no non-marshalable fields) and intentionally ignored to keep the helper's
// signature ergonomic at call sites that build a cursor from a known-good
// last row.
func Encode(sortBy, sortDir, sortValue, id string) string {
	c := Cursor{V: cursorVersion, SortBy: sortBy, SortDir: sortDir, SortValue: sortValue, ID: id}
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses an opaque cursor token produced by Encode. Returns an error
// for malformed base64/JSON, an unsupported version, or a missing required
// field — every case here is a caller input fault (stale/tampered/cross-
// version token), so handlers should map a Decode error to arg_invalid, not
// an internal error.
func Decode(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor payload: %w", err)
	}
	if c.V != cursorVersion {
		return Cursor{}, fmt.Errorf("unsupported cursor version %d", c.V)
	}
	if c.SortBy == "" || c.SortDir == "" || c.ID == "" {
		return Cursor{}, fmt.Errorf("cursor missing required field(s)")
	}
	return c, nil
}

// Validate checks that a decoded cursor's sort_by/sort_dir match the
// request's CURRENT sort_by/sort_dir. Cursors aren't portable across
// different sort orders (DEC-001) — paging with one sort_by/sort_dir then
// switching mid-stream would silently produce a nonsensical WHERE clause
// (comparing against a column that isn't even the active ORDER BY), so the
// server rejects the mismatch instead of guessing.
func (c Cursor) Validate(sortBy, sortDir string) error {
	if c.SortBy != sortBy || c.SortDir != sortDir {
		return fmt.Errorf("cursor was issued for sort_by=%s sort_dir=%s, but this request specifies sort_by=%s sort_dir=%s",
			c.SortBy, c.SortDir, sortBy, sortDir)
	}
	return nil
}
