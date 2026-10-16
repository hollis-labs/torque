package service

import "database/sql"

// validateProgressDetail accepts unset (inherit) as well as the C4 levels.
func validateProgressDetail(value string) error {
	switch value {
	case "", "off", "milestone", "step", "verbose":
		return nil
	default:
		return &ValidationError{Field: "progress_detail", Message: "expected off, milestone, step or verbose (empty clears)"}
	}
}

func progressDetailValue(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
