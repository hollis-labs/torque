package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommentAdd_RejectsMissingContent is the reported defect: content is
// declared required, was not checked, and a call omitting it returned ok:true
// with a real comment ID while persisting a row that stored nothing. The
// reporting session lost four batch records this way and reported them as
// landed.
func TestCommentAdd_RejectsMissingContent(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "target"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   taskID,
	})
	require.True(t, isErr, "a missing content must be rejected, not stored: %s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "content", field)
	assert.Equal(t, "content is required", msg,
		"same text as torque_comment_bulk_add — one helper, so the paths cannot drift")

	// Nothing was persisted.
	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID,
	})
	require.False(t, isErr, text)
	var listed struct {
		Meta struct {
			Returned int `json:"returned"`
		} `json:"meta"`
	}
	parseData(t, text, &listed)
	assert.Equal(t, 0, listed.Meta.Returned)
}

// Whitespace-only content destroys the audit trail as thoroughly as none.
func TestCommentAdd_RejectsWhitespaceOnlyContent(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "target"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	for _, content := range []string{"", "   ", "\n\t "} {
		text, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "content": content,
		})
		require.True(t, isErr, "content=%q must be rejected: %s", content, text)
		_, msg, field := parseError(t, text)
		assert.Equal(t, "content is required", msg)
		assert.Equal(t, "content", field)
	}
}

// All three comment-writing tools answer identically — the asymmetry that
// made this bug findable is what the shared helper removes.
func TestCommentTools_ShareOneEmptinessContract(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "target"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "someone", "content": "real",
	})
	require.False(t, isErr, text)
	var c map[string]interface{}
	parseData(t, text, &c)
	commentID := int64(c["id"].(float64))

	calls := []struct {
		tool string
		args map[string]interface{}
	}{
		{"torque_comment_add", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "content": "  ",
		}},
		{"torque_comment_bulk_add", map[string]interface{}{
			"targets": `[{"entity_type":"task","entity_id":"` + taskID + `"}]`, "content": "  ",
		}},
		{"torque_comment_update", map[string]interface{}{
			"id": float64(commentID), "author": "someone", "content": "  ",
		}},
	}
	for _, call := range calls {
		text, isErr := callTool(t, a, call.tool, call.args)
		require.True(t, isErr, "%s must reject empty content: %s", call.tool, text)
		code, msg, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code, call.tool)
		assert.Equal(t, "content is required", msg, call.tool)
		assert.Equal(t, "content", field, call.tool)
	}
}

// TestCommentDelete_UnattributedRowIsDeletable closes the second, unverified
// claim on this task: the persisted row could not be removed.
//
// The cause is the empty AUTHOR, not the empty content — an empty-content row
// with a real author always deleted fine. author is optional on
// torque_comment_add and an omitted one stores "", so EVERY unattributed
// comment was permanently unremovable, not only bug-created ones: delete
// requires an exact author match, and the handler rejected "" as missing
// before it could ever match.
func TestCommentDelete_UnattributedRowIsDeletable(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "target"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	// An unattributed comment — omitted author, real content.
	text, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "content": "unattributed note",
	})
	require.False(t, isErr, text)
	var c map[string]interface{}
	parseData(t, text, &c)
	require.Equal(t, "", c["author"])
	commentID := c["id"].(float64)

	// A named author still cannot delete someone else's comment.
	text, isErr = callTool(t, a, "torque_comment_delete", map[string]interface{}{
		"id": commentID, "author": "someone-else",
	})
	require.True(t, isErr, "author-scoping still applies: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "permission", code)

	// An omitted author is still an error — the empty string has to be
	// deliberate, so a caller who forgets the field is not silently matched
	// against unattributed rows.
	text, isErr = callTool(t, a, "torque_comment_delete", map[string]interface{}{
		"id": commentID,
	})
	require.True(t, isErr, "%s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "author is required", msg)
	assert.Equal(t, "author", field)

	// An explicit empty author names the unattributed author and succeeds.
	text, isErr = callTool(t, a, "torque_comment_delete", map[string]interface{}{
		"id": commentID, "author": "",
	})
	require.False(t, isErr, `author:"" must match an unattributed comment: %s`, text)
	var deleted map[string]interface{}
	parseData(t, text, &deleted)
	assert.Equal(t, true, deleted["deleted"])

	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID,
	})
	require.False(t, isErr, text)
	var listed struct {
		Meta struct {
			Returned int `json:"returned"`
		} `json:"meta"`
	}
	parseData(t, text, &listed)
	assert.Equal(t, 0, listed.Meta.Returned, "the row is actually gone")
}

// A transition's optional comment must not open a second door to a blank row.
func TestTaskTransition_WhitespaceCommentWritesNoRow(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "target"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": taskID, "status": "doing", "comment": "   ",
	})
	require.False(t, isErr, "the transition itself still applies: %s", text)
	var moved map[string]interface{}
	parseData(t, text, &moved)
	assert.Equal(t, "doing", moved["Status"])

	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID,
	})
	require.False(t, isErr, text)
	var listed struct {
		Meta struct {
			Returned int `json:"returned"`
		} `json:"meta"`
	}
	parseData(t, text, &listed)
	assert.Equal(t, 0, listed.Meta.Returned, "no blank comment row")
}
