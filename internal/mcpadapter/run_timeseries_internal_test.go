package mcpadapter

import (
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestRunTimeSeriesByteBudgetRejectsIncompleteSeries(t *testing.T) {
	result := service.RunTimeSeriesResult{Buckets: []service.RunTimeSeriesBucket{{StatusCounts: map[string]int{strings.Repeat("x", maxMCPResponseBytes): 1}}}}
	_, err := cappedRunTimeSeriesResult(result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "until")
	require.Contains(t, err.Error(), "shorten")
	require.Len(t, result.Buckets, 1)
}
