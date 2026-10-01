package sqlstore

// RunFacets uses the same cohort predicates as run lists and exact counts.
// Profile resolves the task's current launch selector, falling back to its
// legacy agent profile; runs do not persist a historical profile snapshot.
func (s *Store) RunFacets(f RunFilter, dims []string, limit int) (EntityFacetResult, error) {
	from, where, args := s.RunFilterSQL(f)
	from += " LEFT JOIN tasks profile_task ON profile_task.id = r.task_id"
	columns := map[string]string{"status": "r.status", "executor": "r.executor", "profile": "COALESCE(NULLIF(profile_task.launch_profile,''),profile_task.agent_profile)"}
	out, err := s.columnFacets(from, where, args, dims, columns, limit)
	if err != nil {
		return out, err
	}
	totals := &RunFacetTotals{}
	// The cost ledger is canonical. Aggregate per run before summing so ledger
	// multiplicity cannot duplicate token totals. Older runs without ledger rows
	// contribute zero cost, as in GetTaskRunAggregate.
	q := "SELECT COALESCE(SUM((SELECT SUM(l.cost) FROM cost_ledger l WHERE l.run_id=r.id)),0), COALESCE(SUM(r.prompt_tokens),0), COALESCE(SUM(r.completion_tokens),0)" + from + where
	if err := s.ReadDB().QueryRow(s.runBind(q), args...).Scan(&totals.Cost, &totals.PromptTokens, &totals.CompletionTokens); err != nil {
		return out, err
	}
	out.Totals = totals
	return out, nil
}
