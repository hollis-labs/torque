package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPlannerNextSeqUsesDecimalIDs(t *testing.T) {
	for _, tc := range []struct {
		db   string
		ids  []string
		want string
	}{
		{"priorities", []string{"PR-08", "PR-19", "PR-22"}, "PR-23"},
		{"decisions", []string{"DEC-008", "DEC-019", "DEC-022"}, "DEC-023"},
		{"risks", []string{"RK-008", "RK-019", "RK-022"}, "RK-023"},
		{"inbox", []string{"IN-008", "IN-019", "IN-022"}, "IN-023"},
	} {
		t.Run(tc.db, func(t *testing.T) {
			assert.Equal(t, tc.want, plannerNextSeq(tc.db, tc.ids))
		})
	}
}

func TestPlannerAddCommentUsesDecimalIDs(t *testing.T) {
	item := PlannerItem{"comments": []any{
		map[string]any{"id": "c-008", "text": "First imported comment"},
		map[string]any{"id": "c-019", "text": "Second imported comment"},
		map[string]any{"id": "c-022", "text": "Last imported comment"},
	}}
	plannerAddComment(item, "tester", "New comment", "", time.Now())
	comments := item["comments"].([]any)
	assert.Equal(t, "c-23", comments[len(comments)-1].(map[string]any)["id"])
}
