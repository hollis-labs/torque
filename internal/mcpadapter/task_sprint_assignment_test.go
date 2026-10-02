package mcpadapter_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestTaskSprintAssignmentHTTPMCP(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	active, err := svc.Sprint.Create(service.SprintCreateInput{Name: "active"})
	require.NoError(t, err)
	inactive, err := svc.Sprint.Create(service.SprintCreateInput{Name: "inactive"})
	require.NoError(t, err)
	require.NoError(t, svc.Sprint.Transition(inactive.ID, "inactive"))
	closed, err := svc.Sprint.Create(service.SprintCreateInput{Name: "closed"})
	require.NoError(t, err)
	members := map[string]*sqlstore.TaskRecord{}
	for _, transport := range []string{"http", "mcp", "bulk"} {
		members[transport], err = svc.Task.Create(service.TaskCreateInput{Title: transport, Manual: true, SprintID: closed.ID})
		require.NoError(t, err)
	}
	require.NoError(t, svc.Sprint.Transition(closed.ID, "completed"))
	for _, transport := range []string{"http", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			update := func(id, target string, wantError bool) {
				t.Helper()
				if transport == "http" {
					body, err := json.Marshal(map[string]any{"sprint_id": target, "title": "edited"})
					require.NoError(t, err)
					req, err := http.NewRequest("PUT", ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(body))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					require.NoError(t, err)
					defer resp.Body.Close()
					if wantError {
						require.Equal(t, 422, resp.StatusCode)
					} else {
						require.Equal(t, 200, resp.StatusCode)
					}
				} else {
					raw, bad := callTool(t, a, "torque_task_update", map[string]any{"id": id, "sprint_id": target, "title": "edited"})
					require.Equal(t, wantError, bad, raw)
					if wantError {
						require.Contains(t, raw, "sprint_id")
						require.Contains(t, raw, "arg_invalid")
					}
				}
			}
			task, err := svc.Task.Create(service.TaskCreateInput{Title: "outside", Manual: true})
			require.NoError(t, err)
			for _, target := range []string{active.ID, inactive.ID} {
				update(task.ID, target, false)
				got, err := svc.Task.Get(task.ID)
				require.NoError(t, err)
				require.Equal(t, target, got.SprintID.String)
			}
			for _, target := range []string{closed.ID, "missing-sprint"} {
				update(task.ID, target, true)
				got, err := svc.Task.Get(task.ID)
				require.NoError(t, err)
				require.Equal(t, inactive.ID, got.SprintID.String)
			}
			member := members[transport]
			update(member.ID, closed.ID, false)
			got, err := svc.Task.Get(member.ID)
			require.NoError(t, err)
			require.Equal(t, "edited", got.Title)
			require.Equal(t, closed.ID, got.SprintID.String)
			update(member.ID, "", false)
			got, err = svc.Task.Get(member.ID)
			require.NoError(t, err)
			require.False(t, got.SprintID.Valid)
		})
	}
	outsider, err := svc.Task.Create(service.TaskCreateInput{Title: "bulk outside", Manual: true})
	require.NoError(t, err)
	ids, _ := json.Marshal([]string{members["bulk"].ID, outsider.ID})
	raw, bad := callTool(t, a, "torque_task_bulk_update", map[string]any{"ids": string(ids), "sprint_id": closed.ID})
	require.False(t, bad, raw)
	var result struct {
		Succeeded []string `json:"succeeded"`
		Failed    []struct {
			ID    string                                `json:"id"`
			Error struct{ Code, Field, Message string } `json:"error"`
		} `json:"failed"`
	}
	parseData(t, raw, &result)
	require.Equal(t, []string{members["bulk"].ID}, result.Succeeded)
	require.Len(t, result.Failed, 1)
	require.Equal(t, outsider.ID, result.Failed[0].ID)
	require.Equal(t, "sprint_id", result.Failed[0].Error.Field)
	require.Equal(t, "arg_invalid", result.Failed[0].Error.Code)
	require.Contains(t, result.Failed[0].Error.Message, "cannot add tasks to a completed sprint")
	// A missing sprint becomes a service validation error before a raw FK write.
	assignment := sql.NullString{String: "missing-sprint", Valid: true}
	err = svc.Task.Update(outsider.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{SprintID: &assignment}})
	var validation *service.ValidationError
	require.ErrorAs(t, err, &validation)
	require.Equal(t, "sprint_id", validation.Field)
}
