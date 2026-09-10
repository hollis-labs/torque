package service_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// taskUpdateParent builds the TaskUpdate that sets parent_id to id.
func taskUpdateParent(id string) sqlstore.TaskUpdate {
	p := sql.NullString{String: id, Valid: true}
	return sqlstore.TaskUpdate{ParentID: &p}
}

// mkTask is a tiny helper for the cycle-message tests: create a task and
// return its id.
func mkTask(t *testing.T, svc *service.Service, title string) string {
	t.Helper()
	task, err := svc.Task.Create(service.TaskCreateInput{Title: title})
	require.NoError(t, err)
	return task.ID
}

func setDeps(t *testing.T, svc *service.Service, id string, deps []string) {
	t.Helper()
	require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{DependsOn: &deps}))
}

// TestDependsOnCycle_NamesThePath is the reported friction: the rejection
// named the task being updated — which the caller already supplied — and not
// the other task in the cycle. With several candidate ids in one call, that
// left the caller guessing which proposed edge was the offender.
func TestDependsOnCycle_NamesThePath(t *testing.T) {
	svc := setupService(t)

	a := mkTask(t, svc, "A")
	b := mkTask(t, svc, "B")
	innocent := mkTask(t, svc, "innocent")

	// B depends on A. Proposing A depends on B closes the loop.
	setDeps(t, svc, b, []string{a})

	err := svc.Task.Update(a, service.TaskUpdateInput{DependsOn: &[]string{innocent, b}})
	require.Error(t, err)

	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "depends_on", verr.Field)
	assert.Equal(t, "depends_on would create a cycle: "+a+" -> "+b+" -> "+a, verr.Message,
		"the message renders the closed loop, not just the updated task")
	assert.NotContains(t, strings.TrimPrefix(verr.Message, "depends_on would create a cycle: "),
		innocent, "an innocent candidate id must not appear in the cycle")
}

// A longer chain renders every hop, so the caller can see where to break it.
func TestDependsOnCycle_NamesMultiHopPath(t *testing.T) {
	svc := setupService(t)

	a := mkTask(t, svc, "A")
	b := mkTask(t, svc, "B")
	c := mkTask(t, svc, "C")

	setDeps(t, svc, b, []string{c})
	setDeps(t, svc, c, []string{a})

	err := svc.Task.Update(a, service.TaskUpdateInput{DependsOn: &[]string{b}})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "depends_on would create a cycle: "+a+" -> "+b+" -> "+c+" -> "+a, verr.Message)
}

// A cycle found after an abandoned sibling branch renders only the cycle.
// The DFS explores the dead-end fan-out first, so this pins that the path
// carried into the surviving branch is not the one the dead end walked.
func TestDependsOnCycle_AbandonedBranchNotInPath(t *testing.T) {
	svc := setupService(t)

	a := mkTask(t, svc, "A")
	b := mkTask(t, svc, "B")
	deadEnd := mkTask(t, svc, "dead-end")
	deeper := mkTask(t, svc, "deeper")

	// B fans out: a dead-end branch explored first, then the branch home.
	setDeps(t, svc, deadEnd, []string{deeper})
	setDeps(t, svc, b, []string{deadEnd, a})

	err := svc.Task.Update(a, service.TaskUpdateInput{DependsOn: &[]string{b}})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "depends_on would create a cycle: "+a+" -> "+b+" -> "+a, verr.Message)
	assert.NotContains(t, verr.Message, deadEnd)
	assert.NotContains(t, verr.Message, deeper)
}

// Self-reference keeps its own clearer message rather than rendering "A -> A".
func TestDependsOnCycle_SelfReferenceMessageUnchanged(t *testing.T) {
	svc := setupService(t)
	a := mkTask(t, svc, "A")

	err := svc.Task.Update(a, service.TaskUpdateInput{DependsOn: &[]string{a}})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "depends_on cannot reference the task itself", verr.Message)
}

// The same defect lived in the adjacent parent_id validator. Fixing only one
// of two adjacent validators just moves the friction.
func TestParentIDCycle_NamesThePath(t *testing.T) {
	svc := setupService(t)

	a := mkTask(t, svc, "A")
	b := mkTask(t, svc, "B")
	c := mkTask(t, svc, "C")

	// B's parent is A; C's parent is B. Making A's parent C closes the loop.
	require.NoError(t, svc.Task.Update(b, service.TaskUpdateInput{
		TaskUpdate: taskUpdateParent(a),
	}))
	require.NoError(t, svc.Task.Update(c, service.TaskUpdateInput{
		TaskUpdate: taskUpdateParent(b),
	}))

	err := svc.Task.Update(a, service.TaskUpdateInput{TaskUpdate: taskUpdateParent(c)})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "parent_id", verr.Field)
	assert.Equal(t, "parent_id would create a cycle: "+a+" -> "+c+" -> "+b+" -> "+a, verr.Message)
}
