package sqlstore

import (
	"fmt"
	"time"
)

const timestampKeyLayout = "2006-01-02 15:04:05.000000000"

func isTimestampSortColumn(col string) bool {
	return col == "created_at" || col == "updated_at"
}

// timestampSortKey gives SQLite's canonical and legacy UTC timestamp text
// one fixed-width key. Padding uses text only, preserving all nine fractional
// digits without floating-point rounding. Callers use this same key for range
// bounds, cursor predicates and ordering. PostgreSQL compares typed timestamps.
func (s *Store) timestampSortKey(col string) string {
	if !isTimestampSortColumn(col) {
		return col
	}
	if _, sqlite := s.dialect.(sqliteDialect); !sqlite {
		return col
	}
	// Current rows use SQLite's space-separated UTC format. Legacy Go writes
	// append " +0000 UTC". Also accept the equivalent RFC3339 UTC spelling.
	plain := fmt.Sprintf("replace(replace(%s, ' +0000 UTC', ''), 'Z', '')", col)
	return fmt.Sprintf("(replace(substr(%s, 1, 19), 'T', ' ') || '.' || CASE WHEN substr(%s, 20, 1) = '.' THEN substr(substr(%s, 21) || '000000000', 1, 9) ELSE '000000000' END)", plain, plain, plain)
}

// timestampArg matches timestampSortKey while preserving the driver's native
// timestamp binding on PostgreSQL. The parser also accepts old whole-second
// cursor values and variable-width fractions.
func (s *Store) timestampArg(raw string) (any, error) {
	t, err := time.Parse(SQLiteDatetimeLayout, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %q: %w", raw, err)
	}
	if _, sqlite := s.dialect.(sqliteDialect); sqlite {
		return t.UTC().Format(timestampKeyLayout), nil
	}
	return t.UTC(), nil
}

func (s *Store) timestampCursorArg(col string, arg any) (any, error) {
	if !isTimestampSortColumn(col) {
		return arg, nil
	}
	return s.timestampArg(arg.(string))
}
