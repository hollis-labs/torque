package mcpadapter_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
)

// taskGetPayload mirrors torque_task_get's comments-bearing response shape.
// Comments/CommentsMeta are pointers so a test can tell "absent" (the
// comments="false" opt-out) from "present but empty".
type taskGetPayload struct {
	ID       string `json:"ID"`
	Title    string `json:"Title"`
	Comments *[]struct {
		ID      int64  `json:"id"`
		Author  string `json:"author"`
		Content string `json:"content"`
	} `json:"Comments"`
	CommentsMeta *struct {
		Returned  int    `json:"returned"`
		Total     int    `json:"total"`
		Omitted   int    `json:"omitted"`
		Truncated bool   `json:"truncated"`
		Hint      string `json:"hint"`
	} `json:"CommentsMeta"`
}

// seedTaskWithComments creates a task and posts n comments on it, numbered
// 1..n in creation order. body renders each comment's content from its index
// so a caller can inflate the thread past the byte cap without a fixture.
func seedTaskWithComments(t *testing.T, a *mcpadapter.Adapter, n int, body func(i int) string) string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "comment-tail-target",
		"description": "task_get comment tail",
	})
	require.False(t, isErr, "create task: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)
	require.NotEmpty(t, taskID)

	for i := 1; i <= n; i++ {
		text, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task",
			"entity_id":   taskID,
			"author":      fmt.Sprintf("author-%d", i),
			"content":     body(i),
		})
		require.False(t, isErr, "add comment %d: %s", i, text)
	}
	return taskID
}

func numberedBody(i int) string { return fmt.Sprintf("comment number %d", i) }

// TestTaskGet_CommentTailOrder is the core contract: the window is the
// NEWEST comments_limit comments, presented oldest → newest.
func TestTaskGet_CommentTailOrder(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 5, numberedBody)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":             taskID,
		"comments_limit": "3",
	})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments, "Comments must be present by default")
	require.NotNil(t, got.CommentsMeta)

	// Newest three (3,4,5) — not the oldest three — read forward.
	var contents []string
	for _, c := range *got.Comments {
		contents = append(contents, c.Content)
	}
	assert.Equal(t, []string{
		"comment number 3",
		"comment number 4",
		"comment number 5",
	}, contents, "window is the tail, ordered oldest → newest")

	assert.Equal(t, 3, got.CommentsMeta.Returned)
	assert.Equal(t, 5, got.CommentsMeta.Total)
	assert.Equal(t, 2, got.CommentsMeta.Omitted)
	assert.True(t, got.CommentsMeta.Truncated)
	assert.Contains(t, got.CommentsMeta.Hint, "torque_comment_list")
	assert.Contains(t, got.CommentsMeta.Hint, taskID)
}

// TestTaskGet_CommentsIncludedByDefault covers the default window and the
// "nothing was omitted" statement — the rule that a reader must never infer
// completeness from array length.
func TestTaskGet_CommentsIncludedByDefault(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 2, numberedBody)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments)
	require.NotNil(t, got.CommentsMeta)
	assert.Len(t, *got.Comments, 2)
	assert.Equal(t, 2, got.CommentsMeta.Returned)
	assert.Equal(t, 2, got.CommentsMeta.Total)
	assert.Equal(t, 0, got.CommentsMeta.Omitted, "omitted is stated even when zero")
	assert.False(t, got.CommentsMeta.Truncated)
	assert.Empty(t, got.CommentsMeta.Hint, "no hint when nothing was cut")
}

// TestTaskGet_CommentsMetaOnEmptyThread: a task with no comments still gets
// CommentsMeta, so "no comments" and "comments not requested" stay distinct.
func TestTaskGet_CommentsMetaOnEmptyThread(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 0, numberedBody)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments)
	require.NotNil(t, got.CommentsMeta)
	assert.Empty(t, *got.Comments)
	assert.Equal(t, 0, got.CommentsMeta.Total)
	assert.False(t, got.CommentsMeta.Truncated)
	// An empty thread must serialize as [] rather than null.
	assert.Contains(t, text, `"Comments": []`)
}

func TestTaskGet_CommentsOptOut(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 3, numberedBody)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":       taskID,
		"comments": "false",
	})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	assert.Nil(t, got.Comments, "opt-out omits Comments entirely, not an empty array")
	assert.Nil(t, got.CommentsMeta, "opt-out omits CommentsMeta entirely")
	assert.Equal(t, taskID, got.ID)
}

// TestTaskGet_CommentsLimitClamped: comments_limit above the max is clamped
// rather than honored.
func TestTaskGet_CommentsLimitClamped(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 12, numberedBody)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":             taskID,
		"comments_limit": "9999",
	})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.CommentsMeta)
	assert.Equal(t, 12, got.CommentsMeta.Returned, "12 < the 100 clamp, so all are returned")
	assert.Equal(t, 0, got.CommentsMeta.Omitted)
}

// TestTaskGet_ByteCapDropsOldestFirst is the direction test. cappedJSONResult
// trims a list from the tail to keep the newest; this path presents oldest →
// newest, so the newest are at the END and the cap must eat from the FRONT.
// Reusing the list helper's drop direction here would keep exactly the stale
// comments and discard the fresh corrections.
func TestTaskGet_ByteCapDropsOldestFirst(t *testing.T) {
	a := setupAdapter(t)
	// ~12KB per comment × 12 = ~144KB, comfortably past the 100KB cap.
	const per = 12 * 1024
	body := func(i int) string {
		return fmt.Sprintf("MARK-%02d-", i) + strings.Repeat("x", per)
	}
	taskID := seedTaskWithComments(t, a, 12, body)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":             taskID,
		"comments_limit": "12",
	})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments)
	require.NotNil(t, got.CommentsMeta)

	window := *got.Comments
	require.NotEmpty(t, window, "the cap must not empty the window on a normal-sized record")
	assert.Less(t, len(window), 12, "the thread does not fit; some comments must be dropped")

	// The NEWEST survived; the OLDEST did not.
	assert.True(t, strings.HasPrefix(window[len(window)-1].Content, "MARK-12-"),
		"newest comment must survive the cap")
	assert.True(t, strings.HasPrefix(window[0].Content, "MARK-"), "window is still contiguous")
	assert.False(t, strings.HasPrefix(window[0].Content, "MARK-01-"),
		"oldest comment must be dropped first")

	assert.Equal(t, 12, got.CommentsMeta.Total, "total counts the whole thread, not the window")
	assert.Equal(t, 12-len(window), got.CommentsMeta.Omitted)
	assert.True(t, got.CommentsMeta.Truncated)
	assert.Contains(t, got.CommentsMeta.Hint, "torque_comment_list")

	// And the response actually fits the transport budget it claims to.
	assert.LessOrEqual(t, len(text), 100*1024, "capped response must fit maxMCPResponseBytes")
}

// TestTaskGet_OversizedRecordAdmitsOmission: when the record ALONE blows the
// cap, the comments all go but the response says so rather than failing
// silently or quietly trimming the record.
func TestTaskGet_OversizedRecordAdmitsOmission(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "oversized-record",
		"description": strings.Repeat("d", 120*1024),
	})
	require.False(t, isErr, "create task: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	for i := 1; i <= 3; i++ {
		_, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task",
			"entity_id":   taskID,
			"author":      "someone",
			"content":     numberedBody(i),
		})
		require.False(t, isErr)
	}

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr, "task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments)
	require.NotNil(t, got.CommentsMeta)
	assert.Empty(t, *got.Comments)
	assert.Equal(t, 3, got.CommentsMeta.Total)
	assert.Equal(t, 3, got.CommentsMeta.Omitted, "omitted equals total when nothing fit")
	assert.True(t, got.CommentsMeta.Truncated)
	assert.Contains(t, got.CommentsMeta.Hint, "exceeds")
	assert.Contains(t, got.CommentsMeta.Hint, "torque_comment_list")
	// The record itself is intact — never truncated behind the caller's back.
	assert.Contains(t, text, strings.Repeat("d", 120*1024), "the description must survive whole")
}

// TestLoopbackTaskGet_ReturnsComments covers the worker-pinned tool — the
// path a dispatched session actually uses to read its own task.
func TestLoopbackTaskGet_ReturnsComments(t *testing.T) {
	fix := setupLoopback(t)

	for i := 1; i <= 3; i++ {
		_, isErr := callTool(t, fix.global, "torque_comment_add", map[string]interface{}{
			"entity_type": "task",
			"entity_id":   fix.taskID,
			"author":      "planner",
			"content":     numberedBody(i),
		})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, fix.loopback, "torque_task_get", map[string]interface{}{})
	require.False(t, isErr, "loopback task_get: %s", text)

	var got taskGetPayload
	parseData(t, text, &got)
	require.NotNil(t, got.Comments, "the worker's own task_get must carry the thread")
	require.NotNil(t, got.CommentsMeta)
	require.Len(t, *got.Comments, 3)
	assert.Equal(t, "comment number 1", (*got.Comments)[0].Content)
	assert.Equal(t, "comment number 3", (*got.Comments)[2].Content)
	assert.Equal(t, 3, got.CommentsMeta.Total)
	assert.False(t, got.CommentsMeta.Truncated)

	// Opt-out works on the loopback tool too.
	text, isErr = callTool(t, fix.loopback, "torque_task_get", map[string]interface{}{"comments": "false"})
	require.False(t, isErr, "loopback task_get opt-out: %s", text)
	var bare taskGetPayload
	parseData(t, text, &bare)
	assert.Nil(t, bare.Comments)
	assert.Nil(t, bare.CommentsMeta)
}

// TestTaskGet_WriteResponsesHaveNoComments guards the boundary: taskResult is
// shared by the write tools, and bolting a comment thread onto every task
// write was explicitly not the ask.
func TestTaskGet_WriteResponsesHaveNoComments(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 3, numberedBody)

	for _, call := range []struct {
		tool string
		args map[string]interface{}
	}{
		{"torque_task_update", map[string]interface{}{"id": taskID, "title": "renamed"}},
		{"torque_task_transition", map[string]interface{}{"id": taskID, "status": "doing"}},
	} {
		text, isErr := callTool(t, a, call.tool, call.args)
		require.False(t, isErr, "%s: %s", call.tool, text)
		var got taskGetPayload
		parseData(t, text, &got)
		assert.Nil(t, got.Comments, "%s must not carry a comment thread", call.tool)
		assert.Nil(t, got.CommentsMeta, "%s must not carry CommentsMeta", call.tool)
	}
}
