package mcpadapter

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Comment-tail defaults for torque_task_get (CW-20260910-0057). Kept beside
// response.go's list limits in spirit but named for this one surface: the
// window is deliberately small because it rides along with a full TaskRecord
// (unbounded description column) rather than a brief shape.
const (
	defaultTaskGetCommentsLimit = 10
	maxTaskGetCommentsLimit     = 100
)

// taskComment is the per-comment shape inside torque_task_get's Comments[].
// Deliberately NOT sqlstore.CommentRecord: entity_type/entity_id are already
// implied by the task the caller just asked for, and every byte spent
// repeating them is a byte the cap has to take out of the thread.
type taskComment struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// commentsMeta states what the Comments[] window contains and, crucially,
// what it does NOT. It is present on every comments-bearing response —
// including when nothing was omitted (omitted=0, truncated=false).
//
// That "always present" rule is the whole point of CW-20260910-0057: the
// failure being fixed is a reader inferring completeness from silence. A
// caller must never have to deduce from an array length whether it is
// looking at the whole thread.
type commentsMeta struct {
	// Returned is len(Comments) — how many are actually in this response.
	Returned int `json:"returned"`
	// Total is the number of comments on the task, from a COUNT query.
	// Never derived from len() over a full fetch — counting by loading the
	// thread would defeat the window.
	Total int `json:"total"`
	// Omitted is Total-Returned: comments that exist and are not here.
	Omitted int `json:"omitted"`
	// Truncated is Omitted > 0, stated explicitly so a caller checking one
	// boolean gets the right answer without arithmetic.
	Truncated bool `json:"truncated"`
	// Hint names the call that retrieves what was cut. Empty when nothing
	// was cut.
	Hint string `json:"hint,omitempty"`
}

// taskWithComments is the torque_task_get response shape when comments are
// included: the normal taskWithTags record plus the tail window and its
// meta. When comments="false" the handler returns bare taskWithTags instead,
// so the two keys are absent rather than empty — an explicit opt-out should
// not leave residue a caller has to interpret.
type taskWithComments struct {
	*taskWithTags
	Comments     []taskComment `json:"Comments"`
	CommentsMeta commentsMeta  `json:"CommentsMeta"`
}

// commentTailHint is the omission notice. It names the exact call that
// retrieves the full thread — an agent reading this should not have to
// reconstruct the tool arguments from the tool list.
func commentTailHint(taskID string, omitted int, recordOversized bool) string {
	if omitted <= 0 {
		return ""
	}
	noun := "older comments"
	if omitted == 1 {
		noun = "older comment"
	}
	prefix := fmt.Sprintf("%d %s omitted", omitted, noun)
	if recordOversized {
		prefix = fmt.Sprintf("the task record alone exceeds the %dKB response cap; all %d %s omitted",
			maxMCPResponseBytes/1024, omitted, noun)
	}
	return fmt.Sprintf(`%s — torque_comment_list entity_type="task" entity_id=%q for the full thread`,
		prefix, taskID)
}

// commentTail returns the newest `limit` comments on a task, ordered oldest
// → newest, along with the task's total comment count.
//
// Two orderings are in play and they are deliberately different. Selection
// takes the TAIL (newest first, LIMIT n at the store) because recent
// comments are the ones carrying corrections. Presentation reverses that to
// created_at ASC — matching torque_comment_list's per-entity default — so a
// thread of successive corrections reads forward, in the order it was
// written, instead of backwards.
func (a *Adapter) commentTail(taskID string, limit int) ([]taskComment, int, error) {
	total, err := a.svc.Comment.CountForEntity(sqlstore.EntityTypeTask, taskID)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 || limit <= 0 {
		return []taskComment{}, total, nil
	}
	// SortBy empty selects SearchComments' default `created_at DESC, id DESC`
	// — newest first, which with LIMIT is exactly the tail we want.
	newestFirst, err := a.svc.Comment.ListFiltered(sqlstore.CommentFilter{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   taskID,
		Limit:      limit,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]taskComment, 0, len(newestFirst))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		c := newestFirst[i]
		out = append(out, taskComment{
			ID:        c.ID,
			Author:    c.Author,
			Content:   c.Content,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}
	return out, total, nil
}

// taskGetResult is the torque_task_get response builder — the cross-task
// tool and the loopback's worker-pinned one both go through it.
//
// It is deliberately NOT taskResult. taskResult is shared by task_update,
// task_transition and task_create; adding comments there would bolt a
// comment thread onto every task WRITE response, which nobody asked for and
// which would multiply the payload of routine status changes.
func (a *Adapter) taskGetResult(task *sqlstore.TaskRecord, includeComments bool, commentsLimit int) (*mcp.CallToolResult, error) {
	base, errRes := a.taskWithTagsFor(task)
	if errRes != nil {
		return errRes, nil
	}
	if !includeComments {
		return okResult(base)
	}

	comments, total, err := a.commentTail(task.ID, commentsLimit)
	if err != nil {
		return errFromService(err)
	}
	return cappedTaskGetResult(task.ID, base, comments, total)
}

// cappedTaskGetResult enforces maxMCPResponseBytes on the comments-bearing
// task_get payload, dropping the OLDEST comment in the window first.
//
// The drop direction is the opposite of cappedJSONResult's, which trims from
// the tail of a list to keep the newest of a recency-sorted slice. Here the
// window is already presented oldest → newest, so the newest — the ones
// carrying the corrections this whole feature exists to surface — are at the
// END. Trimming the tail would keep exactly the stale comments and discard
// the fresh ones. Hence index 0.
//
// torque_task_get enforced no byte limit at all before this (taskResult went
// straight to okResult with an unbounded description column), so this adds
// the guard rather than reusing one.
func cappedTaskGetResult(taskID string, base *taskWithTags, comments []taskComment, total int) (*mcp.CallToolResult, error) {
	if comments == nil {
		comments = []taskComment{}
	}
	build := func(window []taskComment, recordOversized bool) taskWithComments {
		if window == nil {
			window = []taskComment{}
		}
		omitted := total - len(window)
		if omitted < 0 {
			omitted = 0
		}
		return taskWithComments{
			taskWithTags: base,
			Comments:     window,
			CommentsMeta: commentsMeta{
				Returned:  len(window),
				Total:     total,
				Omitted:   omitted,
				Truncated: omitted > 0,
				Hint:      commentTailHint(taskID, omitted, recordOversized),
			},
		}
	}

	window := comments
	for {
		payload := build(window, false)
		b, err := json.MarshalIndent(Response{OK: true, Data: payload}, "", "  ")
		if err != nil {
			log.Printf("mcpadapter: task_get marshal failed: %v", err)
			return errResult(ErrCodeInternal, "response serialization failed", "")
		}
		if len(b) <= maxMCPResponseBytes {
			return mcp.NewToolResultText(string(b)), nil
		}
		if len(window) == 0 {
			// The record alone does not fit. Return it anyway, with every
			// comment declared omitted and a hint that says why — a silent
			// transport failure, or a record quietly cut down to size,
			// would both be worse than an oversized response that admits
			// what it is.
			return okResult(build(nil, true))
		}
		// Drop the oldest comment still in the window and retry.
		window = window[1:]
	}
}
