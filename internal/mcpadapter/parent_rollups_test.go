package mcpadapter_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestParentFacetsRequestedRollupsAndCohortTotals(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for i := 0; i < 6; i++ {
		status := "active"
		if i == 4 {
			status = "inactive"
		}
		project, epic, sprint := fmt.Sprintf("P%d", i), fmt.Sprintf("E%d", i), fmt.Sprintf("S%d", i)
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: project, Name: "needle", Status: status}))
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: epic, Name: "needle", Status: status, ProjectID: str(project)}))
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: sprint, Name: "needle", Status: status, ProjectID: str(project)}))
		// Descending task totals make parent 2 a non-top-N group; parent 3 has
		// only an internal task and must still have an explicit zero rollup.
		for j := 0; j < 6-i; j++ {
			kind := "agent"
			if i == 3 {
				kind = "internal"
			}
			taskStatus := []string{"todo", "doing", "done"}[j%3]
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: fmt.Sprintf("T%d-%d", i, j), Title: "child", Status: taskStatus, Kind: kind, Executor: "cli", ProjectID: str(project), EpicID: str(epic), SprintID: str(sprint)}))
		}
	}
	require.NoError(t, store.ArchiveProject("P5"))
	require.NoError(t, store.ArchiveEpic("E5"))
	require.NoError(t, store.ArchiveSprint("S5"))
	// Additional children distinguish project child counts from parent counts,
	// and exercise archived children independently of the parent archive flag.
	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "extra-E", Name: "other", ProjectID: str("P2")}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "extra-S", Name: "other", ProjectID: str("P2")}))
	require.NoError(t, store.ArchiveEpic("extra-E"))
	require.NoError(t, store.ArchiveSprint("extra-S"))

	for _, entity := range []string{"project", "epic", "sprint"} {
		t.Run(entity, func(t *testing.T) {
			prefix := strings.ToUpper(entity[:1])
			for _, archived := range []bool{false, true} {
				t.Run(fmt.Sprintf("archived=%v", archived), func(t *testing.T) {
					query := url.Values{"status": {"active"}, "search": {"needle"}, "bucket_limit": {"1"}, "include_archived": {fmt.Sprint(archived)}}
					get := func(q url.Values) sqlstore.EntityFacetResult {
						resp, err := http.Get(ts.URL + "/api/v1/" + entity + "s/facets?" + q.Encode())
						require.NoError(t, err)
						defer resp.Body.Close()
						require.Equal(t, http.StatusOK, resp.StatusCode)
						var got sqlstore.EntityFacetResult
						require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
						args := map[string]any{}
						for key, values := range q {
							args[key] = values[0]
						}
						raw, isErr := callTool(t, a, "torque_"+entity+"_facets", args)
						require.False(t, isErr, raw)
						var mcp sqlstore.EntityFacetResult
						parseData(t, raw, &mcp)
						require.Equal(t, got, mcp)
						return got
					}
					baseline := get(query)
					require.Greater(t, baseline.MatchingCount, baseline.BucketLimit)
					require.Equal(t, prefix+"0", baseline.TaskRollups.Scopes[0].ScopeID)
					require.True(t, baseline.TaskRollups.Truncated)
					// Trim/deduplicate IDs, omit inactive/unknown IDs, retain zero-task IDs.
					query.Set("rollup_ids", " "+prefix+"2, "+prefix+"3,"+prefix+"2,"+prefix+"4,"+prefix+"5,missing,, ")
					got := get(query)
					require.Equal(t, baseline.MatchingCount, got.MatchingCount)
					require.Equal(t, baseline.Facets, got.Facets)
					require.Equal(t, baseline.TaskTotals, got.TaskTotals)
					require.Equal(t, baseline.ChildTotals, got.ChildTotals)
					expectedIDs := []string{prefix + "2", prefix + "3"}
					if archived {
						expectedIDs = []string{prefix + "2", prefix + "5", prefix + "3"}
					}
					require.Len(t, got.TaskRollups.Scopes, len(expectedIDs))
					require.Equal(t, len(expectedIDs), got.TaskRollups.TotalDistinct)
					require.False(t, got.TaskRollups.Truncated)
					for i, id := range expectedIDs {
						require.Equal(t, id, got.TaskRollups.Scopes[i].ScopeID)
					}
					require.Equal(t, 4, got.TaskRollups.Scopes[0].Total)
					require.Equal(t, map[string]int{"todo": 2, "doing": 1, "done": 1}, got.TaskRollups.Scopes[0].Counts)
					zero := got.TaskRollups.Scopes[len(expectedIDs)-1]
					require.Zero(t, zero.Total)
					require.Equal(t, map[string]int{}, zero.Counts)
					// Brute-force fixture sum over actual parent list rows and task rows.
					var parentIDs []string
					switch entity {
					case "project":
						parents, err := store.ListProjects(sqlstore.ProjectFilter{Status: "active", Search: "needle", IncludeArchived: archived})
						require.NoError(t, err)
						for _, p := range parents {
							parentIDs = append(parentIDs, p.ID)
						}
					case "epic":
						parents, err := store.ListEpics(sqlstore.EpicFilter{Status: "active", Search: "needle", IncludeArchived: archived})
						require.NoError(t, err)
						for _, p := range parents {
							parentIDs = append(parentIDs, p.ID)
						}
					case "sprint":
						parents, err := store.ListSprints(sqlstore.SprintFilter{Status: "active", Search: "needle", IncludeArchived: archived})
						require.NoError(t, err)
						for _, p := range parents {
							parentIDs = append(parentIDs, p.ID)
						}
					}
					totals := sqlstore.ParentTaskTotals{Counts: map[string]int{}}
					children := sqlstore.ProjectChildCounts{}
					for _, id := range parentIDs {
						filter := sqlstore.TaskFilter{}
						switch entity {
						case "project":
							filter.ProjectID = id
						case "epic":
							filter.EpicID = id
						case "sprint":
							filter.SprintID = id
						}
						tasks, err := store.ListTasks(filter)
						require.NoError(t, err)
						for _, task := range tasks {
							if task.Kind != "internal" {
								totals.Total++
								totals.Counts[task.Status]++
							}
						}
						if entity == "project" {
							epics, err := store.ListEpics(sqlstore.EpicFilter{ProjectID: id, IncludeArchived: archived})
							require.NoError(t, err)
							sprints, err := store.ListSprints(sqlstore.SprintFilter{ProjectID: id, IncludeArchived: archived})
							require.NoError(t, err)
							children.Epics += len(epics)
							children.Sprints += len(sprints)
							for _, rollup := range got.TaskRollups.Scopes {
								if rollup.ScopeID == id {
									require.Equal(t, &sqlstore.ProjectChildCounts{Epics: len(epics), Sprints: len(sprints)}, rollup.Children)
								}
							}
						}
					}
					require.Equal(t, &totals, got.TaskTotals)
					if entity == "project" {
						require.Equal(t, &children, got.ChildTotals)
					} else {
						require.Nil(t, got.ChildTotals)
						require.Nil(t, got.TaskRollups.Scopes[0].Children)
					}
					query.Set("rollup_ids", "")
					blank := get(query)
					require.Empty(t, blank.TaskRollups.Scopes)
					require.Zero(t, blank.TaskRollups.TotalDistinct)
					require.False(t, blank.TaskRollups.Truncated)
					require.Equal(t, got.TaskTotals, blank.TaskTotals)
					require.Equal(t, got.ChildTotals, blank.ChildTotals)
					query.Set("status", "no-match")
					empty := get(query)
					require.Zero(t, empty.TaskTotals.Total)
					require.Empty(t, empty.TaskTotals.Counts)
					if entity == "project" {
						require.Equal(t, &sqlstore.ProjectChildCounts{}, empty.ChildTotals)
					}
				})
			}
		})
	}
}

func TestParentFacetRollupIDValidation(t *testing.T) {
	a, ts, _ := setupAdjacentQueryParitySurfaces(t)
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = fmt.Sprintf("ID-%d", i)
	}
	for _, entity := range []string{"project", "epic", "sprint"} {
		t.Run(entity, func(t *testing.T) {
			rawIDs := strings.Join(ids, ",")
			resp, err := http.Get(ts.URL + "/api/v1/" + entity + "s/facets?rollup_ids=" + url.QueryEscape(rawIDs))
			require.NoError(t, err)
			require.Equal(t, 400, resp.StatusCode)
			var body map[string]any
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			resp.Body.Close()
			require.Contains(t, fmt.Sprint(body), "rollup_ids")
			raw, isErr := callTool(t, a, "torque_"+entity+"_facets", map[string]any{"rollup_ids": rawIDs})
			require.True(t, isErr, raw)
			require.Contains(t, raw, "rollup_ids")
			raw, isErr = callTool(t, a, "torque_"+entity+"_facets", map[string]any{"rollup_ids": 123})
			require.True(t, isErr, raw)
			require.Contains(t, raw, "rollup_ids")
			// Exactly 200 distinct IDs is accepted even with duplicates and blanks.
			valid := strings.Join(ids[:200], ",") + ", ID-0,,"
			resp, err = http.Get(ts.URL + "/api/v1/" + entity + "s/facets?rollup_ids=" + url.QueryEscape(valid))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			resp.Body.Close()
			raw, isErr = callTool(t, a, "torque_"+entity+"_facets", map[string]any{"rollup_ids": valid})
			require.False(t, isErr, raw)
		})
	}
}
