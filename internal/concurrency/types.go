package concurrency

import (
	"context"
	"database/sql"
	"time"
)

// WriteOp represents a serialized write operation to the main database.
// All writes to clockwork.db flow through the write serializer channel
// as WriteOp values.
type WriteOp struct {
	// Fn is the write function to execute inside the serialized writer.
	// It receives the single write-connection *sql.DB.
	Fn func(db *sql.DB) error

	// Result receives the error (or nil) after execution completes.
	Result chan error
}

// BufferedEvent is a high-frequency event buffered through go-queue
// before being batch-drained into the main database.
type BufferedEvent struct {
	RunID     int64     `json:"run_id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"` // "progress", "tokens", "heartbeat", "log"
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// BufferedEventType constants for hot-tier event classification.
const (
	EventTypeProgress  = "progress"
	EventTypeTokens    = "tokens"
	EventTypeHeartbeat = "heartbeat"
	EventTypeLog       = "log"
)

// DrainStats tracks write buffer drain performance for observability.
type DrainStats struct {
	BatchesDrained int64
	EventsDrained  int64
	LastDrainAt    time.Time
	LastBatchSize  int
	DrainErrors    int64
	QueueDepth     int
}

// DBPoolConfig configures the read/write connection pool.
type DBPoolConfig struct {
	// DBPath is the path to the main clockwork.db file.
	DBPath string

	// QueueDBPath is the path to the hot-tier queue.db file.
	QueueDBPath string

	// MaxReadConns is the maximum number of read connections. Default: 4.
	MaxReadConns int

	// BusyTimeoutMs is the SQLite busy_timeout PRAGMA value. Default: 5000.
	BusyTimeoutMs int

	// WriteChannelSize is the buffer size for the write serializer channel. Default: 256.
	WriteChannelSize int
}

// DefaultDBPoolConfig returns sensible defaults.
func DefaultDBPoolConfig(dbPath string) DBPoolConfig {
	return DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      "queue.db",
		MaxReadConns:     4,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 256,
	}
}

// DrainConfig configures the write buffer drain behavior.
type DrainConfig struct {
	// BatchSize is the max events to drain per batch. Default: 50.
	BatchSize int

	// DrainInterval is the time between drain cycles. Default: 1s.
	DrainInterval time.Duration

	// QueueName is the go-queue queue name for hot events. Default: "hot_events".
	QueueName string
}

// DefaultDrainConfig returns sensible defaults.
func DefaultDrainConfig() DrainConfig {
	return DrainConfig{
		BatchSize:     50,
		DrainInterval: time.Second,
		QueueName:     "hot_events",
	}
}

// WriteSerializer is the interface for the single-writer goroutine.
type WriteSerializer interface {
	// Submit sends a write operation to the serializer and blocks until complete.
	Submit(ctx context.Context, fn func(db *sql.DB) error) error

	// Start begins the write serializer goroutine. Blocks until ctx is cancelled.
	Start(ctx context.Context) error

	// Stop signals the serializer to drain and shut down.
	Stop()
}

// WriteBuffer is the interface for the go-queue backed hot write buffer.
type WriteBuffer interface {
	// Push enqueues a high-frequency event to the hot tier (queue.db).
	Push(ctx context.Context, event BufferedEvent) error

	// StartDrain begins the drain goroutine that batches events into the main DB.
	StartDrain(ctx context.Context) error

	// Stats returns current drain statistics.
	Stats() DrainStats

	// Stop signals the drain to finish current batch and shut down.
	Stop()
}
