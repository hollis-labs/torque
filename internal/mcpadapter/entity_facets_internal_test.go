package mcpadapter

import (
	"encoding/json"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestEntityFacetByteBudgetPreservesCounts(t *testing.T) {
	r := sqlstore.EntityFacetResult{MatchingCount: 400, BucketLimit: 200, Dimensions: []string{"profile"}, Facets: []sqlstore.EntityFacet{{Dimension: "profile", TotalDistinct: 400, Returned: 200, Truncated: true}}, Totals: &sqlstore.RunFacetTotals{Cost: 7, PromptTokens: 10, CompletionTokens: 20}, TaskRollups: &sqlstore.ParentTaskRollups{TotalDistinct: 400, Returned: 200, Truncated: true}}
	for i := 0; i < 200; i++ {
		value := strings.Repeat("x", 1000)
		r.Facets[0].Buckets = append(r.Facets[0].Buckets, sqlstore.EntityFacetBucket{Value: value, Count: 1})
		r.TaskRollups.Scopes = append(r.TaskRollups.Scopes, sqlstore.ParentTaskRollup{ScopeID: value, Total: 2, Counts: map[string]int{"done": 1, "todo": 1}})
	}
	got, err := cappedEntityFacetResult(r)
	require.NoError(t, err)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), maxMCPResponseBytes)
	out := got.(Response).Data.(sqlstore.EntityFacetResult)
	require.Equal(t, 400, out.MatchingCount)
	require.Equal(t, 400, out.Facets[0].TotalDistinct)
	require.Equal(t, r.Totals, out.Totals)
	require.True(t, out.Facets[0].Truncated)
	require.True(t, out.TaskRollups.Truncated)
	require.Equal(t, len(out.Facets[0].Buckets), out.Facets[0].Returned)
	require.Equal(t, len(out.TaskRollups.Scopes), out.TaskRollups.Returned)
	for _, scope := range out.TaskRollups.Scopes {
		require.Equal(t, map[string]int{"done": 1, "todo": 1}, scope.Counts)
	}
}
