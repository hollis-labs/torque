package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The curated board: the workstreams (and the decisions, risks and inbox items
// around them) the owner chose to manage, marked with the tag "board". It maps
// onto the same four section keys as the activity feed so the API shape holds:
//
//	in_flight  board workstreams with status active
//	pre_flight board workstreams with status planned or paused
//	landed     board workstreams done within recent_hours (by `updated`)
//	holds      open decisions, risks and new inbox items tagged board or linked
//	           to a board workstream, plus board workstreams with blockers
//
// parked, unreviewed and older done workstreams stay off the board. Torque
// tasks never appear as rows; a workstream carries only their counts.

func boardStrings(v any) []string {
	var out []string
	for _, x := range asAnySlice(v) {
		if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func boardHasTag(item PlannerItem) bool {
	for _, t := range boardStrings(item["tags"]) {
		if t == BoardTag {
			return true
		}
	}
	return false
}

func boardWorkstreamRow(it PlannerItem, counts map[string]int, now time.Time) PlannerBoardRow {
	r := boardTrackerRow("workstreams", "workstream", it, now)
	r.Workstream = ""
	r.Phase = boardText(it, "phase")
	r.Owner = boardText(it, "owner")
	r.TLDR = boardText(it, "tldr")
	if r.TLDR == "" {
		r.TLDR = boardText(it, "state")
	}
	r.NextStep = boardText(it, "next_step")
	r.Blockers = boardStrings(it["blockers"])
	r.Tags = boardStrings(it["tags"])
	if len(counts) > 0 {
		r.TaskCounts = counts
	}
	return r
}

func composeCuratedBoard(in boardInput) PlannerBoard {
	now := in.Now
	if in.RecentHours <= 0 {
		in.RecentHours = DefaultBoardRecentHours
	}
	if in.ActiveHours <= 0 {
		in.ActiveHours = DefaultBoardActiveHours
	}
	if in.Limit <= 0 {
		in.Limit = DefaultBoardLimit
	}
	recentFrom := now.Add(-time.Duration(in.RecentHours * float64(time.Hour)))

	var active, planned, done, blocked []PlannerBoardRow
	boardIDs := map[string]bool{} // ids of board workstreams, with and without the WS- prefix
	for _, it := range in.Workstreams {
		id := boardText(it, "id")
		if id == "" || !boardHasTag(it) {
			continue
		}
		boardIDs[id] = true
		boardIDs[strings.TrimPrefix(id, "WS-")] = true
		row := boardWorkstreamRow(it, in.WSTasks[id], now)
		switch row.Status {
		case "active":
			active = append(active, row)
		case "planned", "paused":
			planned = append(planned, row)
		case "done":
			if row.Updated != "" && !row.edgeTime().Before(recentFrom) {
				done = append(done, row)
			}
		}
		if len(row.Blockers) > 0 && row.Status != "done" {
			blocked = append(blocked, row)
		}
	}
	byPriority := func(rows []PlannerBoardRow) {
		sort.SliceStable(rows, func(i, j int) bool {
			pi, pj := rows[i].Priority, rows[j].Priority
			if pi == 0 {
				pi = 99
			}
			if pj == 0 {
				pj = 99
			}
			if pi != pj {
				return pi < pj
			}
			ti, tj := rows[i].ts(), rows[j].ts()
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			return rows[i].ID < rows[j].ID
		})
	}
	byPriority(active)
	byPriority(planned)
	boardNewest(done)
	byPriority(blocked)

	linked := func(it PlannerItem) bool {
		if boardHasTag(it) {
			return true
		}
		for _, f := range []string{"workstream_ids", "related_ids", "workstream"} {
			var refs []string
			if s, ok := it[f].(string); ok {
				refs = []string{s}
			} else {
				refs = boardStrings(it[f])
			}
			for _, ref := range refs {
				if boardIDs[ref] {
					return true
				}
			}
		}
		return false
	}
	var needs, risks, inbox []PlannerBoardRow
	pick := func(db, source, status string, severity string, into *[]PlannerBoardRow) {
		for _, it := range in.Tracker[db] {
			if boardText(it, "id") == "" || boardText(it, "status") != status || !linked(it) {
				continue
			}
			if severity != "" && boardText(it, "severity") != severity {
				continue
			}
			*into = append(*into, boardTrackerRow(db, source, it, now))
		}
		boardNewest(*into)
	}
	pick("decisions", "decision", "needs-decision", "", &needs)
	pick("risks", "risk", "open", "high", &risks)
	pick("inbox", "inbox", "new", "", &inbox)

	full := map[string][]PlannerBoardRow{
		"in_flight":  active,
		"landed":     done,
		"pre_flight": planned,
		"holds":      concatRows(needs, risks, inbox, blocked),
	}
	b := PlannerBoard{
		Scope:       BoardScopeCurated,
		GeneratedAt: now.UTC().Format(boardISO),
		RecentHours: in.RecentHours,
		ActiveHours: in.ActiveHours,
		Limit:       in.Limit,
		Sections:    map[string][]PlannerBoardRow{},
		Counts:      map[string]int{},
		Totals:      map[string]int{},
		Notices:     []string{},
	}
	for _, k := range PlannerBoardSections {
		rows := full[k]
		b.Totals[k] = len(rows)
		if len(rows) > in.Limit {
			rows = rows[:in.Limit]
		}
		if rows == nil {
			rows = []PlannerBoardRow{}
		}
		b.Sections[k] = rows
		b.Counts[k] = len(rows)
	}
	if len(boardIDs) == 0 {
		untagged := 0
		for _, it := range in.Workstreams {
			if boardText(it, "id") != "" {
				untagged++
			}
		}
		b.Notices = append(b.Notices, fmt.Sprintf("No planner items are tagged board. Tag a workstream with board to show it here (%d workstreams could be tagged).", untagged))
	}
	return b
}

// curatedBoardText renders the curated board: status word, id, title, owner,
// truncated next step, blocker count and task counts, grouped by section.
func curatedBoardText(b PlannerBoard, cut func(string, int) string, idw int) string {
	titles := map[string]string{"in_flight": "IN MOTION", "pre_flight": "NEXT UP", "landed": "RECENTLY LANDED", "holds": "WAITING ON YOU"}
	words := map[string]string{"active": "ACTIVE", "planned": "PLANNED", "paused": "PAUSED", "done": "LANDED", "decision": "DECIDE", "inbox": "TRIAGE", "risk": "RISK"}
	order := []string{"in_flight", "pre_flight", "landed", "holds"}
	out := []string{"BOARD  " + b.GeneratedAt}
	for _, k := range order {
		rows := b.Sections[k]
		more := ""
		if b.Totals[k] > len(rows) {
			more = fmt.Sprintf(" (+%d more)", b.Totals[k]-len(rows))
		}
		out = append(out, "", fmt.Sprintf("%s  %d%s", titles[k], len(rows), more))
		if len(rows) == 0 {
			out = append(out, "  NOTHING")
		}
		for _, r := range rows {
			word := words[r.Status]
			if k == "holds" {
				word = "BLOCKED"
				if r.Source != "workstream" {
					word = words[r.Source]
				}
			} else if word == "" {
				word = strings.ToUpper(r.Status)
			}
			line := fmt.Sprintf("  %-8s %-*s %-48s %-14s", word, idw, r.ID, cut(r.Title, 48), cut(r.Owner, 14))
			if r.Source == "workstream" {
				if r.NextStep != "" {
					line += " next: " + cut(r.NextStep, 60)
				}
				if n := len(r.Blockers); n > 0 {
					line += fmt.Sprintf(" [%d blocker%s]", n, map[bool]string{true: "", false: "s"}[n == 1])
				}
				if tc := boardCountsText(r.TaskCounts); tc != "" {
					line += " tasks: " + tc
				}
			}
			out = append(out, strings.TrimRight(line, " "))
			if k == "holds" && r.Source == "workstream" {
				for _, bl := range r.Blockers {
					out = append(out, "      - "+cut(bl, 100))
				}
			}
		}
	}
	for _, n := range b.Notices {
		out = append(out, "", "NOTICE  "+n)
	}
	return strings.Join(out, "\n")
}

func boardCountsText(counts map[string]int) string {
	var parts []string
	for _, st := range CanonicalStatuses {
		if n := counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	return strings.Join(parts, ", ")
}
