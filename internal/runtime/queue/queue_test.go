package queue_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupQueue(t *testing.T) *queue.Queue {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "queue.db")

	q, err := queue.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { q.Close() })
	return q
}

func TestEnqueueAndDequeue(t *testing.T) {
	q := setupQueue(t)

	err := q.Enqueue(context.Background(), &queue.Job{
		ID:      "job-001",
		TaskID:  "CW-20260407-0001",
		RunID:   1,
		Payload: `{"description":"Fix auth bug"}`,
	})
	require.NoError(t, err)

	job, err := q.Dequeue(context.Background())
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, "job-001", job.ID)
	assert.Equal(t, "CW-20260407-0001", job.TaskID)
	assert.Equal(t, int64(1), job.RunID)
}

func TestDequeueEmptyReturnsNil(t *testing.T) {
	q := setupQueue(t)

	job, err := q.Dequeue(context.Background())
	require.NoError(t, err)
	assert.Nil(t, job)
}

func TestAckRemovesJob(t *testing.T) {
	q := setupQueue(t)

	q.Enqueue(context.Background(), &queue.Job{
		ID:     "job-001",
		TaskID: "CW-20260407-0001",
		RunID:  1,
	})

	job, _ := q.Dequeue(context.Background())
	require.NotNil(t, job)

	err := q.Ack(context.Background(), job.ID)
	require.NoError(t, err)

	// Should be empty now
	next, err := q.Dequeue(context.Background())
	require.NoError(t, err)
	assert.Nil(t, next)
}

func TestFailRequeuesJob(t *testing.T) {
	q := setupQueue(t)

	q.Enqueue(context.Background(), &queue.Job{
		ID:     "job-001",
		TaskID: "CW-20260407-0001",
		RunID:  1,
	})

	job, _ := q.Dequeue(context.Background())
	require.NotNil(t, job)

	err := q.Fail(context.Background(), job.ID, "transient error")
	require.NoError(t, err)

	// Should be available again
	next, err := q.Dequeue(context.Background())
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, "job-001", next.ID)
}

func TestQueueDepth(t *testing.T) {
	q := setupQueue(t)

	q.Enqueue(context.Background(), &queue.Job{ID: "job-001", TaskID: "T1", RunID: 1})
	q.Enqueue(context.Background(), &queue.Job{ID: "job-002", TaskID: "T2", RunID: 2})
	q.Enqueue(context.Background(), &queue.Job{ID: "job-003", TaskID: "T3", RunID: 3})

	depth, err := q.Depth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, depth)
}

func TestQueuePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "queue.db")

	// Enqueue and close
	q1, err := queue.Open(context.Background(), dbPath)
	require.NoError(t, err)
	q1.Enqueue(context.Background(), &queue.Job{ID: "job-001", TaskID: "T1", RunID: 1})
	q1.Close()

	// Reopen and dequeue
	q2, err := queue.Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer q2.Close()

	job, err := q2.Dequeue(context.Background())
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, "job-001", job.ID)
}

// Remove unused import guard
var _ = os.TempDir
var _ = time.Now
