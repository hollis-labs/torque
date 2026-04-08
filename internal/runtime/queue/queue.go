package queue

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Job represents a unit of work in the queue.
type Job struct {
	ID         string
	TaskID     string
	RunID      int64
	Payload    string
	EnqueuedAt time.Time
	Status     string // "pending", "processing", "done", "failed"
}

// Queue is a persistent job queue backed by SQLite.
type Queue struct {
	db *sql.DB
}

// Open creates or opens a SQLite-backed queue at the given path.
func Open(dbPath string) (*Queue, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open queue db: %w", err)
	}

	// SQLite hardening
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue busy_timeout: %w", err)
	}

	// Create queue table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS queue_jobs (
		id          TEXT PRIMARY KEY,
		task_id     TEXT NOT NULL,
		run_id      INTEGER NOT NULL,
		payload     TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'pending',
		enqueued_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		started_at  DATETIME,
		error       TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create queue table: %w", err)
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_jobs(status)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create queue index: %w", err)
	}

	return &Queue{db: db}, nil
}

// Enqueue adds a job to the queue.
func (q *Queue) Enqueue(ctx context.Context, job *Job) error {
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO queue_jobs (id, task_id, run_id, payload, status) VALUES (?, ?, ?, ?, 'pending')`,
		job.ID, job.TaskID, job.RunID, job.Payload,
	)
	return err
}

// Dequeue atomically claims the oldest pending job. Returns nil if the queue is empty.
func (q *Queue) Dequeue(ctx context.Context) (*Job, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	job := &Job{}
	err = tx.QueryRowContext(ctx,
		`SELECT id, task_id, run_id, payload, enqueued_at FROM queue_jobs
		 WHERE status = 'pending'
		 ORDER BY enqueued_at ASC
		 LIMIT 1`,
	).Scan(&job.ID, &job.TaskID, &job.RunID, &job.Payload, &job.EnqueuedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE queue_jobs SET status = 'processing', started_at = CURRENT_TIMESTAMP WHERE id = ?`,
		job.ID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	job.Status = "processing"
	return job, nil
}

// Ack marks a job as done and removes it from the queue.
func (q *Queue) Ack(ctx context.Context, jobID string) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM queue_jobs WHERE id = ?`, jobID)
	return err
}

// Fail marks a job as failed and resets it to pending for retry.
func (q *Queue) Fail(ctx context.Context, jobID string, reason string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE queue_jobs SET status = 'pending', started_at = NULL, error = ? WHERE id = ?`,
		reason, jobID,
	)
	return err
}

// Depth returns the number of pending jobs in the queue.
func (q *Queue) Depth(ctx context.Context) (int, error) {
	var count int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_jobs WHERE status = 'pending'`).Scan(&count)
	return count, err
}

// Close closes the underlying database connection.
func (q *Queue) Close() error {
	return q.db.Close()
}
