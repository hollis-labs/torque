package scheduler

// TokenTotalsTracked reports how many runs hold a live token total. Tests
// use it to prove the per-run state is released however a run ends.
func (s *Scheduler) TokenTotalsTracked() int { return s.tokenTotals.size() }
