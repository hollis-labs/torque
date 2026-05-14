package service_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// TestTaskCreate_ParentIDRoundTrip verifies that Create persists parent_id
// when the referenced parent exists.
func TestTaskCreate_ParentIDRoundTrip(t *testing.T) {
	svc := setupService(t)

	parent, err := svc.Task.Create(service.TaskCreateInput{Title: "parent"})
	require.NoError(t, err)

	child, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "child",
		ParentID: parent.ID,
	})
	require.NoError(t, err)
	require.True(t, child.ParentID.Valid)
	require.Equal(t, parent.ID, child.ParentID.String)
}

// TestTaskCreate_ParentIDMissing verifies that Create rejects a parent_id
// that does not reference an existing task.
func TestTaskCreate_ParentIDMissing(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "orphan",
		ParentID: "CW-NOPE-0001",
	})
	require.Error(t, err)
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
	require.Equal(t, "parent_id", vErr.Field)
}

// TestTaskUpdate_ParentIDSelfReference verifies that an update pointing a
// task's parent_id at itself is rejected.
func TestTaskUpdate_ParentIDSelfReference(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)

	err = svc.Task.Update(t1.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{
			ParentID: &sql.NullString{String: t1.ID, Valid: true},
		},
	})
	require.Error(t, err)
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
	require.Equal(t, "parent_id", vErr.Field)
}

// TestTaskUpdate_ParentIDCycle verifies that an update which would close a
// cycle (A → B → C → A) is rejected.
func TestTaskUpdate_ParentIDCycle(t *testing.T) {
	svc := setupService(t)

	a, err := svc.Task.Create(service.TaskCreateInput{Title: "A"})
	require.NoError(t, err)
	b, err := svc.Task.Create(service.TaskCreateInput{Title: "B", ParentID: a.ID})
	require.NoError(t, err)
	c, err := svc.Task.Create(service.TaskCreateInput{Title: "C", ParentID: b.ID})
	require.NoError(t, err)

	// Attempting to set A.parent_id = C closes the cycle A → C → B → A.
	err = svc.Task.Update(a.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{
			ParentID: &sql.NullString{String: c.ID, Valid: true},
		},
	})
	require.Error(t, err)
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
	require.Equal(t, "parent_id", vErr.Field)
}

// TestTaskUpdate_ParentIDClear verifies that a NullString with Valid=false
// clears the parent linkage without tripping the cycle check.
func TestTaskUpdate_ParentIDClear(t *testing.T) {
	svc := setupService(t)

	parent, err := svc.Task.Create(service.TaskCreateInput{Title: "parent"})
	require.NoError(t, err)
	child, err := svc.Task.Create(service.TaskCreateInput{Title: "child", ParentID: parent.ID})
	require.NoError(t, err)
	require.True(t, child.ParentID.Valid)

	err = svc.Task.Update(child.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{
			ParentID: &sql.NullString{Valid: false},
		},
	})
	require.NoError(t, err)

	refreshed, err := svc.Task.Get(child.ID)
	require.NoError(t, err)
	require.False(t, refreshed.ParentID.Valid)
}

// TestTaskCreate_WorkingDirInheritsFromParent verifies CW-20260508-0004:
// a child task created without WorkingDir inherits from the parent when
// the parent has a non-empty WorkingDir. Surfaced in S2.5 plan-execute
// smoke (2026-05-08): orchestrator created kind=internal planner sub-task
// via torque_task_create without working_dir; scheduler dispatch then
// failed with "agent.Executor: working_dir is required". Auto-inheritance
// makes the common case (LLM-driven sub-task creation) work without
// every caller knowing to pass the field.
func TestTaskCreate_WorkingDirInheritsFromParent(t *testing.T) {
	svc := setupService(t)

	parent, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "parent",
		WorkingDir: "/Users/test/projects/widget",
	})
	require.NoError(t, err)
	require.Equal(t, "/Users/test/projects/widget", parent.WorkingDir)

	child, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "child",
		ParentID: parent.ID,
		// WorkingDir intentionally omitted — should inherit
	})
	require.NoError(t, err)
	require.Equal(t, "/Users/test/projects/widget", child.WorkingDir,
		"child should inherit working_dir from parent")
}

// TestTaskCreate_WorkingDirExplicitWinsOverParent verifies that a caller-
// provided WorkingDir is NOT overwritten by the parent's value. Inheritance
// is a fallback for the empty case only.
func TestTaskCreate_WorkingDirExplicitWinsOverParent(t *testing.T) {
	svc := setupService(t)

	parent, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "parent",
		WorkingDir: "/Users/test/projects/parent-dir",
	})
	require.NoError(t, err)

	child, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "child",
		ParentID:   parent.ID,
		WorkingDir: "/Users/test/projects/child-dir",
	})
	require.NoError(t, err)
	require.Equal(t, "/Users/test/projects/child-dir", child.WorkingDir,
		"explicit child WorkingDir must NOT be overwritten by parent")
}

// TestTaskCreate_WorkingDirNoParentNoInheritance verifies that a root task
// (no parent_id) with empty WorkingDir is unchanged — the executor will
// reject at dispatch if the task is kind=agent or kind=internal, but Create
// itself doesn't enforce non-empty here. (See agent.Executor for dispatch-
// time validation.)
func TestTaskCreate_WorkingDirNoParentNoInheritance(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title: "root-no-workdir",
	})
	require.NoError(t, err)
	require.Equal(t, "", task.WorkingDir,
		"root task without WorkingDir stays empty — executor catches at dispatch")
}

// TestTaskCreate_WorkingDirParentEmptyNoInheritance verifies that when the
// parent has an empty WorkingDir, no inheritance happens — child.WorkingDir
// stays empty. (Both columns are plain strings, not sql.NullString — the
// inherit guard is just `parent.WorkingDir != ""`.)
func TestTaskCreate_WorkingDirParentEmptyNoInheritance(t *testing.T) {
	svc := setupService(t)

	parent, err := svc.Task.Create(service.TaskCreateInput{Title: "parent-no-workdir"})
	require.NoError(t, err)
	require.Equal(t, "", parent.WorkingDir)

	child, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "child",
		ParentID: parent.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "", child.WorkingDir)
}

// TestTaskList_ParentIDFilter verifies that filtering by parent_id returns
// only the children of a plan, and the ParentIDNull variant returns roots.
func TestTaskList_ParentIDFilter(t *testing.T) {
	svc := setupService(t)

	root1, err := svc.Task.Create(service.TaskCreateInput{Title: "root1"})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "root2"})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "c1", ParentID: root1.ID})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "c2", ParentID: root1.ID})
	require.NoError(t, err)

	children, err := svc.Task.List(sqlstore.TaskFilter{ParentID: root1.ID})
	require.NoError(t, err)
	require.Len(t, children, 2)

	roots, err := svc.Task.List(sqlstore.TaskFilter{ParentIDNull: true})
	require.NoError(t, err)
	require.Len(t, roots, 2)
}
