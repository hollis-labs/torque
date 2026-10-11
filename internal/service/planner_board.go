package service

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// The planner board ("departures"): what is in flight, what just landed,
// what is preparing, and what is waiting on the owner. A pure composition of
// planner items and Torque tasks into four sections, ported from the
// portfolio-tracker's board.mjs; Torque tasks now come straight from the
// store instead of over REST.
//
//	in_flight  ACTIVELY WORKED: Torque doing with activity (update or comment)
//	           within active_hours, roadmap in-progress, Torque review (ARRIVING).
//	           A doing task with no recent activity is preparing, not executing.
//	landed     Torque done updated within recent_hours, roadmap landed/done
//	           updated within 7 days, decisions decided within recent_hours.
//	pre_flight preparing, not executing: Torque doing with no recent activity
//	           (PREPARING), Torque queued, roadmap adopting (BOARDING), roadmap
//	           planned with horizon now or next. Never the todo backlog.
//	holds      decisions needs-decision, open high risks, inbox new.
//
// Planner dates are plain days: `updated` shows the start of that day (an
// honest "at most this old" age), while window checks count the day as ending
// at its last second, so "decided today" is always recent.

// Board sections, in display order.
var PlannerBoardSections = []string{"in_flight", "landed", "pre_flight", "holds"}

// Board defaults and limits.
const (
	DefaultBoardRecentHours = 72
	DefaultBoardActiveHours = 2
	DefaultBoardLimit       = 30
	boardRoadmapLandedDays  = 7
	maxBoardRecentHours     = 24 * 90
	maxBoardActiveHours     = 24 * 30
	maxBoardLimit           = 200
	boardISO                = "2006-01-02T15:04:05.000Z"
)

// PlannerBoardRow is one line of the board.
type PlannerBoardRow struct {
	Source     string `json:"source"` // torque | roadmap | decision | inbox | risk
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Priority   int    `json:"priority,omitempty"`
	Workstream string `json:"workstream,omitempty"`
	Updated    string `json:"updated"`
	DB         string `json:"db,omitempty"` // planner rows only

	edge time.Time // the instant window checks use (end of a plain day); zero = use Updated
}

// PlannerBoard is the composed board.
type PlannerBoard struct {
	GeneratedAt string                       `json:"generated_at"`
	RecentHours float64                      `json:"recent_hours"`
	ActiveHours float64                      `json:"active_hours"`
	Limit       int                          `json:"limit"`
	Sections    map[string][]PlannerBoardRow `json:"sections"`
	Counts      map[string]int               `json:"counts"` // rows shown per section
	Totals      map[string]int               `json:"totals"` // rows before the limit
	Notices     []string                     `json:"notices"`
}

// BoardTask is the part of a Torque task the board needs.
type BoardTask struct {
	ID        string
	Title     string
	Status    string
	Priority  int
	Tags      []string
	UpdatedAt time.Time
}

// boardInput is what composeBoard works from. No I/O.
type boardInput struct {
	Now         time.Time
	RecentHours float64
	ActiveHours float64
	Limit       int
	Tasks       map[string][]BoardTask // doing, review, queued, done
	TaskTotals  map[string]int         // Torque's own totals per status, when more were not fetched
	Activity    map[string]time.Time   // latest comment per task id; nil = the signal was unavailable
	Tracker     map[string][]PlannerItem
}

var (
	boardWSTag = regexp.MustCompile(`^ws-(.+)$`)
	boardDay   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// boardISOOf turns a planner date ("2026-10-08": start of that day) or an
// RFC 3339 string into an ISO instant, never after now; "" when unusable.
func boardISOOf(value string, now time.Time) string {
	t, ok := boardParse(value)
	if !ok {
		return ""
	}
	if t.After(now) {
		t = now
	}
	return t.UTC().Format(boardISO)
}

func boardParse(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// boardTaskWorkstream is the <name> of a ws-<name> tag; "" when none.
func boardTaskWorkstream(tags []string) string {
	for _, tag := range tags {
		if m := boardWSTag.FindStringSubmatch(tag); m != nil {
			return m[1]
		}
	}
	return ""
}

func boardTrackerWorkstream(item PlannerItem) string {
	ids, _ := item["workstream_ids"].([]any)
	if len(ids) == 0 {
		return ""
	}
	return strings.TrimPrefix(fmt.Sprint(ids[0]), "WS-")
}

func boardText(m PlannerItem, k string) string {
	s, _ := m[k].(string)
	return s
}

func boardInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		if n == math.Trunc(n) {
			return int(n)
		}
	}
	return 0
}

func (r *PlannerBoardRow) ts() time.Time {
	t, _ := time.Parse(boardISO, r.Updated)
	return t
}

func (r *PlannerBoardRow) edgeTime() time.Time {
	if !r.edge.IsZero() {
		return r.edge
	}
	return r.ts()
}

func boardTorqueRow(t BoardTask, now time.Time) PlannerBoardRow {
	r := PlannerBoardRow{Source: "torque", ID: t.ID, Title: t.Title, Status: t.Status, Workstream: boardTaskWorkstream(t.Tags)}
	if r.Title == "" {
		r.Title = t.ID
	}
	if t.Priority > 0 {
		r.Priority = t.Priority
	}
	if !t.UpdatedAt.IsZero() {
		r.Updated = boardISOOf(t.UpdatedAt.UTC().Format(time.RFC3339Nano), now)
	}
	return r
}

func boardTrackerRow(db, source string, item PlannerItem, now time.Time, dateFields ...string) PlannerBoardRow {
	if len(dateFields) == 0 {
		dateFields = []string{"updated", "created"}
	}
	raw := ""
	for _, f := range dateFields {
		if v := boardText(item, f); v != "" {
			raw = v
			break
		}
	}
	id := boardText(item, "id")
	r := PlannerBoardRow{Source: source, ID: id, Title: boardText(item, "title"), Status: boardText(item, "status"), DB: db, Workstream: boardTrackerWorkstream(item)}
	if r.Title == "" {
		r.Title = id
	}
	if p := boardInt(item["priority"]); p > 0 {
		r.Priority = p
	}
	r.Updated = boardISOOf(raw, now)
	if boardDay.MatchString(raw) && r.Updated != "" {
		end, _ := time.Parse(time.RFC3339, raw+"T23:59:59Z")
		if end.After(now) {
			end = now
		}
		r.edge = end
	}
	return r
}

// newest sorts rows newest first, then by id.
func boardNewest(rows []PlannerBoardRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := rows[i].ts(), rows[j].ts()
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return rows[i].ID < rows[j].ID
	})
}

// composeBoard builds the four sections from the inputs. Untidy input (missing
// titles, dates, empty lists) never panics.
func composeBoard(in boardInput) PlannerBoard {
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
	hour := time.Hour
	recentFrom := now.Add(-time.Duration(in.RecentHours * float64(hour)))
	roadmapFrom := now.Add(-boardRoadmapLandedDays * 24 * hour)
	inWindow := func(r PlannerBoardRow, from time.Time) bool { return r.Updated != "" && !r.edgeTime().Before(from) }

	torque := func(status string) []PlannerBoardRow {
		var out []PlannerBoardRow
		for _, t := range in.Tasks[status] {
			if t.ID != "" && t.Status == status {
				out = append(out, boardTorqueRow(t, now))
			}
		}
		return out
	}
	road := func(statuses ...string) []PlannerBoardRow {
		var out []PlannerBoardRow
		for _, it := range in.Tracker["roadmap"] {
			if boardText(it, "id") == "" {
				continue
			}
			for _, s := range statuses {
				if boardText(it, "status") == s {
					out = append(out, boardTrackerRow("roadmap", "roadmap", it, now))
					break
				}
			}
		}
		return out
	}
	filter := func(rows []PlannerBoardRow, keep func(PlannerBoardRow) bool) []PlannerBoardRow {
		var out []PlannerBoardRow
		for _, r := range rows {
			if keep(r) {
				out = append(out, r)
			}
		}
		return out
	}

	doingAll := torque("doing")
	boardNewest(doingAll)
	lastTouch := func(r PlannerBoardRow) time.Time {
		t := r.ts()
		if a, ok := in.Activity[r.ID]; ok && a.After(t) {
			return a
		}
		return t
	}
	isActive := func(r PlannerBoardRow) bool {
		return in.Activity == nil || !lastTouch(r).Before(now.Add(-time.Duration(in.ActiveHours*float64(hour))))
	}
	doing := filter(doingAll, isActive)
	preparing := filter(doingAll, func(r PlannerBoardRow) bool { return !isActive(r) })
	review := torque("review")
	boardNewest(review)
	queued := torque("queued")
	sort.SliceStable(queued, func(i, j int) bool {
		pi, pj := queued[i].Priority, queued[j].Priority
		if pi == 0 {
			pi = 99
		}
		if pj == 0 {
			pj = 99
		}
		if pi != pj {
			return pi < pj
		}
		ti, tj := queued[i].ts(), queued[j].ts()
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return queued[i].ID < queued[j].ID
	})
	doneAllRows := torque("done")
	done := filter(doneAllRows, func(r PlannerBoardRow) bool { return inWindow(r, recentFrom) })
	roadFlight := road("in-progress")
	boardNewest(roadFlight)
	roadBoarding := road("adopting")
	boardNewest(roadBoarding)
	roadLanded := filter(road("landed", "done"), func(r PlannerBoardRow) bool { return inWindow(r, roadmapFrom) })

	var plannedItems []PlannerItem
	for _, it := range in.Tracker["roadmap"] {
		h := boardText(it, "horizon")
		if boardText(it, "id") != "" && boardText(it, "status") == "planned" && (h == "now" || h == "next") {
			plannedItems = append(plannedItems, it)
		}
	}
	orderOf := func(it PlannerItem) float64 {
		if o, ok := it["order"].(float64); ok {
			return o
		}
		return 1e9
	}
	sort.SliceStable(plannedItems, func(i, j int) bool {
		hi, hj := boardText(plannedItems[i], "horizon"), boardText(plannedItems[j], "horizon")
		if hi != hj {
			return hi == "now"
		}
		return orderOf(plannedItems[i]) < orderOf(plannedItems[j])
	})
	roadPlanned := make([]PlannerBoardRow, 0, len(plannedItems))
	for _, it := range plannedItems {
		roadPlanned = append(roadPlanned, boardTrackerRow("roadmap", "roadmap", it, now))
	}

	byStatus := func(db, source, status string, fields ...string) []PlannerBoardRow {
		var out []PlannerBoardRow
		for _, it := range in.Tracker[db] {
			if boardText(it, "id") != "" && boardText(it, "status") == status {
				out = append(out, boardTrackerRow(db, source, it, now, fields...))
			}
		}
		return out
	}
	decided := filter(byStatus("decisions", "decision", "decided", "decided_at", "date", "updated"), func(r PlannerBoardRow) bool { return inWindow(r, recentFrom) })
	needs := byStatus("decisions", "decision", "needs-decision")
	boardNewest(needs)
	inbox := byStatus("inbox", "inbox", "new")
	boardNewest(inbox)
	var risks []PlannerBoardRow
	for _, it := range in.Tracker["risks"] {
		if boardText(it, "id") != "" && boardText(it, "status") == "open" && boardText(it, "severity") == "high" {
			risks = append(risks, boardTrackerRow("risks", "risk", it, now))
		}
	}
	boardNewest(risks)

	// doing first, then roadmap work under way, then review (arriving)
	inFlight := concatRows(doing, roadFlight, review)
	landed := concatRows(done, roadLanded, decided)
	boardNewest(landed)
	preFlight := concatRows(preparing, queued, roadBoarding, roadPlanned)
	holds := concatRows(needs, risks, inbox)

	full := map[string][]PlannerBoardRow{"in_flight": inFlight, "landed": landed, "pre_flight": preFlight, "holds": holds}
	b := PlannerBoard{
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
		if len(rows) > in.Limit {
			rows = rows[:in.Limit]
		}
		if rows == nil {
			rows = []PlannerBoardRow{}
		}
		b.Sections[k] = rows
		b.Counts[k] = len(rows)
	}
	// Torque rows beyond what was fetched: Torque's own total minus what we hold.
	extra := func(status string, held int) int {
		total := in.TaskTotals[status]
		if total < held {
			total = held
		}
		return total - held
	}
	b.Totals["in_flight"] = len(inFlight) + extra("doing", len(doingAll)) + extra("review", len(review))
	b.Totals["landed"] = len(landed) + extra("done", len(doneAllRows))
	b.Totals["pre_flight"] = len(preFlight) + extra("queued", len(queued))
	b.Totals["holds"] = len(holds)
	return b
}

func concatRows(parts ...[]PlannerBoardRow) []PlannerBoardRow {
	var out []PlannerBoardRow
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ---------- text ----------

// BoardText renders the board compactly, one row per line (status word, id,
// truncated title, workstream, age). This is the agents' quick check-in.
func BoardText(b PlannerBoard) string {
	now, _ := time.Parse(boardISO, b.GeneratedAt)
	words := map[string]string{"doing": "IN FLIGHT", "review": "ARRIVING", "done": "LANDED", "landed": "LANDED", "decided": "LANDED", "queued": "QUEUED", "planned": "QUEUED", "in-progress": "IN FLIGHT", "adopting": "BOARDING"}
	hold := map[string]string{"decision": "DECIDE", "inbox": "TRIAGE", "risk": "RISK"}
	titles := map[string]string{"in_flight": "IN FLIGHT", "landed": "RECENTLY LANDED", "pre_flight": "PRE-FLIGHT", "holds": "HOLDS (WAITING ON YOU)"}
	age := func(iso string) string {
		t, err := time.Parse(boardISO, iso)
		if err != nil {
			return ""
		}
		m := int(math.Max(0, math.Round(now.Sub(t).Minutes())))
		switch {
		case m < 60:
			return fmt.Sprintf("%dm", m)
		case m < 2880:
			return fmt.Sprintf("%dh", int(math.Round(float64(m)/60)))
		}
		return fmt.Sprintf("%dd", int(math.Round(float64(m)/1440)))
	}
	cut := func(s string, n int) string {
		r := []rune(s)
		if len(r) > n {
			return string(r[:n-1]) + "…"
		}
		return s
	}
	idw := 16
	for _, k := range PlannerBoardSections {
		for _, r := range b.Sections[k] {
			if l := len([]rune(r.ID)); l > idw {
				idw = l
			}
		}
	}
	if idw > 30 {
		idw = 30
	}
	out := []string{"DEPARTURES  " + b.GeneratedAt}
	for _, k := range PlannerBoardSections {
		rows := b.Sections[k]
		more := ""
		if b.Totals[k] > len(rows) {
			more = fmt.Sprintf(" (+%d more)", b.Totals[k]-len(rows))
		}
		out = append(out, "", fmt.Sprintf("%s  %d%s", titles[k], len(rows), more))
		if len(rows) == 0 {
			if k == "pre_flight" {
				out = append(out, "  NOTHING QUEUED")
			} else {
				out = append(out, "  NOTHING")
			}
		}
		for _, r := range rows {
			word := words[r.Status]
			switch {
			case k == "holds":
				if word = hold[r.Source]; word == "" {
					word = "HOLD"
				}
			case k == "pre_flight" && r.Status == "doing":
				word = "PREPARING"
			case word == "":
				word = strings.ToUpper(r.Status)
			}
			line := fmt.Sprintf("  %-9s %-*s %-56s %-18s %4s", word, idw, r.ID, cut(r.Title, 56), cut(r.Workstream, 18), age(r.Updated))
			out = append(out, strings.TrimRight(line, " "))
		}
	}
	for _, n := range b.Notices {
		out = append(out, "", "NOTICE  "+n)
	}
	return strings.Join(out, "\n")
}

// ---------- loading ----------

// PlannerBoardOptions are the knobs of one board read; zero values take the defaults.
type PlannerBoardOptions struct {
	RecentHours float64
	ActiveHours float64
	Limit       int
}

// Board composes the board from the planner tables and the Torque task store.
func (s *PlannerService) Board(opts PlannerBoardOptions) (*PlannerBoard, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	if opts.RecentHours == 0 {
		opts.RecentHours = DefaultBoardRecentHours
	}
	if opts.ActiveHours == 0 {
		opts.ActiveHours = DefaultBoardActiveHours
	}
	if opts.Limit == 0 {
		opts.Limit = DefaultBoardLimit
	}
	if !(opts.RecentHours > 0 && opts.RecentHours <= maxBoardRecentHours) {
		return nil, &ValidationError{Field: "recent_hours", Message: fmt.Sprintf("recent_hours must be greater than 0 and at most %d", maxBoardRecentHours)}
	}
	if !(opts.ActiveHours > 0 && opts.ActiveHours <= maxBoardActiveHours) {
		return nil, &ValidationError{Field: "active_hours", Message: fmt.Sprintf("active_hours must be greater than 0 and at most %d", maxBoardActiveHours)}
	}
	if opts.Limit < 1 || opts.Limit > maxBoardLimit {
		return nil, &ValidationError{Field: "limit", Message: fmt.Sprintf("limit must be 1-%d", maxBoardLimit)}
	}

	now := s.now().UTC()
	in := boardInput{
		Now: now, RecentHours: opts.RecentHours, ActiveHours: opts.ActiveHours, Limit: opts.Limit,
		Tasks: map[string][]BoardTask{}, TaskTotals: map[string]int{}, Tracker: map[string][]PlannerItem{},
	}
	for _, db := range []string{"roadmap", "decisions", "inbox", "risks"} {
		items, err := s.store.PlannerList(db)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			in.Tracker[db] = append(in.Tracker[db], plannerClone(it))
		}
	}

	var notices []string
	for _, status := range []string{"doing", "review", "queued", "done"} {
		f := sqlstore.TaskFilter{Status: status, SortBy: "updated_at", SortDir: "desc", Limit: opts.Limit, ExcludeInternal: true}
		if status == "done" {
			// one minute resolution, so repeated reads share work
			from := now.Add(-time.Duration(opts.RecentHours * float64(time.Hour))).Truncate(time.Minute)
			f.UpdatedAfter = from.Format(sqlstore.SQLiteDatetimeLayout)
		}
		page, err := s.store.ListTasksPage(f)
		if err != nil {
			notices = append(notices, fmt.Sprintf("Torque %s tasks unavailable: %v", status, err))
			continue
		}
		in.TaskTotals[status] = page.Total
		for _, t := range page.Tasks {
			bt := BoardTask{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, UpdatedAt: t.UpdatedAt}
			if tags, err := s.store.ListTaskTags(t.ID); err == nil {
				for _, tg := range tags {
					bt.Tags = append(bt.Tags, tg.Slug)
				}
			}
			in.Tasks[status] = append(in.Tasks[status], bt)
		}
	}
	// Torque does not bump updated_at on a comment, so a working agent would
	// look idle: the latest comment on each doing task counts as activity.
	if doing := in.Tasks["doing"]; len(doing) > 0 {
		ids := make([]string, len(doing))
		for i, t := range doing {
			ids[i] = t.ID
		}
		if latest, err := s.store.LatestCommentAt("task", ids); err == nil {
			in.Activity = latest
		} else {
			notices = append(notices, "Task comments unavailable; every doing task is shown as in flight.")
		}
	} else {
		in.Activity = map[string]time.Time{}
	}

	b := composeBoard(in)
	b.Notices = append(b.Notices, notices...)
	return &b, nil
}
