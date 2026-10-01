package sqlstore

import "fmt"

// RunTimeSeriesCell is a SQL aggregate, never an individual run. Start is
// the local bucket label; the service resolves it using the requested offset.
type RunTimeSeriesCell struct {
	Start            string
	Status           string
	Count            int
	PromptTokens     int64
	CompletionTokens int64
	Cost             float64
}

func (s *Store) RunTimeSeries(f RunFilter, bucket string, offsetMinutes int) ([]RunTimeSeriesCell, error) {
	format := "%Y-%m-%dT00:00:00"
	pgFormat := "YYYY-MM-DD\"T\"00:00:00"
	if bucket == "hour" {
		format = "%Y-%m-%dT%H:00:00"
		pgFormat = "YYYY-MM-DD\"T\"HH24:00:00"
	} else if bucket != "day" {
		return nil, fmt.Errorf("unsupported time bucket %q", bucket)
	}
	// Drop fractions only for grouping: SQLite otherwise rounds nanoseconds
	// near an edge into the next second. The indexed cohort predicate retains
	// the complete normalized nanosecond timestamp.
	expr := fmt.Sprintf("strftime('%s', substr(%s,1,19), '%+d minutes')", format, s.timestampSortKey("r.started_at"), offsetMinutes)
	if _, ok := s.dialect.(postgresDialect); ok {
		expr = fmt.Sprintf("to_char((r.started_at AT TIME ZONE 'UTC') + INTERVAL '%d minutes', '%s')", offsetMinutes, pgFormat)
	}
	from, where, args := s.RunFilterSQL(f)
	// Aggregate each run's ledger entries first, avoiding token/count
	// duplication when a run has multiple ledger rows. Ledger dates do not
	// define cohort membership.
	q := "SELECT " + expr + ", r.status, COUNT(*), COALESCE(SUM(r.prompt_tokens),0), COALESCE(SUM(r.completion_tokens),0), COALESCE(SUM((SELECT SUM(l.cost) FROM cost_ledger l WHERE l.run_id=r.id)),0)" + from + where + " GROUP BY " + expr + ", r.status ORDER BY " + expr + ", r.status"
	rows, err := s.ReadDB().Query(s.runBind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cells := []RunTimeSeriesCell{}
	for rows.Next() {
		var cell RunTimeSeriesCell
		if err := rows.Scan(&cell.Start, &cell.Status, &cell.Count, &cell.PromptTokens, &cell.CompletionTokens, &cell.Cost); err != nil {
			return nil, err
		}
		cells = append(cells, cell)
	}
	return cells, rows.Err()
}
