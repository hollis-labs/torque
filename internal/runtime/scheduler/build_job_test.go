package scheduler

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildJob_PopulatesMetadata(t *testing.T) {
	task := sqlstore.TaskRecord{
		ID:       "CW-T-1",
		Title:    "t",
		Metadata: sql.NullString{String: `{"timeout_seconds_override":1800,"origin":"auto"}`, Valid: true},
	}
	job := buildJob(task, 7)
	require.NotNil(t, job.Metadata)
	assert.Equal(t, float64(1800), job.Metadata["timeout_seconds_override"])
	assert.Equal(t, "auto", job.Metadata["origin"])
}

func TestBuildJob_NilMetadataWhenAbsent(t *testing.T) {
	task := sqlstore.TaskRecord{ID: "CW-T-2", Title: "t"}
	job := buildJob(task, 7)
	assert.Nil(t, job.Metadata)
}

func TestBuildJob_NilMetadataWhenInvalidJSON(t *testing.T) {
	task := sqlstore.TaskRecord{
		ID:       "CW-T-3",
		Title:    "t",
		Metadata: sql.NullString{String: `not-json`, Valid: true},
	}
	job := buildJob(task, 7)
	// Invalid JSON must not panic; Metadata stays nil so downstream code treats it as absent.
	assert.Nil(t, job.Metadata)
}
