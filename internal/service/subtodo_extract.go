package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// subtodoCheckboxLine matches top-level markdown checkbox list items:
//   - [ ] pending
//   - [x] done (case-insensitive)
//
// Indented lines (nested sub-lists) are skipped per spec: only items at
// zero indent are pulled up as structured subtodos. The list marker accepts
// "-", "*", or "+" to cover common markdown dialects.
var subtodoCheckboxLine = regexp.MustCompile(`^[-*+]\s+\[( |x|X)\]\s+(.+?)\s*$`)

// ExtractSubtodosFromDescription scans a task description for top-level
// markdown checkbox lines and returns them as Subtodo items. Items default
// to required=false; Done mirrors the "[x]" marker. Item ids are generated
// as "item-1", "item-2", ... in first-appearance order. The description is
// returned untouched so the original markdown remains addressable in the UI.
//
// Returns nil when there are no checkbox lines, so callers can use len() to
// gate whether they write to the DB at all.
func ExtractSubtodosFromDescription(description string) []sqlstore.Subtodo {
	if description == "" {
		return nil
	}
	var out []sqlstore.Subtodo
	for line := range strings.SplitSeq(description, "\n") {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		m := subtodoCheckboxLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		done := m[1] == "x" || m[1] == "X"
		out = append(out, sqlstore.Subtodo{
			ID:       fmt.Sprintf("item-%d", len(out)+1),
			Text:     m[2],
			Required: false,
			Done:     done,
		})
	}
	return out
}
