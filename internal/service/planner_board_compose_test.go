package service

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The composition tests are the portfolio-tracker's board.test.mjs, ported:
// same fixtures, same expectations.

var boardNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func hoursAgo(h float64) time.Time { return boardNow.Add(-time.Duration(h * float64(time.Hour))) }

func btask(n int, status string, h float64, over ...func(*BoardTask)) BoardTask {
	t := BoardTask{ID: fmt.Sprintf("CW-20261008-%04d", n), Title: fmt.Sprintf("Task %d", n), Status: status, Priority: 3, UpdatedAt: hoursAgo(h)}
	for _, o := range over {
		o(&t)
	}
	return t
}

func rowIDs(rows []PlannerBoardRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func it(kv ...any) PlannerItem {
	m := PlannerItem{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestBoardEmptyInputGivesFourEmptySections(t *testing.T) {
	b := composeBoard(boardInput{Now: boardNow})
	for _, k := range PlannerBoardSections {
		assert.Empty(t, b.Sections[k], k)
		assert.Equal(t, 0, b.Counts[k])
		assert.Equal(t, 0, b.Totals[k])
	}
	assert.Equal(t, "2026-10-08T12:00:00.000Z", b.GeneratedAt)
	assert.NotNil(t, b.Sections["holds"], "an empty section is [] in JSON, not null")
}

func TestBoardInFlightOrderAndAdoptingIsPreFlight(t *testing.T) {
	b := composeBoard(boardInput{
		Now: boardNow,
		Tasks: map[string][]BoardTask{
			"doing":  {btask(1, "doing", 5), btask(2, "doing", 1)},
			"review": {btask(3, "review", 2)},
		},
		Tracker: map[string][]PlannerItem{"roadmap": {
			it("id", "RM-a", "title", "A", "status", "in-progress", "updated", "2026-10-08", "workstream_ids", []any{"WS-alpha"}),
			it("id", "RM-b", "title", "B", "status", "adopting", "updated", "2026-10-07"),
			it("id", "RM-c", "title", "C", "status", "idea", "updated", "2026-10-08"),
		}},
	})
	assert.Equal(t, []string{"CW-20261008-0002", "CW-20261008-0001", "RM-a", "CW-20261008-0003"}, rowIDs(b.Sections["in_flight"]))
	assert.Equal(t, []string{"RM-b"}, rowIDs(b.Sections["pre_flight"]))
	rm := b.Sections["in_flight"][2]
	assert.Equal(t, "roadmap", rm.Source)
	assert.Equal(t, "roadmap", rm.DB)
	assert.Equal(t, "alpha", rm.Workstream)
	assert.Equal(t, "", b.Sections["in_flight"][0].DB)
	assert.Equal(t, "torque", b.Sections["in_flight"][0].Source)
}

func TestBoardWorkstreamFromTag(t *testing.T) {
	assert.Equal(t, "fabric-vnext", boardTaskWorkstream([]string{"x", "ws-fabric-vnext"}))
	assert.Equal(t, "", boardTaskWorkstream([]string{"x"}))
	assert.Equal(t, "", boardTaskWorkstream(nil))
	b := composeBoard(boardInput{Now: boardNow, Tasks: map[string][]BoardTask{"doing": {btask(1, "doing", 1, func(t *BoardTask) { t.Tags = []string{"ws-nanite"} })}}})
	assert.Equal(t, "nanite", b.Sections["in_flight"][0].Workstream)
}

func TestBoardLandedWindows(t *testing.T) {
	b := composeBoard(boardInput{
		Now: boardNow, RecentHours: 24,
		Tasks: map[string][]BoardTask{"done": {btask(1, "done", 2), btask(2, "done", 30), btask(3, "done", 23), btask(4, "doing", 1)}},
		Tracker: map[string][]PlannerItem{
			"roadmap": {
				it("id", "RM-new", "title", "n", "status", "landed", "updated", "2026-10-02"), // 6 days ago: in
				it("id", "RM-old", "title", "o", "status", "done", "updated", "2026-09-30"),   // 8 days ago: out
				it("id", "RM-bad", "title", "b", "status", "landed"),                          // no date: out
			},
			"decisions": {
				it("id", "DEC-1", "title", "d1", "status", "decided", "decided_at", "2026-10-08"),
				it("id", "DEC-2", "title", "d2", "status", "decided", "date", "2026-10-07"),                          // end of 10-07 is 12 h ago: in
				it("id", "DEC-3", "title", "d3", "status", "decided", "date", "2026-10-05", "updated", "2026-10-08"), // decision date wins over edits
				it("id", "DEC-4", "title", "d4", "status", "reversed", "decided_at", "2026-10-08"),
			},
		},
	})
	assert.ElementsMatch(t, []string{"CW-20261008-0001", "CW-20261008-0003", "DEC-1", "DEC-2", "RM-new"}, rowIDs(b.Sections["landed"]))
	for i := 1; i < len(b.Sections["landed"]); i++ {
		assert.False(t, b.Sections["landed"][i].ts().After(b.Sections["landed"][i-1].ts()), "newest first")
	}
	for _, r := range b.Sections["landed"] {
		if r.ID == "DEC-1" {
			assert.Equal(t, "decision", r.Source)
		}
	}
}

func TestBoardPreFlightIsQueuedAndPlannedNowNextOnly(t *testing.T) {
	b := composeBoard(boardInput{
		Now: boardNow,
		Tasks: map[string][]BoardTask{"queued": {
			btask(1, "queued", 3, func(t *BoardTask) { t.Priority = 2 }),
			btask(2, "queued", 1, func(t *BoardTask) { t.Priority = 1 }),
			btask(3, "todo", 1),
		}},
		Tracker: map[string][]PlannerItem{"roadmap": {
			it("id", "RM-later", "title", "l", "status", "planned", "horizon", "later", "updated", "2026-10-08"),
			it("id", "RM-next", "title", "x", "status", "planned", "horizon", "next", "updated", "2026-10-08", "order", 10.0),
			it("id", "RM-now", "title", "n", "status", "planned", "horizon", "now", "updated", "2026-10-08", "order", 50.0),
			it("id", "RM-idea", "title", "i", "status", "idea", "horizon", "now", "updated", "2026-10-08"),
		}},
	})
	assert.Equal(t, []string{"CW-20261008-0002", "CW-20261008-0001", "RM-now", "RM-next"}, rowIDs(b.Sections["pre_flight"]))
	assert.Equal(t, 1, b.Sections["pre_flight"][0].Priority)
}

func TestBoardHolds(t *testing.T) {
	b := composeBoard(boardInput{
		Now: boardNow,
		Tracker: map[string][]PlannerItem{
			"decisions": {it("id", "DEC-1", "title", "a", "status", "needs-decision", "updated", "2026-10-08"), it("id", "DEC-2", "title", "b", "status", "deferred")},
			"inbox": {
				it("id", "IN-1", "title", "i", "status", "new", "updated", "2026-10-08"),
				it("id", "IN-2", "title", "j", "status", "triaged"),
				it("id", "IN-3", "title", "k", "status", "dismissed"),
			},
			"risks": {
				it("id", "RK-1", "title", "r", "status", "open", "severity", "high", "updated", "2026-10-08"),
				it("id", "RK-2", "title", "s", "status", "open", "severity", "medium"),
				it("id", "RK-3", "title", "t", "status", "closed", "severity", "high"),
			},
		},
	})
	assert.Equal(t, []string{"DEC-1", "RK-1", "IN-1"}, rowIDs(b.Sections["holds"]))
	var sources []string
	for _, r := range b.Sections["holds"] {
		sources = append(sources, r.Source)
	}
	assert.Equal(t, []string{"decision", "risk", "inbox"}, sources)
}

func TestBoardLimitPerSectionAndTorqueTotalsCarryThrough(t *testing.T) {
	var many []BoardTask
	for i := 0; i < 5; i++ {
		many = append(many, btask(i+1, "doing", float64(i+1)))
	}
	b := composeBoard(boardInput{Now: boardNow, Limit: 2, Tasks: map[string][]BoardTask{"doing": many}, TaskTotals: map[string]int{"doing": 40}})
	assert.Len(t, b.Sections["in_flight"], 2)
	assert.Equal(t, 2, b.Counts["in_flight"])
	assert.Equal(t, 40, b.Totals["in_flight"])
}

func TestBoardUntidyInputDoesNotPanic(t *testing.T) {
	b := composeBoard(boardInput{
		Now:   boardNow,
		Tasks: map[string][]BoardTask{"doing": {{ID: "CW-20261008-0001", Status: "doing"}, {}}},
		Tracker: map[string][]PlannerItem{
			"roadmap":   {nil, it("id", "RM-x", "status", "in-progress")},
			"decisions": nil,
		},
	})
	require.Len(t, b.Sections["in_flight"], 2)
	assert.Equal(t, "CW-20261008-0001", b.Sections["in_flight"][0].Title, "a missing title falls back to the id")
	assert.Equal(t, "", boardISOOf("", boardNow))
	assert.Equal(t, "", boardISOOf("garbage", boardNow))
	assert.Equal(t, "2026-10-08T12:00:00.000Z", boardISOOf("2027-01-01", boardNow), "never after now")
}

func TestBoardIdleDoingTaskIsPreparingAndACommentKeepsItInFlight(t *testing.T) {
	tasks := map[string][]BoardTask{"doing": {btask(1, "doing", 1), btask(2, "doing", 6), btask(3, "doing", 6), btask(4, "doing", 6)}}
	activity := map[string]time.Time{"CW-20261008-0002": hoursAgo(0.5), "CW-20261008-0003": hoursAgo(5)}
	b := composeBoard(boardInput{Now: boardNow, Tasks: tasks, Activity: activity})
	assert.Equal(t, []string{"CW-20261008-0001", "CW-20261008-0002"}, rowIDs(b.Sections["in_flight"]))
	assert.ElementsMatch(t, []string{"CW-20261008-0003", "CW-20261008-0004"}, rowIDs(b.Sections["pre_flight"]))
	assert.Equal(t, 2, b.Totals["in_flight"])
	assert.Equal(t, 2, b.Totals["pre_flight"])
	assert.Regexp(t, `PREPARING CW-20261008-0003`, BoardText(b))

	all := composeBoard(boardInput{Now: boardNow, Tasks: tasks})
	assert.Len(t, all.Sections["in_flight"], 4, "no activity signal: every doing task counts as active")
	wide := composeBoard(boardInput{Now: boardNow, Tasks: tasks, Activity: activity, ActiveHours: 8})
	assert.Len(t, wide.Sections["in_flight"], 4)
}

func TestBoardText(t *testing.T) {
	b := composeBoard(boardInput{Now: boardNow, Tasks: map[string][]BoardTask{
		"doing":  {btask(1, "doing", 2, func(t *BoardTask) { t.Title = strings.Repeat("x", 100) })},
		"review": {btask(2, "review", 50)},
	}})
	b.Notices = []string{"Torque is unavailable"}
	text := BoardText(b)
	lines := strings.Split(text, "\n")
	has := func(re string) bool {
		for _, l := range lines {
			if regexp.MustCompile(re).MatchString(l) {
				return true
			}
		}
		return false
	}
	assert.True(t, has(`^ {2}IN FLIGHT\s+CW-20261008-0001\s+x+…\s+2h$`), text)
	assert.True(t, has(`^ {2}ARRIVING\s+CW-20261008-0002.*2d$`), text)
	assert.Contains(t, lines, "  NOTHING QUEUED")
	assert.Contains(t, lines, "NOTICE  Torque is unavailable")
	assert.True(t, strings.HasPrefix(text, "DEPARTURES  2026-10-08T12:00:00.000Z"))
}
