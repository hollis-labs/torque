package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Profiles reflect the task's current selector, not a historical run snapshot.
const runProfileExpr = "COALESCE(NULLIF(profile_task.launch_profile,''),profile_task.agent_profile)"

// RunFilterSQL is the cohort predicate shared by pages, counts and facets.
// Alias r is runs; scoped queries also join tasks as t.
func (s *Store) RunFilterSQL(f RunFilter) (string, string, []any) {
	from := " FROM runs r"
	where := []string{"1=1"}
	args := []any{}
	if f.ProjectID != "" || f.SprintID != "" || f.EpicID != "" {
		from += " INNER JOIN tasks t ON t.id = r.task_id"
	}
	if len(f.Profiles) > 0 {
		from += " LEFT JOIN tasks profile_task ON profile_task.id = r.task_id"
	}
	for _, v := range []struct{ col, value string }{{"r.task_id", f.TaskID}, {"t.project_id", f.ProjectID}, {"t.sprint_id", f.SprintID}, {"t.epic_id", f.EpicID}} {
		if v.value != "" {
			where = append(where, v.col+" = ?")
			args = append(args, v.value)
		}
	}
	if !f.Since.IsZero() {
		where = append(where, s.timestampSortKey("r.started_at")+" >= ?")
		v, _ := s.timestampArg(f.Since.UTC().Format(SQLiteDatetimeLayoutWithFractional))
		args = append(args, v)
	}
	if !f.Until.IsZero() {
		where = append(where, s.timestampSortKey("r.started_at")+" <= ?")
		v, _ := s.timestampArg(f.Until.UTC().Format(SQLiteDatetimeLayoutWithFractional))
		args = append(args, v)
	}
	for _, filter := range []struct {
		column string
		values []string
	}{{"r.status", f.Statuses}, {"r.executor", f.Executors}, {runProfileExpr, f.Profiles}} {
		if len(filter.values) == 0 {
			continue
		}
		marks := make([]string, len(filter.values))
		for i, value := range filter.values {
			marks[i] = "?"
			args = append(args, value)
		}
		where = append(where, filter.column+" IN ("+strings.Join(marks, ",")+")")
	}
	return from, " WHERE " + strings.Join(where, " AND "), args
}

func (s *Store) runSortExpr(sortBy string) (string, error) {
	switch sortBy {
	case "", "started_at":
		return s.timestampSortKey("r.started_at"), nil
	case "status":
		return "r.status", nil
	case "cost":
		return "COALESCE(r.cost,0)", nil
	case "duration":
		if _, ok := s.dialect.(postgresDialect); ok {
			return "COALESCE(CAST(ROUND(EXTRACT(EPOCH FROM (r.ended_at-r.started_at))*1000) AS BIGINT), -1)", nil
		}
		return "COALESCE(CAST(ROUND((julianday(" + s.timestampSortKey("r.ended_at") + ")-julianday(" + s.timestampSortKey("r.started_at") + "))*86400000) AS INTEGER), -1)", nil
	default:
		return "", fmt.Errorf("unsupported run sort %q", sortBy)
	}
}

func (s *Store) runBind(q string) string {
	if _, ok := s.dialect.(postgresDialect); !ok {
		return q
	}
	n := 0
	return qPlaceholder(q, &n)
}
func qPlaceholder(q string, n *int) string {
	parts := strings.Split(q, "?")
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			*n++
			fmt.Fprintf(&b, "$%d", *n)
		}
		b.WriteString(p)
	}
	return b.String()
}

type runReader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func (s *Store) queryRuns(db runReader, f RunFilter) ([]RunRecord, error) {
	expr, err := s.runSortExpr(f.SortBy)
	if err != nil {
		return nil, err
	}
	dir := strings.ToLower(f.SortDir)
	if dir == "" {
		dir = "desc"
	}
	if dir != "asc" && dir != "desc" {
		return nil, fmt.Errorf("invalid sort direction")
	}
	from, where, args := s.RunFilterSQL(f)
	if f.AfterID != 0 {
		var value any = f.AfterSortValue
		switch f.SortBy {
		case "", "started_at":
			value, err = s.timestampArg(f.AfterSortValue)
		case "cost":
			var v float64
			v, err = strconv.ParseFloat(f.AfterSortValue, 64)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				err = fmt.Errorf("non-finite cost")
			}
			value = v
		case "duration":
			value, err = strconv.ParseInt(f.AfterSortValue, 10, 64)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: invalid run sort value", ErrInvalidCursor)
		}
		op := ">"
		if dir == "desc" {
			op = "<"
		}
		where += " AND (" + expr + " " + op + " ? OR (" + expr + " = ? AND r.id > ?))"
		args = append(args, value, value, f.AfterID)
	}
	q := `SELECT r.id,r.task_id,r.executor,r.status,r.started_at,r.ended_at,
 r.prompt_tokens,r.completion_tokens,r.cost,r.exit_code,r.error_message,r.metadata,
 r.cache_read_tokens,r.cache_write_tokens,r.cost_source,` + expr + from + where + " ORDER BY " + expr + " " + dir + ", r.id ASC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	if f.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, f.Offset)
	}
	rows, err := db.Query(s.runBind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunRecord{}
	for rows.Next() {
		var r RunRecord
		var sortValue any
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Executor, &r.Status, &r.StartedAt, &r.EndedAt, &r.PromptTokens, &r.CompletionTokens, &r.Cost, &r.ExitCode, &r.ErrorMessage, &r.Metadata, &r.CacheReadTokens, &r.CacheWriteTokens, &r.CostSource, &sortValue); err != nil {
			return nil, err
		}
		if f.SortBy == "" || f.SortBy == "started_at" {
			r.QuerySortValue = r.StartedAt.UTC().Format(SQLiteDatetimeLayoutWithFractional)
		} else {
			switch v := sortValue.(type) {
			case []byte:
				r.QuerySortValue = string(v)
			default:
				r.QuerySortValue = fmt.Sprint(v)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRunsPage counts the matching cohort before cursor/offset within the
// same read snapshot as the page. Public callers opt into this extra work.
func (s *Store) ListRunsPage(f RunFilter) ([]RunRecord, int, error) {
	tx, err := s.ReadDB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	from, where, args := s.RunFilterSQL(f)
	var total int
	if err := tx.QueryRow(s.runBind("SELECT COUNT(*)"+from+where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	runs, err := s.queryRuns(tx, f)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}
